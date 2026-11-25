package turn

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/compact"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
)

// This file joins compaction (P5-02) to a turn. Before each model call the
// chat's summary, when one stands for the start of the branch being sent,
// and the messages the person pinned are given to the context builder.
// After the call is built, when its record says the chat has outgrown the
// room for its earlier turns, the oldest are summarised beside the turn,
// in the background, by the model routing gives the summary task. The call
// is never held for it. internal/compact does the deciding and keeps the
// summaries; it reads no setting, so the settings are read here.

// The settings compaction reads, in settings/chats.json.
const (
	KeyCompact              = "chats.compact"
	KeyCompactTrigger       = "chats.compact_trigger_percent"
	KeyCompactKeep          = "chats.compact_keep_percent"
	KeyCompactMaxTokens     = "chats.compact_summary_max_tokens"
	KeyCompactHeading       = "chats.compact_summary_heading"
	KeyCompactPrompt        = "chats.compact_prompt"
	KeyCompactMessageChars  = "chats.compact_message_max_chars"
	KeyCompactCallsMax      = "chats.compact_calls_max"
	KeyCompactTimeout       = "chats.compact_timeout"
	KeyCompactRetryAfter    = "chats.compact_retry_after"
	KeyContextPinnedHeading = "chats.context_pinned_heading"
)

// stepSummary is the step a summary call is made with. Runner.used
// records it as a call of the summary kind.
const stepSummary = -1

// compactions is which chats are being summarised, and when a summary of
// a chat last failed. Its zero value is ready to use.
type compactions struct {
	mu      sync.Mutex
	running map[string]bool
	failed  map[string]time.Time
}

// start claims a chat for a summary job. It is refused while a job runs
// for the chat, and for retry after a job for it failed.
func (c *compactions) start(key string, retry time.Duration, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running == nil {
		c.running, c.failed = map[string]bool{}, map[string]time.Time{}
	}
	if c.running[key] || (!c.failed[key].IsZero() && now.Sub(c.failed[key]) < retry) {
		return false
	}
	c.running[key] = true
	return true
}

// done gives a chat back, and notes when its job failed.
func (c *compactions) done(key string, failed bool, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.running, key)
	if failed {
		c.failed[key] = now
	} else {
		delete(c.failed, key)
	}
}

// busy says whether a summary job runs for a chat.
func (c *compactions) busy(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running[key]
}

// history is what one call is given in place of the oldest turns.
type history struct {
	// summary is the stored summary that stands for the start of the
	// branch, and its zero value when none does.
	summary compact.Summary
	// text is the heading and the summary, as sent.
	text string
	// covers is how many of the call's messages the summary stands for.
	covers int
	// pins are the pinned messages of the branch before the current turn.
	pins []contextbuild.Pin
}

