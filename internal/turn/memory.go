package turn

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// This file joins the memory pass (P6-06) to a turn. When a turn ends with
// a whole reply, its pass is queued in the tenant's database and the
// chat's memory worker starts beside the turn, in a goroutine of its own
// with a store of its own, so a reply is never held for it. The worker
// takes the chat's passes one at a time, oldest first: each reads the
// person's message and the reply, asks the model routing gives the memory
// task for the durable facts in them, and adds those facts to the person's
// knowledge store as one source that points back to the chat.
//
// internal/knowledge/memory decides what is asked, how the answer is read,
// where facts may land and what the reserve allows. It is a guard of fixed
// rule 3 and reads no setting, so the settings are read here.

// stepMemory is the step a memory pass's call is made with. Runner.used
// records it as a call of the memory kind.
const stepMemory = -2

// The bounds of knowledge.memory_timeout.
const (
	memoryTimeoutMin = 5 * time.Second
	memoryTimeoutMax = 30 * time.Minute
)

// memoryWorkers is which chats have a memory worker at work in this
// process. Its zero value is ready to use.
type memoryWorkers struct {
	mu sync.Mutex
	on map[string]bool
}

// claim takes a chat for a worker. It is refused while one is at work.
func (w *memoryWorkers) claim(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.on == nil {
		w.on = map[string]bool{}
	}
	if w.on[key] {
		return false
	}
	w.on[key] = true
	return true
}

// release gives a chat back.
func (w *memoryWorkers) release(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.on, key)
}

// memoryLater queues the memory pass of a turn that has ended, and starts
// the chat's memory worker unless one is at work. It returns at once and
// never changes how the turn ended. A pass is queued only for a turn whose
// reply is whole, for a person whose memory pass is on and who has a
// knowledge store, in a chat that is not incognito. Otherwise nothing is
// written, which is what an incognito chat asks.
func (r *Runner) memoryLater(s *tenant.Store, t row, status string) {
	if status != StatusDone || r.setup() != nil {
		return
	}
	ctx := context.Background()
	if r.setting(ctx, s, memory.SettingOn) != "true" {
		return
	}
	if name := r.setting(ctx, s, knowledge.SettingAdapter); name == "" || name == knowledge.None {
		return
	}
	incognito, err := memory.Incognito(ctx, s, t.ChatID)
	if err != nil {
		r.logf("turns: %s turn %s: reading whether the chat is incognito, so no memory pass is made: %v", t.requestID, t.ID, err)
		return
	}
	if incognito {
		return
	}
	if r.o.SkipMemory != nil && r.o.SkipMemory(ctx, s, t.ChatID) {
		return // a wiki upkeep job's run (P8-06)
	}
	if _, err := memory.Queue(ctx, s, t.ID, t.ChatID); err != nil {
		r.logf("turns: %s turn %s: the memory pass was not queued: %v", t.requestID, t.ID, err)
		return
	}
	r.memoryWork(s.TenantID(), t.ChatID)
}

// memoryWork starts a chat's memory worker, unless one is at work or the
// runner is closing.
func (r *Runner) memoryWork(tenantID, chatID string) {
	key := tenantID + "/" + chatID
	if !r.memory.claim(key) {
		return
	}
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		r.memory.release(key)
		return
	}
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		r.memoryDrain(tenantID, chatID, key)
	}()
}

