package turn

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/recap"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// This file gives each chat its recap (P5-05): a short summary, its topics
// and the decisions made in it, which the past-chat search finds chats by
// and gives to the model (pastchats.go). When a turn ends with a whole
// reply and the chat has changed enough since its recap, a job beside the
// turn waits until the chat has rested, then asks the model routing gives
// the summary task for a new recap, under the turn's request id. The turn
// is never held for it. internal/recap writes the request, reads the
// answer, keeps the recap and holds fixed rule 3's answer for past chats;
// it reads no setting, so the settings are read here.
// docs/architecture/chat-summaries.md is the whole of it.

// The settings recaps read, in settings/chats.json.
const (
	KeyRecap           = "chats.recap"
	KeyRecapAfterTurns = "chats.recap_after_turns"
	KeyRecapAfterIdle  = "chats.recap_after_idle"
	KeyRecapTimeout    = "chats.recap_timeout"
	KeyRecapRetryAfter = "chats.recap_retry_after"
)

// The bounds of the recap settings that are read here.
const (
	recapAfterTurnsMax = 1000
	recapIdleMax       = 24 * time.Hour
	recapTimeoutMin    = 5 * time.Second
	recapTimeoutMax    = 30 * time.Minute
)

// errRecapBusy says a recap of the chat is being made already.
var errRecapBusy = errors.New("a recap of this chat is being made")

// recapJobs is which chats wait to be given a recap in this process, which
// are being given one, and when a chat's recap last failed. Its zero value
// is ready to use.
type recapJobs struct {
	mu sync.Mutex
	// due is when a chat will have rested long enough. A chat is in it
	// while one goroutine waits for it.
	due map[string]time.Time
	// making is the chats whose recap is being made.
	making map[string]bool
	failed map[string]time.Time
}

func (j *recapJobs) ready() {
	if j.due == nil {
		j.due, j.making, j.failed = map[string]time.Time{}, map[string]bool{}, map[string]time.Time{}
	}
}

// start claims a chat for making its recap. It is refused while one is
// being made.
func (j *recapJobs) start(key string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.ready()
	if j.making[key] {
		return false
	}
	j.making[key] = true
	return true
}

// done gives a chat back, and notes whether its recap failed.
func (j *recapJobs) done(key string, failed bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.making, key)
	if failed {
		j.failed[key] = time.Now()
	} else {
		delete(j.failed, key)
	}
}

// recapPlan is when a chat is given a new recap: after how many more
// turns, once it has rested how long, and how long after a failure.
type recapPlan struct {
	afterTurns int
	idle       time.Duration
	retry      time.Duration
}

// recapPlanOf reads the plan's settings for a tenant.
func (r *Runner) recapPlanOf(ctx context.Context, s *tenant.Store) (recapPlan, error) {
	var p recapPlan
	var bad []string
	var err error
	if p.afterTurns, err = strconv.Atoi(r.setting(ctx, s, KeyRecapAfterTurns)); err != nil || p.afterTurns < 1 || p.afterTurns > recapAfterTurnsMax {
		bad = append(bad, KeyRecapAfterTurns)
	}
	if p.idle, err = time.ParseDuration(r.setting(ctx, s, KeyRecapAfterIdle)); err != nil || p.idle < 0 || p.idle > recapIdleMax {
		bad = append(bad, KeyRecapAfterIdle)
	}
	if p.retry, err = time.ParseDuration(r.setting(ctx, s, KeyRecapRetryAfter)); err != nil || p.retry <= 0 {
		bad = append(bad, KeyRecapRetryAfter)
	}
	if len(bad) > 0 {
		return recapPlan{}, errors.New("these settings do not read: " + strings.Join(bad, ", "))
	}
	return p, nil
}

// recapOptions reads what a recap is made with, and how long making one
// may take.
func (r *Runner) recapOptions(ctx context.Context, s *tenant.Store) (recap.Options, time.Duration, error) {
	var bad []string
	whole := func(key string) int {
		n, err := strconv.Atoi(r.setting(ctx, s, key))
		if err != nil {
			bad = append(bad, key)
		}
		return n
	}
	o := recap.Options{
		Prompt:          r.setting(ctx, s, recap.SettingPrompt),
		SummaryMaxWords: whole(recap.SettingSummaryMaxWords),
		MaxTopics:       whole(recap.SettingMaxTopics),
		MaxDecisions:    whole(recap.SettingMaxDecisions),
		ItemMaxChars:    whole(recap.SettingItemMaxChars),
		MessageMaxChars: whole(recap.SettingMessageMaxChars),
		InputMaxChars:   whole(recap.SettingInputMaxChars),
		MaxOutputTokens: whole(recap.SettingMaxOutputTokens),
	}
	timeout, err := time.ParseDuration(r.setting(ctx, s, KeyRecapTimeout))
	if err != nil || timeout < recapTimeoutMin || timeout > recapTimeoutMax {
		bad = append(bad, KeyRecapTimeout)
	}
	if len(bad) > 0 {
		return recap.Options{}, 0, errors.New("these settings do not read: " + strings.Join(bad, ", "))
	}
	return o, timeout, recap.CheckOptions(o)
}