// olderHistory finds what one call is given in place of its oldest turns:
// the newest summary that stands for the start of the branch being sent
// and may go to every model of the call's route, and the messages the
// person pinned on that branch. Nothing here stops a call: a record that
// does not read gives nothing and one line in the log.
func (r *Runner) olderHistory(ctx context.Context, s *tenant.Store, t row, m models.Model, messages []seam.Message, mayReadSensitive bool) history {
	var h history
	broke := func(what string, err error) history {
		r.logf("turns: %s turn %s: %s, so no summary and no pinned message is sent: %v", t.requestID, t.ID, what, err)
		return history{}
	}
	pinned, err := chat.Pins(s, t.ChatID)
	if err != nil {
		return broke("reading the pinned messages", err)
	}
	var sums []compact.Summary
	if r.setting(ctx, s, KeyCompact) == "true" {
		if sums, err = compact.List(ctx, s, t.ChatID); err != nil {
			return broke("reading the chat's summaries", err)
		}
	}
	if len(pinned) == 0 && len(sums) == 0 {
		return h
	}
	path, err := earlier(s, t.ChatID, t.UserMessageID)
	if err != nil {
		return broke("reading the chat", err)
	}
	metas, err := metaOf(ctx, s, t.ChatID)
	if err != nil {
		return broke("reading the chat", err)
	}
	at := map[int64]int{}
	for i, msg := range path {
		at[msg.ID] = i
	}
	prefix := func(n int) []seam.Message { return buildMessages(path[:n], metas, m.Name) }

	starts := map[int]bool{}
	for _, span := range contextbuild.Turns(messages) {
		starts[span[0]] = true
	}
	for _, sum := range sums {
		p, ok := at[sum.ThroughID]
		if !ok || !compact.Usable(sum, mayReadSensitive) {
			continue
		}
		// It stands for the start of this branch only when the messages it
		// was made from are, as this call sends them, the call's first.
		head := prefix(p + 1)
		if len(head) == 0 || !starts[len(head)] || len(head) >= len(messages) || !reflect.DeepEqual(head, messages[:len(head)]) {
			continue
		}
		h.summary, h.covers = sum, len(head)
		h.text = withRule(r.setting(ctx, s, KeyCompactHeading), sum.Text)
		break
	}

	heading := r.setting(ctx, s, KeyContextPinnedHeading)
	for _, id := range pinned {
		p, ok := at[id]
		if !ok {
			continue // on another branch, or in the current turn
		}
		var words []string
		for _, b := range path[p].Blocks {
			if b.Type == seam.BlockText && strings.TrimSpace(b.Text) != "" {
				words = append(words, b.Text)
			}
		}
		if len(words) == 0 {
			continue
		}
		who := "The person wrote:"
		if path[p].Role == seam.RoleAssistant {
			who = "The assistant wrote:"
		}
		h.pins = append(h.pins, contextbuild.Pin{
			Message: len(prefix(p+1)) - 1,
			Item:    contextbuild.Item{Label: fmt.Sprintf("pinned message %d", id), Text: withRule(heading, who+"\n"+strings.Join(words, "\n")), Ref: messageRef(id)},
		})
	}
	return h
}

// earlier returns a turn's branch from the chat's first message down to
// the message before the person's message the turn answers.
func earlier(s *tenant.Store, chatID string, userMessageID int64) ([]chat.Message, error) {
	path, err := chat.Path(s, chatID)
	if err != nil {
		return nil, err
	}
	for i, msg := range path {
		if msg.ID == userMessageID {
			return path[:i], nil
		}
	}
	// The person stepped to another branch: it is read upwards.
	asked, err := chat.GetMessage(s, chatID, userMessageID)
	if err != nil {
		return nil, err
	}
	var out []chat.Message
	for id := asked.ParentID; id != 0; {
		msg, err := chat.GetMessage(s, chatID, id)
		if err != nil {
			return nil, err
		}
		out = append([]chat.Message{msg}, out...)
		id = msg.ParentID
	}
	return out, nil
}

// compactOptions reads compaction's settings for a tenant.
func (r *Runner) compactOptions(ctx context.Context, s *tenant.Store) (o compact.Options, timeout, retry time.Duration, err error) {
	var bad []string
	whole := func(key string) int {
		n, err := strconv.Atoi(r.setting(ctx, s, key))
		if err != nil {
			bad = append(bad, key)
		}
		return n
	}
	duration := func(key string) time.Duration {
		d, err := time.ParseDuration(r.setting(ctx, s, key))
		if err != nil || d <= 0 {
			bad = append(bad, key)
		}
		return d
	}
	o = compact.Options{
		TriggerPercent:   whole(KeyCompactTrigger),
		KeepPercent:      whole(KeyCompactKeep),
		SummaryMaxTokens: whole(KeyCompactMaxTokens),
		Prompt:           r.setting(ctx, s, KeyCompactPrompt),
		MessageMaxChars:  whole(KeyCompactMessageChars),
		CallsMax:         whole(KeyCompactCallsMax),
	}
	timeout, retry = duration(KeyCompactTimeout), duration(KeyCompactRetryAfter)
	if len(bad) > 0 {
		return compact.Options{}, 0, 0, errors.New("these settings do not read: " + strings.Join(bad, ", "))
	}
	return o, timeout, retry, compact.CheckOptions(o)
}