// memoryDrain works a chat's queued passes, oldest first, until none
// waits. A pass a stop left running is marked interrupted first, with a
// line in the log. A pass queued while the worker was letting go is never
// missed: the worker looks again after it lets go, and takes the chat back
// if it can.
func (r *Runner) memoryDrain(tenantID, chatID, key string) {
	s, err := r.o.Install.Store(tenantID)
	if err != nil {
		r.memory.release(key)
		r.logf("turns: the memory pass: opening the store of tenant %s: %v", tenantID, err)
		return
	}
	defer s.Close()
	ctx := context.Background()
	for {
		cut, err := memory.Interrupt(ctx, s, chatID)
		if err != nil {
			r.logf("turns: the memory pass: reading the passes of a chat of tenant %s: %v", tenantID, err)
		}
		for _, id := range cut {
			r.logf("turns: turn %s: its memory pass was running when Keystone stopped, and is not run again because it may already have added its source", id)
		}
		for !r.isClosing() {
			p, ok, err := memory.Claim(ctx, s, chatID)
			if err != nil {
				r.logf("turns: the memory pass: reading the queue of a chat of tenant %s: %v", tenantID, err)
			}
			if !ok {
				break
			}
			r.memoryPass(s, p)
		}
		r.memory.release(key)
		if r.isClosing() {
			return
		}
		if waiting, err := memory.Waiting(ctx, s, chatID); err != nil || !waiting || !r.memory.claim(key) {
			return
		}
	}
}

// memoryPass makes one pass and ends its row with how it went. It never
// stops anything else: whatever breaks is one line in the log and the
// pass's reason. A pass the runner's closing cuts short is left running,
// and the chat's next worker marks it interrupted.
func (r *Runner) memoryPass(s *tenant.Store, p memory.Pass) {
	bg := context.Background()
	end := func(st memory.Status, reason string) {
		p.Status, p.Reason = st, reason
		if err := memory.Finish(bg, s, p); err != nil {
			r.logf("turns: turn %s: the memory pass: %v", p.TurnID, err)
		}
	}
	t, err := getTurn(bg, s, p.ChatID, p.TurnID)
	if err != nil {
		r.logf("turns: turn %s: the memory pass: reading the turn: %v", p.TurnID, err)
		end(memory.StatusFailed, "The turn could not be read. The reason is in the server's log.")
		return
	}
	broke := func(reason string, err error) {
		r.logf("turns: %s turn %s: the memory pass: %s %v", t.requestID, t.ID, reason, err)
		end(memory.StatusFailed, reason+" The reason is in the server's log.")
	}
	o, timeout, err := r.memoryOptions(bg, s)
	if err != nil {
		broke("The memory pass's settings do not read.", err)
		return
	}
	ctx, cancel := context.WithTimeout(audit.WithRequestID(r.base, t.requestID), timeout)
	defer cancel()

	if r.setting(ctx, s, memory.SettingOn) != "true" {
		end(memory.StatusSkipped, memory.ReasonOff)
		return
	}
	if incognito, err := memory.Incognito(ctx, s, t.ChatID); err != nil || incognito {
		if err != nil {
			broke("Whether the chat is incognito could not be read.", err)
			return
		}
		end(memory.StatusSkipped, memory.ReasonIncognito)
		return
	}
	if t.Status != StatusDone || t.ReplyMessageID == 0 {
		end(memory.StatusSkipped, "The turn did not end with a whole reply.")
		return
	}
	name := r.setting(ctx, s, knowledge.SettingAdapter)
	if name == "" || name == knowledge.None {
		end(memory.StatusSkipped, memory.ReasonNoStore)
		return
	}
	ks, err := knowledge.Open(ctx, name, s)
	if err != nil {
		broke("The knowledge store did not open.", err)
		return
	}
	defer ks.Close()
	if !ks.Capabilities().AddSource {
		end(memory.StatusSkipped, memory.ReasonCannotAdd)
		return
	}

	// Fixed rule 3, before any model is asked: the facts of a chat that was
	// sent sensitive content are sensitive, and land in a space the store
	// says is sensitive or nowhere. The route of such a chat holds only
	// models that may read it, as every route of the chat does.
	held := r.sensitive(ctx, s, t, false)
	space, err := memory.Target(ctx, ks, held, o)
	if errors.Is(err, memory.ErrNowhere) {
		end(memory.StatusSkipped, nowhere(o))
		return
	}
	if err != nil {
		broke("The knowledge store's catalog could not be read.", err)
		return
	}
	x, err := r.exchange(s, t)
	if err != nil {
		broke("The chat could not be read.", err)
		return
	}
	if x.Empty() {
		end(memory.StatusSkipped, memory.ReasonNothing)
		return
	}
	m, provider, secrets, failure := r.memoryRoute(ctx, s, t)
	if failure != nil {
		end(memory.StatusSkipped, "No model could take the memory pass: "+failure.Message)
		return
	}
	out := r.call(ctx, s, t, stepMemory, m, provider, secrets, memory.Request(o, x), func(string, any) {})
	if out.closing {
		return
	}
	p.Model = out.model.Name
	if out.err != nil {
		switch {
		case out.failure != nil && out.failure.Code == string(seam.ErrSpendCap):
			end(memory.StatusSkipped, out.failure.Message)
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			r.logf("turns: %s turn %s: the memory pass ran out of time after %s", t.requestID, t.ID, timeout)
			end(memory.StatusFailed, "The memory pass ran out of time.")
		default:
			why := "The model call failed."
			if out.failure != nil {
				why = "The model call failed: " + out.failure.Message
			}
			r.logf("turns: %s turn %s: the memory pass: %s", t.requestID, t.ID, why)
			end(memory.StatusFailed, why)
		}
		return
	}
	read, err := memory.Facts(out.resp, o)
	if err != nil {
		broke("The model's answer was not the facts that were asked for.", err)
		return
	}
	if len(read.Facts) == 0 {
		end(memory.StatusDone, "")
		return
	}

	// Fixed rule 3 again, after the call: if the chat was sent sensitive
	// content while the model read, its facts are taken to be sensitive too,
	// and Add refuses them from a model that may not read such content.
	sensitive := held || r.sensitive(bg, s, t, false)
	if sensitive && !held {
		if space, err = memory.Target(ctx, ks, true, o); err != nil {
			end(memory.StatusSkipped, nowhere(o))
			return
		}
	}
	// A chat deleted while its pass ran leaves nothing behind it.
	if _, err := chat.Get(s, t.ChatID); err != nil {
		return
	}
	src, err := memory.Add(ctx, ks, o, x, read.Facts, space, sensitive, out.model.MayReadSensitive())
	switch {
	case errors.Is(err, memory.ErrSensitive):
		r.logf("turns: %s turn %s: the memory pass: the chat was sent sensitive content while %s read it, and that model may not read it, so its facts are kept nowhere", t.requestID, t.ID, out.model.Name)
		end(memory.StatusSkipped, "The chat was sent sensitive content while a model that may not read it read the exchange, so its facts were kept nowhere.")
		return
	case errors.Is(err, memory.ErrLanded):
		r.logf("turns: %s turn %s: the memory pass: %v", t.requestID, t.ID, err)
		p.Reason = "The store put these facts in a space it does not say is sensitive."
	case err != nil:
		broke("The knowledge store did not take the source.", err)
		return
	}
	p.Space, p.SourceID, p.Sensitive, p.Facts = src.Space, src.ID, src.Label.Sensitive(), read.Texts()
	end(memory.StatusDone, p.Reason)
}

