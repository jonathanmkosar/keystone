package turn

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/tools"
)

// This file joins recall (P6-05) to a turn. The person's knowledge store is
// searched once, with their newest message, before the turn's route is
// chosen, because what the search found decides which models the turn may
// be sent to. Each model call of the turn is then given what was found, and
// the search-first rule, for the least of the models its route holds.
// internal/knowledge/recall does the work. It is a guard of fixed rule 3
// and reads no setting, so the settings are read here.

// The bounds of knowledge.recall_timeout.
const (
	recallTimeoutMin = 100 * time.Millisecond
	recallTimeoutMax = time.Minute
)

// Why nothing was searched, for the turn inspector's record.
const (
	noteRecallOff  = "Recall is switched off, so the knowledge store was not searched."
	noteNotOpened  = "The knowledge store did not open, so nothing was recalled. The reason is in the server's log."
	noteNoAnswer   = "The knowledge store did not answer in time, so nothing was recalled. The reason is in the server's log."
	noteBadOptions = "The recall settings do not read, so nothing was recalled. The reason is in the server's log."
)

// turnRecall is what recall found for one turn. The turn's row carries it
// from the search to the route and to each model call.
type turnRecall struct {
	// adapter is the person's knowledge store adapter. Empty when they have
	// none, and then nothing is searched and nothing is recorded.
	adapter string
	// open says the store opened for this turn. The search-first rule is
	// then part of the standing instructions.
	open bool
	// note says why nothing was searched, when nothing was.
	note string
	// found is what the search brought back, for every kind of model.
	found recall.Found
	// unsaved says the record could not be kept. Without it a chat sent
	// sensitive content would not be held, so none is sent.
	unsaved bool
	// lookups are the knowledge lookups the current model call offered, by
	// name, made for the least of its route. See lookups.go.
	lookups map[string]tools.Tool
}

// searchKnowledge searches the person's knowledge store with the turn's
// message. It never stops a turn: a store that is missing, down or slow,
// and a setting that does not read, give nothing and one line in the log.
// With the none adapter nothing is opened.
func (r *Runner) searchKnowledge(ctx context.Context, s *tenant.Store, t row) *turnRecall {
	tr := &turnRecall{}
	if r.setup() != nil {
		return tr // the turn fails when it opens its model, and says why
	}
	get := func(key string) (string, bool) {
		v, err := tenantsettings.Get(ctx, s, r.reg, key)
		return strings.TrimSpace(v), err == nil
	}
	name, ok := get(knowledge.SettingAdapter)
	if !ok || name == "" || name == knowledge.None {
		return tr
	}
	tr.adapter = name

	ks, err := knowledge.Open(ctx, name, s)
	if err != nil {
		tr.note = noteNotOpened
		r.logf("turns: %s turn %s: recall: the knowledge store %s did not open, so nothing is recalled: %v", t.requestID, t.ID, name, err)
		return tr
	}
	defer ks.Close()
	tr.open = true

	if on, _ := get(recall.SettingOn); on != "true" {
		tr.note = noteRecallOff
		return tr
	}
	o, timeout, err := recallOptions(get, name)
	if err != nil {
		tr.note = noteBadOptions
		r.logf("turns: %s turn %s: recall: %v", t.requestID, t.ID, err)
		return tr
	}
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	found, err := recall.Find(sctx, ks, newestText(s, t), o)
	if err != nil {
		tr.note = noteNoAnswer
		if ctx.Err() == nil { // a turn that was stopped needs no line
			r.logf("turns: %s turn %s: recall: searching the knowledge store %s gave nothing: %v", t.requestID, t.ID, name, err)
		}
		return tr
	}
	tr.found = found
	return tr
}

// recallOptions reads recall's limits for one adapter, and its timeout.
func recallOptions(get func(string) (string, bool), adapter string) (recall.Options, time.Duration, error) {
	var bad []string
	text := func(key string) string {
		v, ok := get(key)
		if !ok {
			bad = append(bad, key)
		}
		return v
	}
	whole := func(key string) int {
		n, err := strconv.Atoi(text(key))
		if err != nil {
			bad = append(bad, key)
		}
		return n
	}
	minKey := recall.SettingMinScore(adapter)
	minScore, err := strconv.ParseFloat(text(minKey), 64)
	if err != nil {
		bad = append(bad, minKey)
	}
	timeout, err := time.ParseDuration(text(recall.SettingTimeout))
	if err != nil || timeout < recallTimeoutMin || timeout > recallTimeoutMax {
		bad = append(bad, recall.SettingTimeout)
	}
	o := recall.Options{
		MinScore:      minScore,
		SearchResults: whole(recall.SettingSearchResults),
		MaxItems:      whole(recall.SettingMaxItems),
		ItemMaxChars:  whole(recall.SettingItemMaxChars),
		QueryMaxChars: whole(recall.SettingQueryMaxChars),
		IgnoreWords:   strings.Fields(text(recall.SettingIgnoreWords)),
	}
	if len(bad) > 0 {
		return recall.Options{}, 0, errors.New("these settings do not read: " + strings.Join(bad, ", "))
	}
	if err := recall.CheckOptions(o); err != nil {
		return recall.Options{}, 0, err
	}
	return o, timeout, nil
}

