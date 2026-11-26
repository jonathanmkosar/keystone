package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/attach"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/recap"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/approval"
)

// This file joins attached chats and handoff (P5-06) to the chat routes and
// to each model call. A chat can attach other chats of the same person;
// each call of it carries every attached chat as its recap (recap.go makes
// one when it has none, or its chat has moved on), in the context builder's
// attached part, and offers open_attached_chat, which reads the rest of
// one a page at a time. A handoff makes a fresh chat with this one attached
// as its parent, so it is carried, and opened, in the same way.
// internal/attach keeps the links and holds fixed rule 3's answer for
// attached chats; it reads no setting, so the settings are read here.
// docs/architecture/attach-and-handoff.md is the whole of it.

// The setting the turn reads beside attach.Options, in settings/chats.json.
const KeyAttachedSummaryWait = "chats.attached_summary_wait"

// The bounds of chats.attached_summary_wait.
const (
	attachedWaitMin = time.Second
	attachedWaitMax = 10 * time.Minute
)

// errAttachedNotHeld is what the model reads when an attached chat held
// sensitive content and this chat could not be held for it.
var errAttachedNotHeld = errors.New("the attached chat holds content from a sensitive space, and this chat could not be marked as holding it, so it was not given")

// attachOptions reads what attaching and reading attached chats take, and
// how long a turn waits for an attached chat's recap.
func (r *Runner) attachOptions(ctx context.Context, s *tenant.Store) (attach.Options, time.Duration, error) {
	var bad []string
	whole := func(key string) int {
		n, err := strconv.Atoi(r.setting(ctx, s, key))
		if err != nil {
			bad = append(bad, key)
		}
		return n
	}
	o := attach.Options{
		Max:          whole(attach.SettingMax),
		PageMessages: whole(attach.SettingPageMessages),
		PageMaxChars: whole(attach.SettingPageMaxChars),
	}
	wait, err := time.ParseDuration(r.setting(ctx, s, KeyAttachedSummaryWait))
	if err != nil || wait < attachedWaitMin || wait > attachedWaitMax {
		bad = append(bad, KeyAttachedSummaryWait)
	}
	if len(bad) > 0 {
		return attach.Options{}, 0, errors.New("these settings do not read: " + strings.Join(bad, ", "))
	}
	return o, wait, attach.CheckOptions(o)
}

// AttachedChat is one chat attached to a chat, as the chat screen shows it.
type AttachedChat struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Summary is the summary of its recap, absent until it has one.
	Summary string `json:"summary,omitempty"`
	// Parent says the chat continues from this one: it was handed off from
	// it, and it cannot be taken off.
	Parent bool `json:"parent"`
	// Sensitive says it holds content from a sensitive space: it reaches
	// only calls whose every model may read such content.
	Sensitive bool `json:"sensitive"`
	// Incognito says it was made incognito after it was attached, so no
	// call carries it.
	Incognito bool      `json:"incognito"`
	Added     time.Time `json:"added"`
}

// ChatRef names a chat, for a link to it.
type ChatRef struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Created time.Time `json:"created"`
}

// AttachedReply answers the routes of attached chats.
type AttachedReply struct {
	ChatID string `json:"chat_id"`
	// Attached are the chats attached to this one: the one it continues
	// from first, then the rest in the order they were attached. Each call
	// of the chat carries them.
	Attached []AttachedChat `json:"attached"`
	// Parent is the chat this one continues from, when it was handed off
	// from one.
	Parent *ChatRef `json:"parent,omitempty"`
	// Handoffs are the chats handed off from this one, oldest first.
	Handoffs []ChatRef `json:"handoffs"`
	// Max is how many chats it may have attached: chats.attach_max.
	Max int `json:"max"`
	// Incognito says this chat is incognito: it attaches nothing, and is
	// neither attached nor handed off.
	Incognito bool `json:"incognito"`
}

// HandoffReply answers POST chats/{id}/handoff.
type HandoffReply struct {
	// Chat is the new chat, with no messages, the model it will use and the
	// chat it continues from attached.
	Chat ChatReply `json:"chat"`
	// Parent is the chat it continues from.
	Parent ChatRef `json:"parent"`
	// Model is the model the parent's next turn would use. A screen may
	// send it with the new chat's first message, so it goes on with the
	// same model.
	Model string `json:"model,omitempty"`
}