// compactLater starts a summary of the oldest turns of the chat, beside
// the turn, when the record of the call just built says the chat has
// outgrown the room for its earlier turns. It returns at once.
func (r *Runner) compactLater(s *tenant.Store, t row, m models.Model, messages []seam.Message, rec contextbuild.Record, h history) {
	ctx := context.Background()
	if len(rec.Turns) < 2 || r.setting(ctx, s, KeyCompact) != "true" {
		return
	}
	o, timeout, retry, err := r.compactOptions(ctx, s)
	if err != nil {
		r.logf("turns: %s turn %s: the oldest turns are not summarised: %v", t.requestID, t.ID, err)
		return
	}
	// The turns the summary stands for count as summarised, whether or not
	// it fitted this call, so a summary that has grown too large for its
	// share is extended, and asked to be shorter, and not made again.
	earlierTurns := rec.Turns[:len(rec.Turns)-1]
	turns := make([]compact.Turn, len(earlierTurns))
	covered := 0
	for i, x := range earlierTurns {
		turns[i] = compact.Turn{Tokens: x.Tokens, Kept: x.Kept, Summarised: h.summary.ID != 0 && x.First+x.Messages <= h.covers}
		if turns[i].Summarised {
			covered++
		}
	}
	span, ok := compact.Plan(turns, rec.TurnRoom, o)
	if !ok && h.summary.ID != 0 && rec.Part(contextbuild.PartSummary).Kept == 0 && covered < len(turns) {
		span, ok = compact.Span{From: covered, To: covered + 1}, true
	}
	if !ok {
		return
	}
	key := s.TenantID() + "/" + t.ChatID
	if !r.compaction.start(key, retry, time.Now()) {
		return
	}
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		r.compaction.done(key, false, time.Now())
		return
	}
	r.wg.Add(1)
	r.mu.Unlock()
	tenantID := s.TenantID()
	go func() {
		defer r.wg.Done()
		failed := r.summarise(tenantID, t, m, messages, rec, h, span, o, timeout)
		r.compaction.done(key, failed, time.Now())
	}()
}

