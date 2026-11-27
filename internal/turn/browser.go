package turn

import (
	"context"
	"strconv"

	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools/browser"
)

// Browser use (P10-03): the browser's tools are offered only to a person who
// has switched agents.browser_use on, and it ships off. A call the model
// makes to one anyway is refused without asking the person (approveStep),
// and the tool itself reads the same switch. docs/architecture/browser-use.md
// is the whole of it.

// browserOn reports whether the person has switched browser use on. A
// switch that cannot be read is off.
func (r *Runner) browserOn(ctx context.Context, s *tenant.Store) bool {
	on, err := strconv.ParseBool(r.setting(ctx, s, browser.KeyEnabled))
	return err == nil && on
}

// withoutBrowser leaves the browser's tools out of a call's tools unless the
// person has switched browser use on. With it off a call offers what it
// offered before browser use was built.
func (r *Runner) withoutBrowser(ctx context.Context, s *tenant.Store, defs []seam.Tool) []seam.Tool {
	if r.browserOn(ctx, s) {
		return defs
	}
	var out []seam.Tool
	for _, d := range defs {
		if !browser.IsTool(d.Name) {
			out = append(out, d)
		}
	}
	return out
}