// nowhere is the reason a pass gives when the facts of a chat that was
// sent sensitive content may land nowhere.
func nowhere(o memory.Options) string {
	if o.SensitiveSpace == "" {
		return "The chat was sent sensitive content, and no sensitive space is set for its facts (" + memory.SettingSensitiveSpace + "), so the exchange was not read."
	}
	return "The chat was sent sensitive content, and the knowledge store does not say the space " + strconv.Quote(o.SensitiveSpace) + " is sensitive, so the exchange's facts were kept nowhere."
}

// exchange reads one turn as the memory pass reads it: the chat's title,
// the person's message and the reply, as text.
func (r *Runner) exchange(s *tenant.Store, t row) (memory.Exchange, error) {
	x := memory.Exchange{ChatID: t.ChatID, TurnID: t.ID, At: t.Created}
	c, err := chat.Get(s, t.ChatID)
	if err != nil {
		return x, err
	}
	x.Title = c.Title
	text := func(id int64) (string, error) {
		if id == 0 {
			return "", nil
		}
		msg, err := chat.GetMessage(s, t.ChatID, id)
		if err != nil {
			return "", err
		}
		return seam.Text(seam.Message{Role: msg.Role, Blocks: msg.Blocks}), nil
	}
	if x.Person, err = text(t.UserMessageID); err != nil {
		return x, err
	}
	x.Reply, err = text(t.ReplyMessageID)
	return x, err
}

