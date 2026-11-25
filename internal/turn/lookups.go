package turn

import (
	"context"
	"errors"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/approval"
)

// The knowledge lookups (P6-05) as tools of the tool loop (P7-01).
//
// They are not in the server's registry, because what a lookup may return
// depends on the call: a call is built for the least of the models its
// route may reach, and a lookup made for it must not return anything that
// least model may not read. So each call that offers them makes them for
// itself, keeps them on the turn, and the calls the model makes in reply
// run those same ones. They run through a registry of their own, so they
// are recorded, timed and checked by the approval rule as every tool is.

// What a model reads when a lookup could not be made.
var (
	errStoreClosed = errors.New("the knowledge store could not be opened, so nothing was looked up")
	errStoreFailed = errors.New("the knowledge store could not answer, so nothing was looked up")
	errNotHeld     = errors.New("the result came from a sensitive space, and the chat could not be marked as holding it, so it was not given")
)

// lookupTools returns the knowledge lookups one model call offers, and
// keeps them on the turn for the calls the model makes in reply. They are
// offered when the person's knowledge store opened for the turn, to a model
// that can use tools, unless the person has switched tools off. p and m are
// the call's provider and model, so the lookups are made for the least of
// the call's route. A name the call already offers is left out. Nothing
// here stops a turn: a lookup that cannot be made is left out and logged.
func (r *Runner) lookupTools(ctx context.Context, s *tenant.Store, t row, p seam.Provider, m models.Model, offered []seam.Tool) []seam.Tool {
	tr := t.recalled
	if tr == nil {
		return nil
	}
	tr.lookups = nil
	tr.chats = r.chatTools(ctx, s, t, p, m) // P5-05: search_chats and read_chat for the least of the call's route (pastchats.go)
	if !tr.open || !m.Has(models.CapTools) || !r.toolsOn(ctx, s) {
		return nil
	}
	list, err := r.knowledgeLookups(ctx, s, p, m)
	if err != nil {
		r.logf("turns: %s turn %s: the knowledge lookups could not be made, so none is offered: %v", t.requestID, t.ID, err)
		return nil
	}
	taken := map[string]bool{}
	for _, d := range offered {
		taken[d.Name] = true
	}
	pols, _ := r.toolPolicies(ctx, s) // a lookup set never is not offered (P7-02)
	tr.lookups = map[string]tools.Tool{}
	var defs []seam.Tool
	for _, l := range list {
		if taken[l.Name] {
			continue
		}
		tr.lookups[l.Name] = r.lookupTool(t, tr.adapter, l)
		if pols[l.Name] == approval.Never {
			continue // kept, so a call made anyway is refused by its policy
		}
		defs = append(defs, seam.Tool{Name: l.Name, Description: l.Description, InputSchema: l.Schema})
	}
	return defs
}

// lookupTool makes one lookup a tool. It opens the person's knowledge store
// for the one lookup. A result from a sensitive space holds the chat to
// models that may read it before the model sees the result, and is not
// given when the chat cannot be held.
func (r *Runner) lookupTool(t row, adapter string, l recall.Lookup) tools.Tool {
	return tools.Tool{
		Name: l.Name, Description: l.Description, InputSchema: l.Schema, ReadOnly: l.ReadOnly, Inside: lookupInside(l.Name),
		Run: func(ctx context.Context, in tools.Input) (string, error) {
			ks, err := knowledge.Open(ctx, adapter, in.Store)
			if err != nil {
				r.logf("turns: %s turn %s: %s: the knowledge store %s did not open: %v", t.requestID, t.ID, l.Name, adapter, err)
				return "", errStoreClosed
			}
			defer ks.Close()
			res, err := l.Run(ctx, in.Store, ks, in.Args)
			switch {
			case errors.Is(err, recall.ErrInput):
				return "", err // the model's own mistake, which it can mend
			case err != nil:
				if ctx.Err() == nil {
					r.logf("turns: %s turn %s: %s: %v", t.requestID, t.ID, l.Name, err)
				}
				return "", errStoreFailed
			}
			if res.Sensitive {
				// Held whatever happens to the turn's context: the text is
				// given only once the hold is kept.
				if err := recall.Hold(context.Background(), in.Store, t.ID, t.ChatID); err != nil {
					r.logf("turns: %s turn %s: %s: holding the chat for a sensitive result: %v", t.requestID, t.ID, l.Name, err)
					return "", errNotHeld
				}
			}
			return res.Text, nil
		},
	}
}

// lookup returns the knowledge lookup the current call offered under name.
func (t row) lookup(name string) (tools.Tool, bool) {
	if l, ok := t.chatTool(name); ok { // a past-chat tool made for the call's route (P5-05, pastchats.go)
		return l, true
	}
	if t.recalled == nil || t.recalled.lookups == nil {
		return tools.Tool{}, false
	}
	l, ok := t.recalled.lookups[name]
	return l, ok
}

// runLookup runs a knowledge lookup through a registry of its own, so it is
// recorded, timed and checked as every tool call is.
func runLookup(ctx context.Context, c tools.Call, l tools.Tool) tools.Result {
	reg := tools.New()
	if err := reg.Add(l); err != nil {
		return reg.Decline(c, "This lookup could not be run.")
	}
	return reg.Run(ctx, c)
}