// newestText is the person's newest message, which recall searches with:
// the message sent, or for a retry the message the old reply answered.
func newestText(s *tenant.Store, t row) string {
	if t.input.RetryOf == 0 {
		return seam.Text(seam.Message{Role: seam.RoleUser, Blocks: t.input.content()})
	}
	old, err := chat.GetMessage(s, t.ChatID, t.input.RetryOf)
	if err != nil || old.ParentID == 0 {
		return ""
	}
	asked, err := chat.GetMessage(s, t.ChatID, old.ParentID)
	if err != nil {
		return ""
	}
	return seam.Text(seam.Message{Role: asked.Role, Blocks: asked.Blocks})
}

// sensitive is fixed rule 3's answer for routing: whether a call of the
// turn carries content from a sensitive space. It does when the chat was
// sent such content before, since its history may repeat it. It does when
// the model the chat runs on may read sensitive content and the turn's
// recall kept some for it. A chat whose record cannot be read counts as
// sensitive. mayRead is the chat's model's Model.MayReadSensitive, and
// false for a call of another kind, which only the held mark decides.
func (r *Runner) sensitive(ctx context.Context, s *tenant.Store, t row, mayRead bool) bool {
	held, err := recall.Held(ctx, s, t.ChatID)
	if err != nil {
		r.logf("turns: %s turn %s: reading whether the chat was sent sensitive content, so it is taken that it was: %v", t.requestID, t.ID, err)
		return true
	}
	return held || (mayRead && t.recalled != nil && t.recalled.found.For(true).Sensitive)
}

// readsSensitive is the answer for every model one call may reach: true
// only when each model of its route may read sensitive content. A call is
// built once and sent as it is to whichever model of the route answers, so
// it is built for the least of them. A provider that is not a route stands
// for the one model it was opened for.
func readsSensitive(p seam.Provider, m models.Model) bool {
	if route, ok := p.(interface{ Models() []models.Model }); ok {
		all := route.Models()
		for _, x := range all {
			if !x.MayReadSensitive() {
				return false
			}
		}
		return len(all) > 0
	}
	return m.MayReadSensitive()
}

// recallFor returns what one model call of the turn is given: the recalled
// items, for a route whose models may or may not read sensitive content,
// and the search-first rule when a knowledge store is open. The record of
// the first call is kept for the turn inspector, and it holds the chat to
// models that may read sensitive content when an item was from a
// sensitive space.
func (r *Runner) recallFor(ctx context.Context, s *tenant.Store, t row, step int, mayRead bool) (items []contextbuild.Item, rule string) {
	tr := t.recalled
	if tr == nil || tr.adapter == "" {
		return nil, ""
	}
	if tr.open {
		rule = r.setting(ctx, s, recall.SettingSearchFirstText)
	}
	out := tr.found.For(mayRead && !tr.unsaved)
	if step == 1 {
		rec := out.Record
		rec.Adapter, rec.Note = tr.adapter, tr.note
		if err := recall.Save(context.Background(), s, t.ID, t.ChatID, rec); err != nil {
			r.logf("turns: %s turn %s: keeping the record of what was recalled, so nothing from a sensitive space is sent: %v", t.requestID, t.ID, err)
			if out.Sensitive {
				tr.unsaved = true
				out = tr.found.For(false)
			}
		}
	}
	refs := recallRefs(out.Record) // the inspector's names for them (P5-04)
	for i, it := range out.Items {
		items = append(items, contextbuild.Item{Label: it.Label, Text: it.Text, Ref: refAt(refs, i)})
	}
	return items, rule
}

// withRule adds a rule to the standing instructions, as a paragraph of
// its own.
func withRule(system, rule string) string {
	switch {
	case rule == "":
		return system
	case system == "":
		return rule
	}
	return system + "\n\n" + rule
}

// knowledgeLookups returns the three knowledge lookups for one model call
// of the turn, with their limits from the person's settings, filtered for
// the least of the models the call's route holds. The tool loop (P7-01)
// offers them, and runs each with the tenant's store and the knowledge
// store it opens with knowledge.Open for the person's knowledge.adapter.
// When a lookup's result is sensitive, the tool loop calls recall.Hold, so
// the chat is held to models that may read it.
func (r *Runner) knowledgeLookups(ctx context.Context, s *tenant.Store, p seam.Provider, m models.Model) ([]recall.Lookup, error) {
	number := func(key string) (int, error) {
		v, err := tenantsettings.Get(ctx, s, r.reg, key)
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(strings.TrimSpace(v))
	}
	results, err := number(recall.SettingLookupResults)
	if err != nil {
		return nil, err
	}
	most, err := number(recall.SettingLookupMaxChars)
	if err != nil {
		return nil, err
	}
	return recall.Lookups(readsSensitive(p, m), recall.LookupOptions{MaxResults: results, MaxChars: most})
}