// memoryRoute is the route a memory pass goes to: the model the memory
// rule names, then the chat's own, then the fallback chain, each behind
// the budget's guard, with the memory pass's reserve and the monthly cap
// asked before each. The test tenant has no route and gets its recorded
// turns.
func (r *Runner) memoryRoute(ctx context.Context, s *tenant.Store, t row) (models.Model, seam.Provider, []string, *TurnError) {
	test, err := testtenant.IsTest(s)
	if err != nil {
		return models.Model{}, nil, nil, &TurnError{Code: CodeFailed, Message: "the test tenant's mark could not be read: " + err.Error()}
	}
	if test {
		tm := r.testModel()
		return tm, budget.Guard(tm, r.test, r.o.Config.Budget), nil, nil
	}
	return r.routed(ctx, s, t, routing.KindMemory, t.Model)
}

// gate is the gate a route of one kind asks before each of its models:
// the monthly cap, and for the memory pass its own reserve first.
func (r *Runner) gate(s *tenant.Store, kind routing.Kind) func(context.Context, models.Model, seam.Request) *seam.Error {
	capped := r.capGate(s)
	if kind != routing.KindMemory {
		return capped
	}
	return func(ctx context.Context, m models.Model, req seam.Request) *seam.Error {
		if refused := r.reserveGate(ctx, s, m, req); refused != nil {
			return refused
		}
		return capped(ctx, m, req)
	}
}

// reserveGate is the memory pass's reserve (decision 0280): a call to a
// model that is not local is refused when this month's memory passes and
// the most the call could cost are together more than
// knowledge.memory_reserve_usd. A local model is never asked about. A
// reserve that cannot be read stops the call. A refused call fails with
// the kind spend_cap, so the route moves on to a model that costs nothing.
func (r *Runner) reserveGate(ctx context.Context, s *tenant.Store, m models.Model, req seam.Request) *seam.Error {
	if !usage.Counts(m.Class) {
		return nil
	}
	unread := func(what string, err error) *seam.Error {
		r.logf("turns: tenant %s: %s: %v", s.TenantID(), what, err)
		return &seam.Error{Kind: seam.ErrSpendCap, Message: "The memory pass's reserve could not be read, so the call was not sent. The reason is in the server's log."}
	}
	usd, err := strconv.ParseFloat(r.setting(ctx, s, memory.SettingReserve), 64)
	if err != nil {
		return unread("reading "+memory.SettingReserve, err)
	}
	reserve, err := memory.NewReserve(usd)
	if err != nil {
		return unread("reading "+memory.SettingReserve, err)
	}
	st, err := memory.Allow(context.Background(), s, reserve, time.Now(), mostCost(m, req, r.o.Config.Budget))
	if err != nil {
		return unread("summing the month's memory passes", err)
	}
	if st.State == usage.StateStopped {
		return &seam.Error{Kind: seam.ErrSpendCap, Message: fmt.Sprintf(
			"The memory pass was not sent to %s. This month's memory passes on models that are not local have cost %s, the memory pass's reserve is %s, and this call could cost up to %s.",
			m.Name, usage.Dollars(st.SpentMicros), usage.Dollars(st.LimitMicros), usage.Dollars(st.NextMicros))}
	}
	return nil
}