// AttachRoutes returns the routes of attached chats and handoff (P5-06),
// served by r.
func AttachRoutes(r *Runner) []api.Route {
	const one = api.Prefix + "chats/{id}"
	return []api.Route{
		{
			Method: "GET", Path: one + "/attached", Handle: r.attachedRead,
			Summary: "The chats attached to the chat, with their summaries: the chat it continues from first, then the rest. Also the chats handed off from it.",
			Reply:   AttachedReply{},
		},
		{
			Method: "PUT", Path: one + "/attached/{other}", Handle: r.attachAdd,
			Summary: "Attach another of the person's chats to the chat. Each call of the chat then carries its summary, and the model can open the rest of it. It takes no body. Attaching a chat twice changes nothing. An incognito chat is refused on either side (409 incognito), and so is one past chats.attach_max (409 too_many).",
			Reply:   AttachedReply{},
		},
		{
			Method: "DELETE", Path: one + "/attached/{other}", Handle: r.attachRemove,
			Summary: "Take an attached chat off the chat. The chat it continues from stays. Taking off a chat that is not attached changes nothing.",
			Reply:   AttachedReply{},
		},
		{
			Method: "POST", Path: one + "/handoff", Handle: r.handoff,
			Summary: "Hand the chat off: make a fresh chat that continues from it, with this one attached, so each of its calls carries this chat's summary and the model can open the rest. It takes no body. An incognito chat is refused (409 incognito), and so is a chat with no messages.",
			Reply:   HandoffReply{},
		},
	}
}

// attachFailed answers an error of internal/attach.
func (r *Runner) attachFailed(w http.ResponseWriter, req *http.Request, c api.Call, err error) {
	switch {
	case errors.Is(err, attach.ErrNotFound):
		r.answer(w, req, c, chat.ErrNotFound)
	case errors.Is(err, attach.ErrSelf):
		refused(w, req, "a chat cannot be attached to itself")
	case errors.Is(err, attach.ErrParent):
		refused(w, req, "the chat this one continues from stays attached")
	case errors.Is(err, attach.ErrFull):
		api.Error(w, req, http.StatusConflict, "too_many", "this chat has as many attached chats as chats.attach_max allows; take one off first")
	case errors.Is(err, attach.ErrIncognito):
		api.Error(w, req, http.StatusConflict, "incognito", "an incognito chat is not attached to anything, attaches nothing and is not handed off")
	default:
		r.answer(w, req, c, err)
	}
}

func (r *Runner) attachedRead(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	r.attachedAnswer(w, req, c, id)
}

