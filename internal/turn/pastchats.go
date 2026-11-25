package turn

import (
	"context"
	"errors"
	"strconv"

	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/builtin"
)

// The past-chat tools (P5-05) for each model call. search_chats and
// read_chat are in the server's registry, made for a call that may reach a
// model that may not read sensitive content, so they leave out every chat
// that was sent some (fixed rule 3). That is what the model is offered by
// name. Each call that offers tools also makes the two for the least of the
// models its route may reach, as the knowledge lookups are made
// (lookups.go), keeps them on the turn, and the calls the model makes in
// reply run those. So a call whose every model may read sensitive content
// is given a held chat too, and the chat it is in is held first.

// chatTools makes search_chats and read_chat for one model call, for the
// least of the models its route may reach, by name. p and m are the call's
// provider and model. A tool the registry does not hold is not made.
// Nothing here stops a turn: tools that cannot be made are logged, and the
// registry's, which give nothing sensitive, run instead.
func (r *Runner) chatTools(ctx context.Context, s *tenant.Store, t row, p seam.Provider, m models.Model) map[string]tools.Tool {
	if r.o.Tools == nil {
		return nil
	}
	limits, err := r.pastChatLimits(ctx, s)
	if err != nil {
		r.logf("turns: %s turn %s: the past-chat tools are not made for the call's route, so the registry's run: %v", t.requestID, t.ID, err)
		return nil
	}
	o := builtin.Options{Limits: r.o.Config.Limits, ReadChatMessages: r.o.Config.ToolLoop.ReadChatMessages, PastChats: limits}
	list, err := builtin.ChatTools(o, readsSensitive(p, m), func(_ context.Context, in tools.Input) error {
		// Held whatever happens to the turn's context: the text is given
		// only once the hold is kept.
		err := recall.Hold(context.Background(), in.Store, t.ID, t.ChatID)
		if err != nil {
			r.logf("turns: %s turn %s: holding the chat for a past chat's sensitive content: %v", t.requestID, t.ID, err)
		}
		return err
	})
	if err != nil {
		r.logf("turns: %s turn %s: the past-chat tools are not made for the call's route, so the registry's run: %v", t.requestID, t.ID, err)
		return nil
	}
	out := map[string]tools.Tool{}
	for _, tl := range list {
		if _, ok := r.o.Tools.Get(tl.Name); ok {
			out[tl.Name] = tl
		}
	}
	return out
}

// pastChatLimits reads the person's limits of the past-chat search.
func (r *Runner) pastChatLimits(ctx context.Context, s *tenant.Store) (builtin.PastChats, error) {
	results, err1 := strconv.Atoi(r.setting(ctx, s, builtin.SettingPastChatsResults))
	passages, err2 := strconv.Atoi(r.setting(ctx, s, builtin.SettingPastChatsPassages))
	if err := errors.Join(err1, err2); err != nil {
		return builtin.PastChats{}, err
	}
	p := builtin.PastChats{MaxResults: results, Passages: passages}
	return p, p.Check()
}

// chatTool returns the past-chat tool the current model call made under
// name.
func (t row) chatTool(name string) (tools.Tool, bool) {
	if t.recalled == nil || t.recalled.chats == nil {
		return tools.Tool{}, false
	}
	tl, ok := t.recalled.chats[name]
	return tl, ok
}
