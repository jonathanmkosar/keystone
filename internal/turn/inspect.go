package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/compact"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/inspector"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// This file joins the turn inspector (P5-04) to a turn. Each model call of
// a turn leaves out what the person took out of it from the inspector, and
// the inspector's routes show one turn and keep what a chat's next turn
// leaves out. internal/inspector puts the calls together and keeps the
// skips; what is here reads the turn's rows and answers the routes.

// previewChars is the most of a turn's first message the inspector shows
// beside it. It is a bound on the screen's layout, not a limit a person
// tunes.
const previewChars = 80

// skipLookback is how many of a chat's newest turns are read for a
// recalled item a person takes out of the next turn. It bounds the work of
// one click, and is not a limit a person tunes.
const skipLookback = 50

// leaveOut marks the recalled items and pinned messages the person took
// out of this turn as removed, so the context builder leaves them out. The
// turn's first call claims every skip that waits in its chat, and its
// later calls read the same ones, so the next turn is the only one that
// leaves an item out. Nothing here stops a call: skips that cannot be read
// leave nothing out, and one line in the log says so.
func (r *Runner) leaveOut(s *tenant.Store, t row, step int, items []contextbuild.Item, pins []contextbuild.Pin) ([]contextbuild.Item, []contextbuild.Pin) {
	var skips []inspector.Skip
	var err error
	if step == 1 {
		skips, err = inspector.Claim(context.Background(), s, t.ChatID, t.ID)
	} else {
		skips, err = inspector.OfTurn(context.Background(), s, t.ID)
	}
	if err != nil {
		r.logf("turns: %s turn %s: reading what the person took out of the turn, so nothing is left out of call %d: %v", t.requestID, t.ID, step, err)
		return items, pins
	}
	return inspector.LeaveOut(items, pins, skips)
}

// messageRef is the inspector's name for a pinned message.
func messageRef(id int64) string {
	return inspector.Ref(inspector.KindMessage, strconv.FormatInt(id, 10))
}

// summaryRef is the inspector's name for a summary, and empty for none.
func summaryRef(sum compact.Summary) string {
	if sum.ID == 0 {
		return ""
	}
	return "summary:" + strconv.FormatInt(sum.ID, 10)
}

// recallRefs are the inspector's names for the items a recall kept, in the
// order of its items: recall.Found.For lists the hits it kept in the order
// it gives their items.
func recallRefs(rec recall.Record) []string {
	var out []string
	for _, h := range rec.Hits {
		if h.Kept {
			out = append(out, inspector.Ref(h.Type, h.ID))
		}
	}
	return out
}

// refAt is refs[i], or nothing.
func refAt(refs []string, i int) string {
	if i < len(refs) {
		return refs[i]
	}
	return ""
}

// InspectReply answers GET chats/{id}/turns/{turn}/inspect: what went into
// each model call of one turn, and what the calls used.
type InspectReply struct {
	Turn Turn `json:"turn"`
	// Calls are the turn's model calls: its chat calls by step, each with
	// what it was built from, then the title, summary and other calls the
	// turn set off, in the order they were made. Each has its usage
	// records and their total.
	Calls []inspector.Call `json:"calls"`
	// Recall is what the turn's search of the person's knowledge store
	// found, kept, dropped and withheld, and why, with where each hit came
	// from. It names sensitive pages, so it is shown to the person and
	// never sent to a model. Absent when the person has no store.
	Recall *recall.Record `json:"recall,omitempty"`
	// Summaries are the summaries of older turns the calls were given, as
	// they are stored with the chat. Read only.
	Summaries []compact.Summary `json:"summaries"`
	// Totals is the sum of every usage record of the turn: the records
	// under the request id of the call that sent it.
	Totals inspector.Totals `json:"totals"`
	// Pinned are the ids of the chat's pinned messages now, oldest first.
	Pinned []int64 `json:"pinned"`
	// Skips are what the chat's next turn will leave out.
	Skips []inspector.Skip `json:"skips"`
}