// doneTurns is how many of a chat's turns have ended with a whole reply.
func doneTurns(ctx context.Context, s *tenant.Store, chatID string) (int, error) {
	var n int
	err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM turns WHERE chat_id = ? AND status = ?", chatID, StatusDone).Scan(&n)
	return n, err
}

// recapDue says whether a chat is due a new recap: it has none and a turn
// has ended with a whole reply, or it has one and afterTurns more have.
func recapDue(ctx context.Context, s *tenant.Store, chatID string, afterTurns int) (bool, error) {
	have, ok, err := recap.Get(ctx, s, chatID)
	if err != nil {
		return false, err
	}
	done, err := doneTurns(ctx, s, chatID)
	if err != nil {
		return false, err
	}
	return recap.Due(have, ok, done, afterTurns), nil
}

// recapLater starts the wait for a chat's new recap when a turn has ended
// with a whole reply and the chat is due one. It returns at once and never
// changes how the turn ended. A chat that already waits waits longer, from
// this turn's end, so a recap is made once the person has stopped writing.
func (r *Runner) recapLater(s *tenant.Store, t row, status string) {
	if status != StatusDone || r.setup() != nil {
		return
	}
	ctx := context.Background()
	if r.setting(ctx, s, KeyRecap) != "true" {
		return
	}
	plan, err := r.recapPlanOf(ctx, s)
	if err != nil {
		r.logf("turns: %s turn %s: the chat is not given a recap: %v", t.requestID, t.ID, err)
		return
	}
	due, err := recapDue(ctx, s, t.ChatID, plan.afterTurns)
	if err != nil {
		r.logf("turns: %s turn %s: reading whether the chat is due a recap, so none is made: %v", t.requestID, t.ID, err)
		return
	}
	if !due {
		return
	}
	key := s.TenantID() + "/" + t.ChatID
	at := time.Now().Add(plan.idle)
	r.recaps.mu.Lock()
	r.recaps.ready()
	if f := r.recaps.failed[key]; !f.IsZero() && time.Since(f) < plan.retry {
		r.recaps.mu.Unlock()
		return
	}
	if _, waiting := r.recaps.due[key]; waiting {
		r.recaps.due[key] = at
		r.recaps.mu.Unlock()
		return
	}
	r.recaps.due[key] = at
	r.recaps.mu.Unlock()

	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		r.recaps.mu.Lock()
		delete(r.recaps.due, key)
		r.recaps.mu.Unlock()
		return
	}
	r.wg.Add(1)
	r.mu.Unlock()
	tenantID := s.TenantID()
	go func() {
		defer r.wg.Done()
		r.recapWhenRested(tenantID, t, key)
	}()
}

// recapWhenRested waits until the chat has rested since its last turn,
// which a later turn's end moves on, and then makes its recap, unless one
// is being made already. A runner that closes ends the wait.
func (r *Runner) recapWhenRested(tenantID string, t row, key string) {
	for {
		r.recaps.mu.Lock()
		wait := time.Until(r.recaps.due[key])
		if wait <= 0 {
			delete(r.recaps.due, key)
			if r.recaps.making[key] {
				r.recaps.mu.Unlock()
				return // being made as asked through the route
			}
			r.recaps.making[key] = true
		}
		r.recaps.mu.Unlock()
		if wait <= 0 {
			break
		}
		timer := time.NewTimer(wait)
		select {
		case <-r.base.Done():
			timer.Stop()
			r.recaps.mu.Lock()
			delete(r.recaps.due, key)
			r.recaps.mu.Unlock()
			return
		case <-timer.C:
		}
	}
	r.recaps.done(key, r.recapAfterTurn(tenantID, t))
}