func (r *Runner) attachAdd(w http.ResponseWriter, req *http.Request, c api.Call) {
	id, other := req.PathValue("id"), req.PathValue("other")
	for _, x := range []string{id, other} {
		if _, err := chat.Get(c.Store, x); err != nil {
			r.answer(w, req, c, err) // another tenant's chat is not in this store: not found
			return
		}
	}
	if err := r.setup(); err != nil {
		r.answer(w, req, c, err)
		return
	}
	o, _, err := r.attachOptions(req.Context(), c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	if _, err := attach.Add(req.Context(), c.Store, id, other, o.Max); err != nil {
		r.attachFailed(w, req, c, err)
		return
	}
	r.attachedAnswer(w, req, c, id)
}

func (r *Runner) attachRemove(w http.ResponseWriter, req *http.Request, c api.Call) {
	id, other := req.PathValue("id"), req.PathValue("other")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	if _, err := attach.Remove(req.Context(), c.Store, id, other); err != nil {
		r.attachFailed(w, req, c, err)
		return
	}
	r.attachedAnswer(w, req, c, id)
}

func (r *Runner) attachedAnswer(w http.ResponseWriter, req *http.Request, c api.Call, chatID string) {
	if err := r.setup(); err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply, err := r.attachedReply(req.Context(), c.Store, chatID)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

// attachedReply is what the chat screen is told of a chat's attached chats
// and handoffs. The person's own screen shows a sensitive chat's title and
// summary too: fixed rule 3 is about what reaches a model.
func (r *Runner) attachedReply(ctx context.Context, s *tenant.Store, chatID string) (AttachedReply, error) {
	o, _, err := r.attachOptions(ctx, s)
	if err != nil {
		return AttachedReply{}, err
	}
	incognito, err := attach.Incognito(ctx, s, chatID)
	if err != nil {
		return AttachedReply{}, err
	}
	links, err := attach.List(ctx, s, chatID)
	if err != nil {
		return AttachedReply{}, err
	}
	kids, err := attach.Handoffs(ctx, s, chatID)
	if err != nil {
		return AttachedReply{}, err
	}
	ids := make([]string, len(links))
	for i, l := range links {
		ids[i] = l.AttachedID
	}
	sums, err := recap.Summaries(ctx, s, ids)
	if err != nil {
		return AttachedReply{}, err
	}
	out := AttachedReply{ChatID: chatID, Attached: []AttachedChat{}, Handoffs: []ChatRef{}, Max: o.Max, Incognito: incognito}
	for _, l := range links {
		c, err := chat.Get(s, l.AttachedID)
		if errors.Is(err, chat.ErrNotFound) {
			continue // deleted while this was read
		}
		if err != nil {
			return AttachedReply{}, err
		}
		ans, _ := attach.MayShow(ctx, s, c.ID, false) // only its marks are read here
		out.Attached = append(out.Attached, AttachedChat{
			ID: c.ID, Title: c.Title, Summary: sums[c.ID], Parent: l.Handoff,
			Sensitive: ans.Sensitive, Incognito: ans.Incognito, Added: l.Added,
		})
		if l.Handoff {
			out.Parent = &ChatRef{ID: c.ID, Title: c.Title, Created: c.Created}
		}
	}
	for _, l := range kids {
		c, err := chat.Get(s, l.ChatID)
		if errors.Is(err, chat.ErrNotFound) {
			continue
		}
		if err != nil {
			return AttachedReply{}, err
		}
		out.Handoffs = append(out.Handoffs, ChatRef{ID: c.ID, Title: c.Title, Created: c.Created})
	}
	return out, nil
}

// handoff makes a fresh chat that continues from this one. The new chat
// has no messages; this one is attached to it as its parent, so each of
// its calls carries this chat's recap, made at its first turn when this
// chat has none or has moved on since.
func (r *Runner) handoff(w http.ResponseWriter, req *http.Request, c api.Call) {
	ctx := req.Context()
	id := req.PathValue("id")
	parent, err := chat.Get(c.Store, id)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	if err := r.setup(); err != nil {
		r.answer(w, req, c, err)
		return
	}
	if on, err := attach.Incognito(ctx, c.Store, id); err != nil || on {
		if err != nil {
			r.answer(w, req, c, err)
			return
		}
		r.attachFailed(w, req, c, attach.ErrIncognito)
		return
	}
	path, err := chat.Path(c.Store, id)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	if len(path) == 0 {
		refused(w, req, "the chat has no messages to carry into a new one")
		return
	}
	made, err := chat.Create(c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	if err := attach.Handoff(ctx, c.Store, id, made.ID); err != nil {
		if derr := chat.Delete(c.Store, made.ID); derr != nil {
			r.logf("%s handing off chat %s: removing the new chat %s again: %v", c.RequestID, id, made.ID, derr)
		}
		r.attachFailed(w, req, c, err)
		return
	}
	whole, err := r.whole(ctx, c.Store, made)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply := HandoffReply{Chat: whole, Parent: ChatRef{ID: parent.ID, Title: parent.Title, Created: parent.Created}}
	if choice, err := r.choice(ctx, c.Store, id); err == nil && choice.Model != nil {
		reply.Model = choice.Model.Name
	}
	api.WriteJSON(w, http.StatusCreated, reply)
}

// attachedLinks returns the chats a turn's chat carries: none when it is
// incognito, or its flag or its links cannot be read.
func (r *Runner) attachedLinks(ctx context.Context, s *tenant.Store, t row) []attach.Link {
	if on, err := attach.Incognito(ctx, s, t.ChatID); on {
		if err != nil {
			r.logf("turns: %s turn %s: reading whether the chat is incognito, so it carries no attached chat: %v", t.requestID, t.ID, err)
		}
		return nil
	}
	links, err := attach.List(ctx, s, t.ChatID)
	if err != nil {
		r.logf("turns: %s turn %s: reading the chat's attached chats, so none is carried: %v", t.requestID, t.ID, err)
		return nil
	}
	return links
}

// attachedSensitive says whether a chat the turn's chat carries holds
// content from a sensitive space. Runner.sensitive asks it for a chat whose
// own model may read such content, so the route keeps to models that may,
// as recall's sensitive items do (decision 0363). A chat whose links cannot
// be read counts as carrying such content.
func (r *Runner) attachedSensitive(ctx context.Context, s *tenant.Store, t row) bool {
	if on, _ := attach.Incognito(ctx, s, t.ChatID); on {
		return false // it carries nothing
	}
	links, err := attach.List(ctx, s, t.ChatID)
	if err != nil {
		r.logf("turns: %s turn %s: reading the chat's attached chats, so it is taken that one holds sensitive content: %v", t.requestID, t.ID, err)
		return true
	}
	for _, l := range links {
		ans, err := attach.MayShow(ctx, s, l.AttachedID, false)
		if err != nil {
			r.logf("turns: %s turn %s: reading attached chat %s's marks, so it counts as sensitive or incognito: %v", t.requestID, t.ID, l.AttachedID, err)
		}
		if !ans.Incognito && ans.Sensitive {
			return true
		}
	}
	return false
}

// attachedToolOffered says whether a call to m offers open_attached_chat,
// when its chat has attached chats: as for every tool, the model must take
// tools and the person must not have switched them off.
func (r *Runner) attachedToolOffered(ctx context.Context, s *tenant.Store, m models.Model) bool {
	if r.o.Tools == nil || !m.Has(models.CapTools) || !r.toolsOn(ctx, s) {
		return false
	}
	pols, _ := r.toolPolicies(ctx, s)
	return pols[attach.ToolName] != approval.Never
}

// attachedItems returns what one model call carries of the chats attached
// to the turn's chat, the one it continues from first: each as its title,
// id and recap. In the first call of a turn, an attached chat with no
// recap, or one its chat has moved past, is given one first. p and m are
// the call's provider and model, so the items are made for the least of
// the call's route: a chat that holds sensitive content is given only when
// every model of the route may read it, and the turn's chat is held first;
// otherwise it is left out, and a line says how many were. An incognito
// chat carries nothing, and an attached chat made incognito is left out.
// Nothing here stops a turn.
func (r *Runner) attachedItems(ctx context.Context, s *tenant.Store, t row, step int, p seam.Provider, m models.Model) []contextbuild.Item {
	links := r.attachedLinks(ctx, s, t)
	if len(links) == 0 {
		return nil
	}
	_, wait, err := r.attachOptions(ctx, s)
	if err != nil {
		r.logf("turns: %s turn %s: the attached chats are carried as they are, with no new summary: %v", t.requestID, t.ID, err)
		wait = 0
	}
	mayRead := readsSensitive(p, m) // for the least of the route's models
	tool := r.attachedToolOffered(ctx, s, m)
	var items []contextbuild.Item
	var sensitive []int
	withheld := 0
	for _, l := range links {
		if step == 1 && wait > 0 {
			r.freshRecap(ctx, s, t, l.AttachedID, wait)
		}
		ans, err := attach.MayShow(ctx, s, l.AttachedID, mayRead)
		if err != nil {
			r.logf("turns: %s turn %s: reading attached chat %s's marks, so it is left out: %v", t.requestID, t.ID, l.AttachedID, err)
		}
		if ans.Incognito {
			continue
		}
		if !ans.Show {
			withheld++
			continue
		}
		c, err := chat.Get(s, l.AttachedID)
		if err != nil {
			continue // deleted while the turn ran
		}
		path, err := chat.Path(s, l.AttachedID)
		if err != nil {
			continue
		}
		card := attach.Card{ID: c.ID, Title: c.Title, Active: c.Active, Parent: l.Handoff, Empty: len(path) == 0, Tool: tool}
		if rc, ok, err := recap.Get(ctx, s, c.ID); err == nil && ok {
			card.Recap = &rc
		}
		label, text := attach.Text(card)
		if ans.Sensitive {
			sensitive = append(sensitive, len(items))
		}
		// The chat it continues from is what a handoff is for, so it is
		// never left out for room; the others share chats.context_attached_percent.
		items = append(items, contextbuild.Item{Label: label, Text: text, Pinned: l.Handoff})
	}
	if len(sensitive) > 0 {
		// Held whatever happens to the call: what it carries stays in the
		// chat and is sent again with every later call of it.
		if err := recall.Hold(context.Background(), s, t.ID, t.ChatID); err != nil {
			r.logf("turns: %s turn %s: holding the chat for an attached chat's sensitive content, so it is left out: %v", t.requestID, t.ID, err)
			kept := items[:0:0]
			for i, it := range items {
				if !contains(sensitive, i) {
					kept = append(kept, it)
				}
			}
			items, withheld = kept, withheld+len(sensitive)
		}
	}
	if withheld > 0 {
		items = append(items, contextbuild.Item{Label: attach.LeftOutLabel, Text: attach.LeftOut(withheld), Pinned: true})
	}
	return items
}

func contains(list []int, n int) bool {
	for _, x := range list {
		if x == n {
			return true
		}
	}
	return false
}

// freshRecap gives an attached chat a recap when it has none, or its
// newest message is past the one its recap was made from, with the recap
// code of P5-05 (makeRecap), under the turn's request id, so the call is
// in the turn's audit trail and usage. It waits up to wait, also for a
// recap of the chat that is being made already, and leaves a chat whose
// recap failed lately alone until chats.recap_retry_after has passed. It
// never fails the turn: a chat with no recap is carried without one.
func (r *Runner) freshRecap(ctx context.Context, s *tenant.Store, t row, chatID string, wait time.Duration) {
	fresh := func() bool {
		path, err := chat.Path(s, chatID)
		if err != nil || len(path) == 0 {
			return true // nothing to make one from
		}
		have, ok, err := recap.Get(ctx, s, chatID)
		return err != nil || (ok && have.ThroughID == path[len(path)-1].ID)
	}
	if fresh() {
		return
	}
	key := s.TenantID() + "/" + chatID
	if retry, err := time.ParseDuration(r.setting(ctx, s, KeyRecapRetryAfter)); err == nil {
		r.recaps.mu.Lock()
		r.recaps.ready()
		failed := r.recaps.failed[key]
		r.recaps.mu.Unlock()
		if !failed.IsZero() && time.Since(failed) < retry {
			return
		}
	}
	deadline := time.Now().Add(wait)
	for !r.recaps.start(key) {
		// Being made already, after a turn of that chat or for another
		// chat that carries it: wait for it.
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	if fresh() {
		r.recaps.done(key, false)
		return
	}
	model := ""
	if choice, err := r.choice(ctx, s, chatID); err == nil && choice.Model != nil {
		model = choice.Model.Name
	}
	cctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	_, err := r.makeRecap(cctx, s, row{Turn: Turn{ID: t.ID, ChatID: chatID, Model: model}, requestID: t.requestID, actor: t.actor})
	r.recaps.done(key, err != nil && !errors.Is(err, errClosing))
	if err != nil && ctx.Err() == nil {
		r.logf("turns: %s turn %s: attached chat %s was not given a summary, so it is carried as it is: %v", t.requestID, t.ID, chatID, err)
	}
}

// withAttached adds open_attached_chat to what one model call offers,
// while the turn's chat carries attached chats.
func (r *Runner) withAttached(ctx context.Context, s *tenant.Store, t row, p seam.Provider, m models.Model, offered []seam.Tool) []seam.Tool {
	return append(offered, r.attachedTools(ctx, s, t, p, m, offered)...)
}

// attachedTools returns open_attached_chat for one model call, while the
// turn's chat carries attached chats, and keeps it on the turn beside the
// past-chat tools made for the call (pastchats.go), so the calls the model
// makes in reply run it. It is made for the least of the call's route, as
// they are, and runs through a registry of its own (runLookup), so each
// call is recorded, timed and checked by the approval rule. A name the call
// offers already is left alone.
func (r *Runner) attachedTools(ctx context.Context, s *tenant.Store, t row, p seam.Provider, m models.Model, offered []seam.Tool) []seam.Tool {
	tr := t.recalled
	if tr == nil || r.o.Tools == nil || !m.Has(models.CapTools) || !r.toolsOn(ctx, s) {
		return nil
	}
	for _, d := range offered {
		if d.Name == attach.ToolName {
			return nil
		}
	}
	carried := false
	for _, l := range r.attachedLinks(ctx, s, t) {
		if on, _ := attach.Incognito(ctx, s, l.AttachedID); !on {
			carried = true
		}
	}
	if !carried {
		return nil
	}
	o, _, err := r.attachOptions(ctx, s)
	if err != nil {
		r.logf("turns: %s turn %s: %s is not offered: %v", t.requestID, t.ID, attach.ToolName, err)
		return nil
	}
	if tr.chats == nil {
		tr.chats = map[string]tools.Tool{}
	}
	tr.chats[attach.ToolName] = r.openAttached(t, o, readsSensitive(p, m))
	if pols, _ := r.toolPolicies(ctx, s); pols[attach.ToolName] == approval.Never {
		return nil // kept, so a call made anyway is refused by its policy
	}
	return []seam.Tool{{Name: attach.ToolName, Description: attach.ToolDescription, InputSchema: attach.ToolSchema}}
}

// openAttached is open_attached_chat, made for one call. mayRead is the
// answer for the least of the call's route. It reads only a chat attached
// to the chat the call is in, and gives a chat that holds sensitive content
// only when mayRead is true, once the chat it goes into is held.
func (r *Runner) openAttached(t row, o attach.Options, mayRead bool) tools.Tool {
	return tools.Tool{
		Name: attach.ToolName, Description: attach.ToolDescription, InputSchema: attach.ToolSchema,
		ReadOnly: true,
		Inside:   true, // the person's own chats, with no tool's results: not outside content (P7-02, decision 0360)
		Run: func(ctx context.Context, in tools.Input) (string, error) {
			var args struct {
				ChatID string `json:"chat_id"`
				Page   *int   `json:"page"`
			}
			if err := json.Unmarshal(in.Args, &args); err != nil || (args.Page != nil && *args.Page < 1) {
				return "", fmt.Errorf(`%s takes a "chat_id", the id of a chat attached to this one, and a "page" of 1 or more`, attach.ToolName)
			}
			page := 1
			if args.Page != nil {
				page = *args.Page
			}
			if on, _ := attach.Incognito(ctx, in.Store, in.ChatID); on {
				return "", errors.New("this chat is incognito, so it carries no attached chat")
			}
			links, err := attach.List(ctx, in.Store, in.ChatID)
			if err != nil {
				return "", errors.New("the chats attached to this one could not be read")
			}
			var attached []string
			for _, l := range links {
				if on, _ := attach.Incognito(ctx, in.Store, l.AttachedID); !on {
					attached = append(attached, l.AttachedID)
				}
			}
			// The ids are named only as the call's context names them: a chat
			// the call may not be given is not named here either.
			id := strings.TrimSpace(args.ChatID)
			switch {
			case len(attached) == 0:
				return "", errors.New("no chat is attached to this one")
			case id == "" && len(attached) > 1:
				return "", fmt.Errorf("%d chats are attached to this one, so give chat_id, as your context gives each attached chat's id", len(attached))
			case id == "":
				id = attached[0]
			}
			if !containsID(attached, id) {
				return "", fmt.Errorf("the chat %q is not attached to this one. Give the id of an attached chat, as your context gives it", id)
			}
			// Fixed rule 3. A mark that cannot be read counts against giving.
			ans, _ := attach.MayShow(ctx, in.Store, id, mayRead)
			if !ans.Show {
				return "", errors.New("that chat holds content from a sensitive space, and a model this call may reach may not read it, so nothing of it is given here")
			}
			c, err := chat.Get(in.Store, id)
			if err != nil {
				return "", fmt.Errorf("the chat %s could not be read", id)
			}
			path, err := chat.Path(in.Store, id)
			if err != nil {
				return "", fmt.Errorf("the chat %s could not be read", id)
			}
			msgs := make([]seam.Message, 0, len(path))
			for _, m := range path {
				msgs = append(msgs, seam.Message{Role: m.Role, Blocks: m.Blocks})
			}
			text, err := attach.Page(id, c.Title, msgs, page, o)
			switch {
			case errors.Is(err, attach.ErrInput):
				return "", err // the model's own mistake, which it can mend
			case err != nil:
				return "", fmt.Errorf("the chat %s could not be read", id)
			}
			if ans.Sensitive {
				// Held whatever happens to the turn's context: the text is
				// given only once the hold is kept.
				if err := recall.Hold(context.Background(), in.Store, t.ID, t.ChatID); err != nil {
					r.logf("turns: %s turn %s: holding the chat for an attached chat's sensitive content: %v", t.requestID, t.ID, err)
					return "", errAttachedNotHeld
				}
			}
			return text, nil
		},
	}
}

func containsID(list []string, id string) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}
