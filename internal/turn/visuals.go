package turn

import (
	"context"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
)

// Inline visuals (P9-04): a reply may hold a chart, as a fenced chart
// block, and a diagram, as a fenced mermaid block, and the chat screen
// draws them (docs/architecture/inline-visuals.md). A model only writes
// them when it knows the two formats, so the standing instructions say
// how, in the words of a setting.
//
// The words are the same in every call, and they are put straight after
// the standing text and before anything that may differ from one call to
// the next, so the start of a call stays one a provider can keep
// (decision 0542).
const (
	// KeyVisualsDraw is the setting that says whether a reply's charts and
	// diagrams are drawn. Off, the model is told nothing about them.
	KeyVisualsDraw = "visuals.draw"
	// KeyVisualsInstructions is the setting that holds what the model is
	// told. Empty tells it nothing.
	KeyVisualsInstructions = "visuals.instructions"
)

// withVisuals is the standing instructions followed by how to write a
// chart or a diagram in a reply, when the person's settings draw them.
func (r *Runner) withVisuals(ctx context.Context, s *tenant.Store, system string) string {
	on, err := tenantsettings.Get(ctx, s, r.reg, KeyVisualsDraw)
	if err != nil {
		return system
	}
	if draw, err := strconv.ParseBool(on); err != nil || !draw {
		return system
	}
	words, err := tenantsettings.Get(ctx, s, r.reg, KeyVisualsInstructions)
	if err != nil {
		return system
	}
	return withRule(system, strings.TrimSpace(words))
}
