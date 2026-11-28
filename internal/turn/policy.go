package turn

import (
	"context"
	"net/http"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
)

// The admin's router policy for each person (P14-05). Six tenant-admin
// settings make it: the ladder, the decision model and the step notes,
// which the router reads (P14-03); router.auto_default, which a new chat
// reads (P14-04); and the two here, which the turn and the chat routes
// enforce:
//
//   - router.allowed_models: the registry names the person may pick by
//     hand. Empty: every model. Auto uses the ladder whatever it says.
//   - router.may_switch_off: the person may take a chat off Auto. It holds
//     only while Auto is offered, that is while the ladder has a usable
//     step: with no ladder there is no Auto to stay on (decision 0720).
//
// The server enforces the policy and the screens only reflect it:
//
//   - A message that names a model while Auto is required is refused, 403
//     auto_required; one that names a model the person may not pick, 403
//     model_not_allowed. Both before anything is queued, and again when the
//     turn leaves the queue, where the turn fails with the same code
//     (decision 0721).
//   - A chat made, or a turn naming no model, while Auto is required is on
//     Auto (decision 0722).
//   - chats.default_model, and the chat's last model, outside the allowed
//     list are passed over (decision 0723).
//
// docs/architecture/router-policy.md describes it.

// The two settings of the admin's policy this file reads.
const (
	KeyMaySwitchOff  = "router.may_switch_off"
	KeyAllowedModels = "router.allowed_models"
)

// The refusals of the admin's policy: an answer's code, 403, and a failed
// turn's.
const (
	CodeModelNotAllowed = "model_not_allowed"
	CodeAutoRequired    = "auto_required"
)

// Policy is the admin's router policy for one person, as it stands.
type Policy struct {
	// Allowed are the registry names the person may pick by hand, from
	// router.allowed_models. Empty: every model.
	Allowed []string
	// MaySwitchOff is router.may_switch_off.
	MaySwitchOff bool
	// AutoOffered says the person's ladder has a usable step.
	AutoOffered bool

	labels map[string]string
}