// SkipsReply answers the skip routes: what the chat's next turn leaves out.
type SkipsReply struct {
	ChatID string `json:"chat_id"`
	// Skips are the recalled pages and sources, and the pinned messages,
	// the chat's next turn leaves out, oldest first.
	Skips []inspector.Skip `json:"skips"`
}

func (r *Runner) inspect(w http.ResponseWriter, req *http.Request, c api.Call) {
	t, err := getTurn(req.Context(), c.Store, req.PathValue("id"), req.PathValue("turn"))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply, err := r.inspection(req.Context(), c.Store, t)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

// inspection reads everything the inspector shows of one turn.
func (r *Runner) inspection(ctx context.Context, s *tenant.Store, t row) (InspectReply, error) {
	stored, err := Contexts(ctx, s, t.ID)
	if err != nil {
		return InspectReply{}, err
	}
	contexts := make([]inspector.Context, len(stored))
	for i, c := range stored {
		contexts[i] = inspector.Context{Step: c.Step, Record: c.Record}
	}
	var records []usage.Record
	if t.requestID != "" { // an empty filter would read every record of the tenant
		if records, err = usage.List(ctx, s, usage.Filter{RequestID: t.requestID}); err != nil {
			return InspectReply{}, err
		}
	}
	calls, totals := inspector.Calls(contexts, records)
	r.previews(ctx, s, t, calls)
	if calls == nil {
		calls = []inspector.Call{}
	}
	reply := InspectReply{Turn: t.Turn, Calls: calls, Totals: totals, Summaries: []compact.Summary{}}

	saved, ok, err := recall.Saved(ctx, s, t.ID)
	if err != nil {
		return InspectReply{}, err
	}
	if ok {
		reply.Recall = &saved
	}

	given := map[string]bool{}
	for _, c := range calls {
		if c.Context != nil {
			if ref := c.Context.Part(contextbuild.PartSummary).Ref; ref != "" {
				given[ref] = true
			}
		}
	}
	if len(given) > 0 {
		sums, err := compact.List(ctx, s, t.ChatID)
		if err != nil {
			return InspectReply{}, err
		}
		for _, sum := range sums {
			if given[summaryRef(sum)] {
				reply.Summaries = append(reply.Summaries, sum)
			}
		}
	}

	if reply.Pinned, err = chat.Pins(s, t.ChatID); err != nil {
		return InspectReply{}, err
	}
	if reply.Pinned == nil {
		reply.Pinned = []int64{}
	}
	if reply.Skips, err = inspector.Waiting(ctx, s, t.ChatID); err != nil {
		return InspectReply{}, err
	}
	return reply, nil
}

// previews gives each chat call the first words of the message that starts
// each turn it lists. The branch is read down to the turn's message, as a
// call to the model it was built for sends it: the turns before the
// current one are the same in every call of the turn, and the current one
// starts at the person's message. A branch that cannot be read gives no
// previews and one line in the log.
func (r *Runner) previews(ctx context.Context, s *tenant.Store, t row, calls []inspector.Call) {
	if t.UserMessageID == 0 {
		return
	}
	branches := map[string][]seam.Message{}
	for i := range calls {
		c := &calls[i]
		if c.Context == nil {
			continue
		}
		messages, read := branches[c.Context.Model]
		if !read {
			var err error
			if messages, err = r.branch(ctx, s, t.ChatID, t.UserMessageID, c.Context.Model); err != nil {
				r.logf("turns: inspecting turn %s: reading its branch, so its turns have no previews: %v", t.ID, err)
			}
			branches[c.Context.Model] = messages
		}
		if len(messages) == 0 {
			continue
		}
		c.Previews = make([]string, len(c.Context.Turns))
		for j, turn := range c.Context.Turns {
			if turn.First >= 0 && turn.First < len(messages) {
				c.Previews[j] = preview(seam.Text(messages[turn.First]))
			}
		}
	}
}

// preview puts a text on one line and cuts it to previewChars.
func preview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= previewChars {
		return text
	}
	return strings.TrimSpace(string([]rune(text)[:previewChars-1])) + "…"
}