// recapAfterTurn makes a chat's recap once it has rested, with a store of
// its own. It reports whether it failed, which it says in one line of the
// log. A chat that is no longer due, has a turn waiting or running, or is
// gone is left alone: the next turn's end looks again.
func (r *Runner) recapAfterTurn(tenantID string, t row) (failed bool) {
	s, err := r.o.Install.Store(tenantID)
	if err != nil {
		r.logf("turns: %s turn %s: the chat's recap: opening the store: %v", t.requestID, t.ID, err)
		return true
	}
	defer s.Close()
	ctx := context.Background()
	if r.setting(ctx, s, KeyRecap) != "true" {
		return false
	}
	plan, err := r.recapPlanOf(ctx, s)
	if err != nil {
		r.logf("turns: %s turn %s: the chat is not given a recap: %v", t.requestID, t.ID, err)
		return true
	}
	if due, err := recapDue(ctx, s, t.ChatID, plan.afterTurns); err != nil || !due {
		return err != nil
	}
	if open, err := openTurns(ctx, s, t.ChatID); err != nil || len(open) > 0 {
		return err != nil
	}
	_, timeout, err := r.recapOptions(ctx, s)
	if err != nil {
		r.logf("turns: %s turn %s: the chat is not given a recap: %v", t.requestID, t.ID, err)
		return true
	}
	ctx, cancel := context.WithTimeout(audit.WithRequestID(r.base, t.requestID), timeout)
	defer cancel()
	job := row{Turn: Turn{ID: t.ID, ChatID: t.ChatID, Model: t.Model}, requestID: t.requestID, actor: t.actor}
	if _, err := r.makeRecap(ctx, s, job); err != nil {
		if errors.Is(err, errClosing) || errors.Is(err, chat.ErrNotFound) {
			return false
		}
		r.logf("turns: %s turn %s: chat %s was not given a recap: %v", t.requestID, t.ID, t.ChatID, err)
		return true
	}
	return false
}

// errClosing says the runner closed while a recap was being made, and
// nothing was kept.
var errClosing = errors.New("the server is stopping")

// recapFailure is why a recap could not be made, as a code and a sentence
// the route answers with.
type recapFailure struct {
	status int
	code   string
	why    string
}

func (f *recapFailure) Error() string { return f.why }

// makeRecap makes a chat's recap and keeps it. t names the chat, the chat's
// own model (Turn.Model), and the request id and actor its model call is
// recorded under; ctx carries the request id and the time it may take.
//
// The call goes to the summary route: routing.summary_model, then the
// chat's own model, then the fallback chain, each behind the budget's
// guard and the monthly cap. For a chat that was sent sensitive content
// the route keeps only models that may read it (fixed rule 3), and the
// recap is kept as sensitive when the chat was held before the call or
// after it. The call is made with the step stepSummary, so its usage
// record is of the kind summary.
func (r *Runner) makeRecap(ctx context.Context, s *tenant.Store, t row) (recap.Recap, error) {
	o, _, err := r.recapOptions(ctx, s)
	if err != nil {
		return recap.Recap{}, err
	}
	c, err := chat.Get(s, t.ChatID)
	if err != nil {
		return recap.Recap{}, err
	}
	path, err := chat.Path(s, t.ChatID)
	if err != nil {
		return recap.Recap{}, err
	}
	if len(path) == 0 {
		return recap.Recap{}, recap.ErrNothing
	}
	// The recap so far stands for the messages before the newest it was
	// made from, when that message is still on the chat's current branch.
	have, ok, err := recap.Get(ctx, s, t.ChatID)
	if err != nil {
		return recap.Recap{}, err
	}
	var prev *recap.Recap
	from := 0
	if ok {
		for i, m := range path {
			if m.ID == have.ThroughID {
				prev, from = &have, i+1
			}
		}
	}
	done, err := doneTurns(ctx, s, t.ChatID)
	if err != nil {
		return recap.Recap{}, err
	}
	if prev != nil && from == len(path) {
		return have, nil // nothing was said since
	}
	messages := make([]seam.Message, 0, len(path)-from)
	for _, m := range path[from:] {
		messages = append(messages, seam.Message{Role: m.Role, Blocks: m.Blocks})
	}

	held := r.sensitive(ctx, s, t, false)
	sm, provider, secrets, failure := r.summaryRoute(ctx, s, t, models.Model{Name: t.Model})
	if failure != nil {
		return recap.Recap{}, &recapFailure{status: http.StatusConflict, code: failure.Code, why: failure.Message}
	}
	fits := func(req seam.Request) bool {
		return budget.Estimated(req, r.o.Config.Budget).Tokens <= budget.InputRoom(sm, req.MaxOutputTokens)
	}
	req, err := recap.Request(recap.Ask{Options: o, Model: sm.Name, Title: c.Title, Previous: prev, Messages: messages, Fits: fits})
	if err != nil {
		return recap.Recap{}, err
	}
	out := r.call(ctx, s, t, stepSummary, sm, provider, secrets, req, func(string, any) {})
	switch {
	case out.closing:
		return recap.Recap{}, errClosing
	case out.stopped && ctx.Err() != nil:
		return recap.Recap{}, ctx.Err() // out of time, or the caller went away
	case out.err != nil:
		why := "the model call failed"
		if out.failure != nil {
			why += ": " + out.failure.Message
		}
		return recap.Recap{}, &recapFailure{status: http.StatusBadGateway, code: "recap_failed", why: why}
	}
	read, err := recap.Answer(out.resp, o)
	if err != nil {
		return recap.Recap{}, &recapFailure{status: http.StatusBadGateway, code: "recap_failed", why: "the model's answer was not the recap asked for: " + err.Error()}
	}
	// A chat deleted while its recap was made leaves nothing behind it.
	if _, err := chat.Get(s, t.ChatID); err != nil {
		return recap.Recap{}, err
	}
	return recap.Save(context.Background(), s, recap.Recap{
		ChatID: t.ChatID, Summary: read.Summary, Topics: read.Topics, Decisions: read.Decisions,
		ThroughID: path[len(path)-1].ID, Turns: done, Model: out.model.Name,
		Sensitive: held || r.sensitive(context.Background(), s, t, false), RequestID: t.requestID,
	})
}