// ParseAllowed reads router.allowed_models: registry names separated by
// commas. Blanks and repeats are dropped.
func ParseAllowed(v string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(v, ",") {
		name := strings.TrimSpace(part)
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// Allows says the person may pick the model by hand.
func (p Policy) Allows(name string) bool {
	if len(p.Allowed) == 0 {
		return true
	}
	for _, a := range p.Allowed {
		if a == name {
			return true
		}
	}
	return false
}

// AutoRequired says the person may not take a chat off Auto, and Auto is
// offered.
func (p Policy) AutoRequired() bool {
	return !p.MaySwitchOff && p.AutoOffered
}

// Refusal says why a message that names the model is refused, or nil when
// the policy lets it name it. An empty name names none and is never
// refused.
func (p Policy) Refusal(named string) *TurnError {
	named = strings.TrimSpace(named)
	switch {
	case named == "":
		return nil
	case p.AutoRequired():
		return &TurnError{Code: CodeAutoRequired, Message: "Your admin keeps your chats on Auto, so a model cannot be picked by hand. Send the message on Auto."}
	case !p.Allows(named):
		label := named
		if l := p.labels[named]; l != "" {
			label = l
		}
		other := "Pick another model."
		if p.AutoOffered {
			other = "Pick another model, or Auto."
		}
		return &TurnError{Code: CodeModelNotAllowed, Message: "Your admin has not allowed " + label + " to be picked by hand. " + other}
	}
	return nil
}

// PolicyFor reads a person's router policy: for unattended work (P14-06)
// and the admin's screens (P15-03), which read it as the turn does.
func (r *Runner) PolicyFor(ctx context.Context, s *tenant.Store) (Policy, error) {
	if err := r.setup(); err != nil {
		return Policy{}, err
	}
	test, err := testtenant.IsTest(s)
	if err != nil {
		return Policy{}, err
	}
	return r.policy(ctx, s, test)
}

// policy reads a person's router policy. A ladder that does not read
// offers no Auto, as the model choice says (withAuto).
func (r *Runner) policy(ctx context.Context, s *tenant.Store, test bool) (Policy, error) {
	allowed, err := tenantsettings.Get(ctx, s, r.reg, KeyAllowedModels)
	if err != nil {
		return Policy{}, err
	}
	may, err := tenantsettings.Get(ctx, s, r.reg, KeyMaySwitchOff)
	if err != nil {
		return Policy{}, err
	}
	p := Policy{Allowed: ParseAllowed(allowed), MaySwitchOff: strings.TrimSpace(may) != "false", labels: map[string]string{}}
	usable, err := r.autoUsable(ctx, s, test)
	if err != nil {
		return Policy{}, err
	}
	for _, m := range usable {
		p.labels[m.Name] = modelLabel(m)
	}
	if st, err := r.routerSettings(ctx, s); err == nil {
		p.AutoOffered = len(ladderSteps(st, usable)) > 0
	}
	return p, nil
}

// ladderSteps are the steps of the ladder that are usable chat models, as
// the model choice lists them.
func ladderSteps(st router.Settings, usable []models.Model) []LadderStep {
	byName := map[string]models.Model{}
	for _, m := range usable {
		if !m.Has(models.CapEmbedding) && !m.Has(models.CapDecision) {
			byName[m.Name] = m
		}
	}
	var out []LadderStep
	for i, rung := range st.Ladder {
		if m, ok := byName[rung.Name]; ok {
			out = append(out, LadderStep{Name: m.Name, Label: modelLabel(m), Class: string(m.Class), Step: i + 1})
		}
	}
	return out
}

// refusePick answers a message the admin's policy refuses, 403 with its
// code, before anything is queued. It returns false when it answered.
func (r *Runner) refusePick(w http.ResponseWriter, req *http.Request, c api.Call, body SendBody) bool {
	if strings.TrimSpace(body.Model) == "" {
		return true
	}
	p, err := r.PolicyFor(req.Context(), c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return false
	}
	if refused := p.Refusal(body.Model); refused != nil {
		api.Error(w, req, http.StatusForbidden, refused.Code, refused.Message)
		return false
	}
	return true
}

// policyAtStart holds a turn leaving the queue to the admin's policy as it
// stands then: a turn queued before the admin changed it, or sent by code
// (Runner.Send), is refused as the route refuses it; a turn naming no model
// while Auto is required puts its chat on Auto. A turn that has started
// (going on after an approval) and a stronger model, which stays on Auto,
// are left alone.
func (r *Runner) policyAtStart(ctx context.Context, s *tenant.Store, t *row, test bool) *TurnError {
	if t.Model != "" || t.strongerOf != "" {
		return nil
	}
	p, err := r.policy(ctx, s, test)
	if err != nil {
		r.logf("turns: %s turn %s: reading the admin's router policy: %v", t.requestID, t.ID, err)
		return &TurnError{Code: CodeFailed, Message: "The turn failed inside Keystone. The reason is in the server's log."}
	}
	if refused := p.Refusal(t.input.Model); refused != nil {
		return refused
	}
	if strings.TrimSpace(t.input.Model) == "" && !t.input.Auto && p.AutoRequired() {
		if err := SetChatAuto(ctx, s, t.ChatID, true); err != nil {
			r.logf("turns: %s turn %s: putting the chat on Auto, as the admin's policy asks: %v", t.requestID, t.ID, err)
			return &TurnError{Code: CodeFailed, Message: "The turn failed inside Keystone. The reason is in the server's log."}
		}
	}
	return nil
}

// autoIfRequired puts a chat just made on Auto when the person may not
// take a chat off it, whatever router.auto_default says.
func (r *Runner) autoIfRequired(ctx context.Context, s *tenant.Store, chatID string) error {
	p, err := r.PolicyFor(ctx, s)
	if err != nil || !p.AutoRequired() {
		return err
	}
	return SetChatAuto(ctx, s, chatID, true)
}

// withPolicy fills a chat's model choice with the admin's policy: the
// models the person may pick by hand, whether they may switch Auto off,
// Auto when it is required, and the model a turn naming none uses, chosen
// among the allowed ones only.
func (r *Runner) withPolicy(ctx context.Context, s *tenant.Store, chatID string, out ModelChoice) (ModelChoice, error) {
	out, err := r.withAuto(ctx, s, chatID, out)
	if err != nil {
		return ModelChoice{}, err
	}
	test, err := testtenant.IsTest(s)
	if err != nil {
		return ModelChoice{}, err
	}
	p, err := r.policy(ctx, s, test)
	if err != nil {
		return ModelChoice{}, err
	}
	out.Allowed = []string{}
	for _, m := range out.Models {
		if p.Allows(m.Name) {
			out.Allowed = append(out.Allowed, m.Name)
		}
	}
	// Auto is offered when the choice lists a ladder.
	out.MaySwitchOff = p.MaySwitchOff || len(out.Ladder) == 0
	if !out.MaySwitchOff {
		out.Auto = true
	}
	if out.Model != nil && !p.Allows(out.Model.Name) {
		out.Model, out.Source = nil, SourceNone
		pick := func(name, source string) bool {
			for _, m := range out.Models {
				if m.Name == name && p.Allows(name) {
					out.Model, out.Source = &m, source
					return true
				}
			}
			return false
		}
		if !pick(r.setting(ctx, s, KeyDefaultModel), SourceDefault) {
			for _, name := range out.Allowed {
				if pick(name, SourceFirst) {
					break
				}
			}
		}
		// A chat on Auto needs no model picked by hand, so it is told nothing.
		if out.Model == nil && !out.Auto {
			out.Reason = "Your admin allows none of the models in your registry to be picked by hand."
			if len(out.Ladder) > 0 {
				out.Reason += " Pick Auto."
			}
		}
	}
	return out, nil
}