// memoryOptions reads the memory pass's settings for a tenant, and its
// timeout.
func (r *Runner) memoryOptions(ctx context.Context, s *tenant.Store) (memory.Options, time.Duration, error) {
	var bad []string
	whole := func(key string) int {
		n, err := strconv.Atoi(r.setting(ctx, s, key))
		if err != nil {
			bad = append(bad, key)
		}
		return n
	}
	o := memory.Options{
		Space:           r.setting(ctx, s, memory.SettingSpace),
		SensitiveSpace:  r.setting(ctx, s, memory.SettingSensitiveSpace),
		Prompt:          r.setting(ctx, s, memory.SettingPrompt),
		SourceName:      r.setting(ctx, s, memory.SettingSourceName),
		MaxFacts:        whole(memory.SettingMaxFacts),
		FactMaxChars:    whole(memory.SettingFactMaxChars),
		MessageMaxChars: whole(memory.SettingMessageMaxChars),
		MaxOutputTokens: whole(memory.SettingMaxOutputTokens),
	}
	timeout, err := time.ParseDuration(r.setting(ctx, s, memory.SettingTimeout))
	if err != nil || timeout < memoryTimeoutMin || timeout > memoryTimeoutMax {
		bad = append(bad, memory.SettingTimeout)
	}
	if len(bad) > 0 {
		return memory.Options{}, 0, errors.New("these settings do not read: " + strings.Join(bad, ", "))
	}
	return o, timeout, memory.CheckOptions(o)
}

// MemoryReply answers the memory routes: a chat's incognito flag and its
// memory passes.
type MemoryReply struct {
	ChatID string `json:"chat_id"`
	// Incognito says the chat's turns run no memory pass.
	Incognito bool `json:"incognito"`
	// Passes are the chat's memory passes, the oldest first: one for each
	// turn whose pass was queued. An incognito turn has none.
	Passes []memory.Pass `json:"passes"`
}

// MemoryRoutes returns the memory pass's routes (P6-06), served by r: a
// chat's passes and its incognito flag.
func MemoryRoutes(r *Runner) []api.Route {
	const one = api.Prefix + "chats/{id}"
	return []api.Route{
		{
			Method: "GET", Path: one + "/memory", Handle: r.memoryRead,
			Summary: "The chat's memory: whether it is incognito, and each memory pass its turns queued, with the facts a pass added to the knowledge store and where.",
			Reply:   MemoryReply{},
		},
		{
			Method: "PUT", Path: one + "/incognito", Handle: r.incognitoOn,
			Summary: "Make the chat incognito: its turns from now on run no memory pass, and a pass still waiting is skipped. It takes no body.",
			Reply:   MemoryReply{},
		},
		{
			Method: "DELETE", Path: one + "/incognito", Handle: r.incognitoOff,
			Summary: "Make the chat ordinary again: its next turns run the memory pass. Turns while it was incognito stay unread.",
			Reply:   MemoryReply{},
		},
	}
}

func (r *Runner) memoryRead(w http.ResponseWriter, req *http.Request, c api.Call) {
	r.memoryReply(w, req, c, req.PathValue("id"))
}

func (r *Runner) incognitoOn(w http.ResponseWriter, req *http.Request, c api.Call) {
	r.setIncognito(w, req, c, true)
}

func (r *Runner) incognitoOff(w http.ResponseWriter, req *http.Request, c api.Call) {
	r.setIncognito(w, req, c, false)
}

func (r *Runner) setIncognito(w http.ResponseWriter, req *http.Request, c api.Call, on bool) {
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	if err := memory.SetIncognito(req.Context(), c.Store, id, on); err != nil {
		r.answer(w, req, c, err)
		return
	}
	r.memoryReply(w, req, c, id)
}

func (r *Runner) memoryReply(w http.ResponseWriter, req *http.Request, c api.Call, chatID string) {
	if _, err := chat.Get(c.Store, chatID); err != nil {
		r.answer(w, req, c, err)
		return
	}
	incognito, err := memory.Incognito(req.Context(), c.Store, chatID)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	passes, err := memory.Passes(req.Context(), c.Store, chatID)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, MemoryReply{ChatID: chatID, Incognito: incognito, Passes: passes})
}