// summarise makes a chat's summary: the summary so far and the turns of
// span go to the summary route, as many calls as it takes up to
// CallsMax, each taking in what the summary model's window holds, and
// each answer is stored as a summary that extends the one before. It
// reports whether it failed, which it says in one line of the log. A job
// the runner's closing cuts short writes nothing more.
func (r *Runner) summarise(tenantID string, t row, m models.Model, messages []seam.Message, rec contextbuild.Record, h history, span compact.Span, o compact.Options, timeout time.Duration) (failed bool) {
	fail := func(why string) bool {
		r.logf("turns: %s turn %s: the oldest turns of chat %s were not summarised, so they are left out with the note: %s", t.requestID, t.ID, t.ChatID, why)
		return true
	}
	s, err := r.o.Install.Store(tenantID)
	if err != nil {
		return fail(err.Error())
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(audit.WithRequestID(r.base, t.requestID), timeout)
	defer cancel()

	// Fixed rule 3: a chat that was sent sensitive content is summarised
	// only by a model that may read it. The summary route is routed as
	// sensitive then, as every route of such a chat is.
	held := r.sensitive(ctx, s, t, false)
	sm, provider, secrets, failure := r.summaryRoute(ctx, s, t, m)
	if failure != nil {
		return fail(failure.Message)
	}
	path, err := earlier(s, t.ChatID, t.UserMessageID)
	if err != nil || len(path) == 0 {
		return fail(fmt.Sprintf("the chat could not be read: %v", err))
	}
	metas, err := metaOf(ctx, s, t.ChatID)
	if err != nil {
		return fail(err.Error())
	}
	o2 := r.o.Config.Budget
	estimate := func(text string) int {
		return budget.Estimate(seam.Request{System: []seam.Block{{Type: seam.BlockText, Text: text}}}, o2)
	}
	heading := r.setting(ctx, s, KeyCompactHeading)
	spans := make([][2]int, len(rec.Turns)-1)
	for i := range spans {
		spans[i] = [2]int{rec.Turns[i].First, rec.Turns[i].First + rec.Turns[i].Messages}
	}
	fits := func(req seam.Request) bool {
		return budget.Estimated(req, o2).Tokens <= budget.InputRoom(sm, req.MaxOutputTokens)
	}

	prev, extends, from := h.summary.Text, h.summary.ID, span.From
	for call := 0; call < o.CallsMax && from < span.To; call++ {
		req, to, err := compact.Request(compact.Ask{
			Options: o, Model: sm.Name, Previous: prev, Messages: messages, Turns: spans,
			Span: compact.Span{From: from, To: span.To}, Room: rec.SummaryRoom - estimate(withRule(heading, " ")),
			CharsPerToken: o2.CharsPerToken, Fits: fits,
		})
		if err != nil {
			return fail(err.Error())
		}
		through, ok := throughID(path, metas, m.Name, messages, rec.Turns[to].First)
		if !ok {
			return fail("the turns to summarise are no longer the start of the chat's branch")
		}
		out := r.call(ctx, s, t, stepSummary, sm, provider, secrets, req, func(string, any) {})
		if out.closing {
			return false
		}
		if out.err != nil {
			why := "the summary call failed"
			if out.failure != nil {
				why += ": " + out.failure.Message
			}
			return fail(why)
		}
		text, err := compact.Answer(out.resp)
		if err != nil {
			return fail(err.Error())
		}
		saved, err := compact.Save(context.Background(), s, compact.Summary{
			ChatID: t.ChatID, FirstID: path[0].ID, ThroughID: through, Turns: to, Extends: extends,
			Text: text, Model: out.model.Name, Sensitive: held || r.sensitive(context.Background(), s, t, false),
			Tokens: estimate(text), TurnID: t.ID,
		})
		if err != nil {
			return fail("the summary could not be kept: " + err.Error())
		}
		prev, extends, from = text, saved.ID, to
	}
	return false
}

// summaryRoute is the route a summary call goes to: the model the
// summary rule names, then the chat's own, then the fallback chain, each
// behind the budget's guard and the monthly cap. The test tenant has no
// route and gets its recorded turns.
func (r *Runner) summaryRoute(ctx context.Context, s *tenant.Store, t row, chatModel models.Model) (models.Model, seam.Provider, []string, *TurnError) {
	test, err := testtenant.IsTest(s)
	if err != nil {
		return models.Model{}, nil, nil, &TurnError{Code: CodeFailed, Message: "the test tenant's mark could not be read: " + err.Error()}
	}
	if test {
		tm := r.testModel()
		return tm, budget.Guard(tm, r.test, r.o.Config.Budget), nil, nil
	}
	return r.routed(ctx, s, t, routing.KindSummary, chatModel.Name)
}

// throughID is the id of the last message of the branch whose messages,
// as a call to model sends them, are the first n of messages. ok is false
// when there is none: the branch changed under the call.
func throughID(path []chat.Message, metas map[int64]meta, model string, messages []seam.Message, n int) (int64, bool) {
	if n < 1 || n > len(messages) {
		return 0, false
	}
	// The largest number of the branch's messages that make no more than n
	// messages of a call. More of the branch never makes fewer.
	k := sort.Search(len(path)+1, func(i int) bool { return len(buildMessages(path[:i], metas, model)) > n }) - 1
	if k < 1 {
		return 0, false
	}
	head := buildMessages(path[:k], metas, model)
	if len(head) != n || !reflect.DeepEqual(head, messages[:n]) {
		return 0, false
	}
	return path[k-1].ID, true
}