// errNotRecalled says a page or source a person would take out of the next
// turn was not given to any of the chat's recent calls.
var errNotRecalled = errors.New("not recalled")

func (r *Runner) skips(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	r.skipsReply(w, req, c, id)
}

func (r *Runner) skip(w http.ResponseWriter, req *http.Request, c api.Call) {
	ctx := req.Context()
	id, kind, ref := req.PathValue("id"), req.PathValue("kind"), req.PathValue("ref")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	if err := inspector.Check(kind, ref); err != nil {
		refused(w, req, strings.TrimPrefix(err.Error(), "inspector: "))
		return
	}
	label, err := r.skipLabel(ctx, c.Store, id, kind, ref)
	switch {
	case errors.Is(err, errNotRecalled):
		api.Error(w, req, http.StatusNotFound, "not_found", fmt.Sprintf("no recent turn of this chat was given %s %s", kind, ref))
		return
	case err != nil:
		r.answer(w, req, c, err)
		return
	}
	if err := inspector.Add(ctx, c.Store, id, inspector.Skip{Kind: kind, Ref: ref, Label: label}); err != nil {
		r.answer(w, req, c, err)
		return
	}
	r.skipsReply(w, req, c, id)
}

func (r *Runner) unskip(w http.ResponseWriter, req *http.Request, c api.Call) {
	id, kind, ref := req.PathValue("id"), req.PathValue("kind"), req.PathValue("ref")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	if err := inspector.Check(kind, ref); err != nil {
		refused(w, req, strings.TrimPrefix(err.Error(), "inspector: "))
		return
	}
	if err := inspector.Remove(req.Context(), c.Store, id, kind, ref); err != nil {
		r.answer(w, req, c, err)
		return
	}
	r.skipsReply(w, req, c, id)
}

func (r *Runner) skipsReply(w http.ResponseWriter, req *http.Request, c api.Call, chatID string) {
	list, err := inspector.Waiting(req.Context(), c.Store, chatID)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, SkipsReply{ChatID: chatID, Skips: list})
}

// skipLabel checks that an item can be taken out of a chat's next turn,
// and names it as the inspector shows it. A message must be one of the
// chat's pinned messages. A page or source must have been given to a call
// of one of the chat's newest turns, and is named as that call named it.
func (r *Runner) skipLabel(ctx context.Context, s *tenant.Store, chatID, kind, ref string) (string, error) {
	if kind == inspector.KindMessage {
		n, _ := strconv.ParseInt(ref, 10, 64) // inspector.Check read it
		msg, err := chat.GetMessage(s, chatID, n)
		if err != nil {
			return "", err
		}
		pinned, err := chat.Pins(s, chatID)
		if err != nil {
			return "", err
		}
		for _, id := range pinned {
			if id == n {
				return fmt.Sprintf("pinned message %d: %s", n, preview(seam.Text(seam.Message{Role: msg.Role, Blocks: msg.Blocks}))), nil
			}
		}
		return "", fmt.Errorf("%w: only a pinned message can be left out of the next turn, and message %d is not pinned", chat.ErrInvalid, n)
	}
	rows, err := s.DB().QueryContext(ctx, `SELECT c.record FROM turn_contexts c JOIN turns t ON t.id = c.turn_id
		WHERE t.chat_id = ? ORDER BY t.created_at DESC, c.step DESC LIMIT ?`, chatID, skipLookback)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	want := inspector.Ref(kind, ref)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return "", err
		}
		var rec contextbuild.Record
		if json.Unmarshal([]byte(raw), &rec) != nil {
			continue // a record that does not read names nothing
		}
		for _, it := range rec.Part(contextbuild.PartRecall).Items {
			if it.Ref == want {
				return it.Label, nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return "", errNotRecalled
}