// withSummaries gives each chat of a list the summary of its recap. A
// summary that cannot be read is left out and logged: the list is not
// failed for it.
func (r *Runner) withSummaries(ctx context.Context, s *tenant.Store, chats []Chat) {
	ids := make([]string, len(chats))
	for i, c := range chats {
		ids[i] = c.ID
	}
	sums, err := recap.Summaries(ctx, s, ids)
	if err != nil {
		r.logf("turns: reading the chats' summaries of tenant %s, so the list has none: %v", s.TenantID(), err)
		return
	}
	for i := range chats {
		chats[i].Summary = sums[chats[i].ID]
	}
}

// RecapReply answers the recap routes.
type RecapReply struct {
	ChatID string `json:"chat_id"`
	// Made says the chat has a recap. Until it has one, Recap is empty.
	Made bool `json:"made"`
	// Recap is the chat's summary, topics and decisions, the model that
	// wrote it, and how many turns had ended when it was made. Sensitive
	// says the chat had been sent content from a sensitive space: the recap
	// then reaches only models that may read such content.
	Recap recap.Recap `json:"recap"`
}

// RecapRoutes returns the routes of chat recaps (P5-05), served by r.
func RecapRoutes(r *Runner) []api.Route {
	const one = api.Prefix + "chats/{id}"
	return []api.Route{
		{
			Method: "GET", Path: one + "/recap", Handle: r.recapRead,
			Summary: "The chat's recap: a short summary, its topics and the decisions made in it, which the past-chat search finds it by. A chat is given one after its turns.",
			Reply:   RecapReply{},
		},
		{
			Method: "POST", Path: one + "/recap", Handle: r.recapMake,
			Summary: "Make the chat's recap now, with the model routing gives the summary task, and answer with it. It takes no body, and waits for the model, up to chats.recap_timeout.",
			Reply:   RecapReply{},
		},
	}
}

func (r *Runner) recapRead(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	have, ok, err := recap.Get(req.Context(), c.Store, id)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, recapReply(id, have, ok))
}

func (r *Runner) recapMake(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	if err := r.setup(); err != nil {
		r.answer(w, req, c, err)
		return
	}
	key := c.Store.TenantID() + "/" + id
	if !r.recaps.start(key) {
		api.Error(w, req, http.StatusConflict, "busy", errRecapBusy.Error()+"; ask again in a moment")
		return
	}
	choice, err := r.choice(req.Context(), c.Store, id)
	if err != nil {
		r.recaps.done(key, false)
		r.answer(w, req, c, err)
		return
	}
	model := ""
	if choice.Model != nil {
		model = choice.Model.Name
	}
	_, timeout, err := r.recapOptions(req.Context(), c.Store)
	if err != nil {
		r.recaps.done(key, false)
		r.answer(w, req, c, err)
		return
	}
	ctx, cancel := context.WithTimeout(audit.WithRequestID(req.Context(), c.RequestID), timeout)
	defer cancel()
	made, err := r.makeRecap(ctx, c.Store, row{Turn: Turn{ChatID: id, Model: model}, requestID: c.RequestID, actor: api.ActorPerson})
	r.recaps.done(key, err != nil)
	var failure *recapFailure
	switch {
	case errors.As(err, &failure):
		api.Error(w, req, failure.status, failure.code, failure.why)
	case errors.Is(err, recap.ErrNothing):
		refused(w, req, "the chat has no messages to make a recap from")
	case errors.Is(err, recap.ErrNoRoom):
		api.Error(w, req, http.StatusBadGateway, "recap_failed", "the chat does not fit the summary model's window")
	case errors.Is(err, context.DeadlineExceeded):
		api.Error(w, req, http.StatusGatewayTimeout, "recap_failed", "making the recap took longer than chats.recap_timeout")
	case err != nil:
		r.answer(w, req, c, err)
	default:
		api.WriteJSON(w, http.StatusOK, recapReply(id, made, true))
	}
}

func recapReply(chatID string, have recap.Recap, ok bool) RecapReply {
	if !ok {
		have = recap.Recap{ChatID: chatID, Topics: []string{}, Decisions: []string{}}
	}
	return RecapReply{ChatID: chatID, Made: ok, Recap: have}
}
