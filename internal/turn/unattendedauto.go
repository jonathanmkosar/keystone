package turn

import (
	"context"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Auto for unattended work (P14-06). Sub-agents, background tasks and
// scheduled runs work through ordinary turns of a chat of their own, and a
// chat on Auto has each of its turns routed afresh by the router before its
// first call (auto.go). So unattended work on Auto is a chat it makes put
// on Auto, and turns that name no model: every round of a task, every run
// of a schedule and every sub-agent's turn is then routed as a person's
// message is, held chats, the cap and the run's budget included. Nothing
// here routes anything.
//
// The call that started the work decides: one that names a model keeps
// the chat off Auto, and its turns name that model, as before Auto.
// docs/architecture/unattended-auto.md describes it.

// UnattendedAuto reports whether unattended work that names no model
// starts on Auto for this tenant: router.auto_default is on and the
// person's ladder has a step. Settings that do not read count as off, so
// the work uses the model it would have used before Auto.
func (r *Runner) UnattendedAuto(ctx context.Context, s *tenant.Store) bool {
	if err := r.setup(); err != nil {
		r.logf("turns: tenant %s: setting up the runner, so unattended work is not put on Auto: %v", s.TenantID(), err)
		return false
	}
	if r.setting(ctx, s, KeyAutoDefault) != "true" {
		return false
	}
	st, err := r.routerSettings(ctx, s)
	if err != nil {
		r.logf("turns: tenant %s: the router's settings do not read, so unattended work is not put on Auto: %v", s.TenantID(), err)
		return false
	}
	return len(st.Ladder) > 0
}

// StartOnAuto is called by unattended work on the chat it has just made for
// its turns. named is the model the call that started the work named, or
// "". When it is "" and UnattendedAuto is true, the chat is put on Auto and
// StartOnAuto reports true: the work's turns then name no model, so each is
// routed afresh. Otherwise the chat is left as it was made and it reports
// false: the work names the model it always has.
func (r *Runner) StartOnAuto(ctx context.Context, s *tenant.Store, chatID, named string) (bool, error) {
	if strings.TrimSpace(named) != "" || !r.UnattendedAuto(ctx, s) {
		return false, nil
	}
	if err := SetChatAuto(ctx, s, chatID, true); err != nil {
		return false, err
	}
	return true, nil
}
