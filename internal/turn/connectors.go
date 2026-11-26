package turn

import (
	"context"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
)

// The connectors' part of a turn (P7-04). The connectors themselves, what a
// call is offered from them and the sessions with their servers are in
// internal/connectors. What is here is the turn's side, made like the
// knowledge lookups (lookups.go): each model call asks for the tools of the
// person's connectors that are on for the chat, keeps them on the turn, and
// the calls the model makes in reply run those same ones, through a
// registry of their own, so they are recorded, timed and checked by the
// approval rule as every tool is. docs/architecture/connectors.md is the
// whole of it.

// Connectors gives turns the tools of each person's connectors. The server
// gives the runner internal/connectors' Pool. Nil means no connectors.
type Connectors interface {
	// ForCall returns what one model call of a chat is offered from the
	// person's connectors, and every tool the model's calls may name. The
	// messages are the chat's branch, from which it reads the tools a call
	// of load_tools has loaded; nil means none. taken says a name the call
	// already offers.
	ForCall(ctx context.Context, s *tenant.Store, chatID string, messages []seam.Message, taken func(name string) bool) ([]seam.Tool, map[string]tools.Tool)
	// Known is every connector tool the person can set a policy for, from
	// what their connectors last listed.
	Known(ctx context.Context, s *tenant.Store) []tools.Info
}

// turnConnectors is what the current model call of a turn was given from
// the person's connectors.
type turnConnectors struct {
	run map[string]tools.Tool
}

// connectorTools returns what one model call offers from the person's
// connectors, and keeps on the turn every tool its calls may name. Nothing
// is offered to a model that cannot use tools, when tools are switched off,
// or when the server has no connectors. A name the call already offers, the
// server's tools and the knowledge lookups keep theirs. A tool set never is
// not offered, and a call made to it anyway is refused by its policy.
func (r *Runner) connectorTools(ctx context.Context, s *tenant.Store, t row, m models.Model, messages []seam.Message, offered []seam.Tool) []seam.Tool {
	tc := t.connectors
	if tc == nil {
		return nil
	}
	tc.run = nil
	if r.o.Connectors == nil || !m.Has(models.CapTools) || !r.toolsOn(ctx, s) {
		return nil
	}
	taken := map[string]bool{}
	for _, d := range offered {
		taken[d.Name] = true
	}
	offer, run := r.o.Connectors.ForCall(ctx, s, t.ChatID, messages, func(name string) bool {
		if taken[name] {
			return true
		}
		if _, ok := t.lookup(name); ok {
			return true
		}
		_, ok := r.o.Tools.Get(name)
		return ok
	})
	tc.run = run
	return r.withoutNever(ctx, s, offer)
}

// withConnectors is offered with what the call offers from the person's
// connectors.
func (r *Runner) withConnectors(ctx context.Context, s *tenant.Store, t row, m models.Model, messages []seam.Message, offered []seam.Tool) []seam.Tool {
	return append(offered, r.connectorTools(ctx, s, t, m, messages, offered)...)
}

// connectorTool returns the connector tool the current call offered under
// name.
func (t row) connectorTool(name string) (tools.Tool, bool) {
	if t.connectors == nil || t.connectors.run == nil {
		return tools.Tool{}, false
	}
	tool, ok := t.connectors.run[name]
	return tool, ok
}

// serverHas says the server's own registry has a tool of that name, which
// keeps it.
func (r *Runner) serverHas(name string) bool {
	_, ok := r.o.Tools.Get(name)
	return ok
}

// knownConnectorTools is every connector tool of the person's, for the
// approvals screen.
func (r *Runner) knownConnectorTools(ctx context.Context, s *tenant.Store) []ToolPolicy {
	if r.o.Connectors == nil || s == nil {
		return nil
	}
	var out []ToolPolicy
	for _, t := range r.o.Connectors.Known(ctx, s) {
		if _, ok := r.o.Tools.Get(t.Name); ok {
			continue // the server's own tool keeps the name
		}
		out = append(out, ToolPolicy{Name: t.Name, Description: t.Description, ReadOnly: t.ReadOnly, Inside: t.Inside, ReachesOthers: t.ReachesOthers})
	}
	return out
}

// closeConnectors closes the connectors' sessions when the runner closes.
func (r *Runner) closeConnectors() {
	if c, ok := r.o.Connectors.(interface{ Close() }); ok && c != nil {
		c.Close()
	}
}
