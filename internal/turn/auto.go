package turn

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/decision"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
)

// Auto in the turn (P14-04). A chat is on Auto, where the router
// (internal/router) chooses the model and the effort for each message, or
// on the model picked. What is here:
//
//   - Every message of a chat on Auto is routed afresh, before its first
//     call, from that message and its call's facts. Nothing from an earlier
//     message holds the chat on a step.
//   - The choice holds for the message's whole turn: every tool step uses
//     the same model and effort, since a thinking block goes back only to
//     the model that wrote it. A turn resumed after an approval reads its
//     route back from its row and is never routed twice.
//   - The step's model is the ask's model for routing.Plan, as a picked
//     model is, so the privacy filter, the capability filter and the
//     fallback chain work as they always have. The router only narrows.
//   - When the chosen local step does not answer before any of its reply
//     is passed on, the runner notes the time (Facts.LocalFailedAt) and
//     routes the message again: the notice local_unavailable, or for a held
//     chat with no other step the code local_unavailable.
//   - Each routing is a row of router_verdicts, and the turn's row keeps
//     the step, the effort, the reason, the notice and the verdict's id.
//   - "Try a stronger model" writes a turn on Auto again, on a new branch,
//     one step above the step that answered (autoroutes.go).
//
// docs/architecture/router.md, "For the turn", and turns.md describe it.

// KeyAutoDefault is the admin's setting that starts a new chat on Auto.
const KeyAutoDefault = "router.auto_default"

// EventRoute is the stream event that says how a turn was routed.
const EventRoute = "route"

// The codes of a turn on Auto, beside the router's sensitive and no_model.
const (
	// CodeLocalUnavailable: the local machine is not answering and no
	// other step may take the message. A held chat waits for the machine.
	CodeLocalUnavailable = router.CodeLocalUnavailable
	// CodeNoLadder: the person has no ladder (a stronger model asked for).
	CodeNoLadder = router.CodeNoLadder
	// CodeNoStronger: no step above the one that answered may take it.
	CodeNoStronger = router.CodeNoStronger
	// CodeNotAuto: a stronger model was asked for a turn not on Auto.
	CodeNotAuto = "not_auto"
)

// NoticeLocalUnavailable is the notice a reply carries when local steps
// were passed over because the local machine was not answering.
const NoticeLocalUnavailable = router.NoticeLocalUnavailable

// The test tenant's recorded ladder: three steps and a decision model,
// played by the fake provider like its one model and in no registry. Its
// ladder ships empty, so nothing it does changes until router.ladder and
// router.decision_model name them for it.
const (
	TestSmallModel    = "recorded-small"
	TestMiddleModel   = "recorded-middle"
	TestLargeModel    = "recorded-large"
	TestDecisionModel = "recorded-decisions"
)

// TurnRoute is how one turn was routed: the route event's data, and the
// route of a turn and of its replies.
type TurnRoute struct {
	// Auto says the router chose the model. Always true today: a turn on
	// the model picked has no route and sends no route event (decision
	// 0696).
	Auto bool `json:"auto"`
	// Model is the registry name of the step's model, and Label what the
	// picker shows for it.
	Model string `json:"model"`
	Label string `json:"label,omitempty"`
	// Step is the step's place on the ladder, from 1, smallest first, of
	// Steps.
	Step  int `json:"step,omitempty"`
	Steps int `json:"steps,omitempty"`
	// Effort is low, medium, high, xhigh or max: what every call of the
	// turn asked for.
	Effort string `json:"effort,omitempty"`
	// Reason is one line, written by Keystone and never by a model.
	Reason string `json:"reason,omitempty"`
	// Notice is local_unavailable when local steps were passed over
	// because the local machine was not answering.
	Notice string `json:"notice,omitempty"`
	// Switched says the step is not the previous message's, so the call
	// lost that provider's prompt cache.
	Switched bool `json:"switched,omitempty"`
	// VerdictID is the turn's row in router_verdicts, which the turn
	// inspector shows.
	VerdictID string `json:"verdict_id,omitempty"`
	// StrongerOf is the turn this one wrote again one step up.
	StrongerOf string `json:"stronger_of,omitempty"`
}

// LadderStep is one step of the person's ladder, as the picker shows it.
type LadderStep struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// Class is local, hosted or trusted.
	Class string `json:"class"`
	// Step is its place on the ladder, from 1, smallest first.
	Step int `json:"step"`
}

// Verdict is one row of router_verdicts: how one message on Auto was
// routed. The decision model was shown Shown and the message's text, which
// is in the chat and not here.
type Verdict struct {
	ID        string `json:"id"`
	TurnID    string `json:"turn_id"`
	RequestID string `json:"request_id"`
	// DecisionModel is the decision model asked, or empty.
	DecisionModel string       `json:"decision_model,omitempty"`
	Shown         router.Shown `json:"shown"`
	// StepAnswered, Probability and EffortAnswered are the verdict's.
	// Empty with none.
	StepAnswered   string  `json:"step_answered,omitempty"`
	Probability    float64 `json:"probability,omitempty"`
	EffortAnswered string  `json:"effort_answered,omitempty"`
	// Step and Effort are what was chosen.
	Step   string `json:"step"`
	Effort string `json:"effort"`
	// MovedUp: the verdict was unsure and the step moved one up.
	MovedUp  bool `json:"moved_up"`
	TimedOut bool `json:"timed_out"`
	// RulesOnly and RulesOnlyWhy: no verdict decided, and why
	// (one_candidate, no_decision_model, decision_model_unusable, held,
	// local_unavailable, timeout, error, stronger_asked).
	RulesOnly    bool   `json:"rules_only"`
	RulesOnlyWhy string `json:"rules_only_why,omitempty"`
	// LatencyMS is how long the decision call took.
	LatencyMS int64 `json:"latency_ms"`
	// LocalUnavailable: this routing's decision call found the local
	// machine not answering.
	LocalUnavailable bool `json:"local_unavailable"`
	// StrongerAsked: the person asked for this turn to be written again by
	// a stronger model.
	StrongerAsked bool      `json:"stronger_asked"`
	Reason        string    `json:"reason"`
	Notice        string    `json:"notice,omitempty"`
	Created       time.Time `json:"created"`
}

// turnRoute is the route of the turn being worked.
type turnRoute struct {
	TurnRoute
	// in is what the router was given, for routing again when the local
	// step does not answer. routed says it was given this run: a route
	// read back from the turn's row has none.
	in       router.Input
	routed   bool
	rerouted bool
}

// effort is the effort every call of the turn asks for: empty off Auto.
func (tr *turnRoute) effort() seam.Effort {
	if tr == nil || !tr.Auto {
		return ""
	}
	return seam.Effort(tr.Effort)
}

// shown is the route a reply of the turn carries, or nil off Auto.
func (tr *turnRoute) shown() *TurnRoute {
	if tr == nil || !tr.Auto {
		return nil
	}
	c := tr.TurnRoute
	return &c
}

// routeColumns are the route's columns of the turns table, which
// turnColumns ends with.
const routeColumns = `auto, ladder_step, ladder_steps, effort, route_label, route_reason, route_notice, route_switched, verdict_id, stronger_of`

// routeCells are the route's columns as they are scanned.
type routeCells struct {
	auto, switched                                     bool
	step, steps                                        int
	effort, label, reason, notice, verdict, strongerOf string
}

func (c *routeCells) targets() []any {
	return []any{&c.auto, &c.step, &c.steps, &c.effort, &c.label, &c.reason, &c.notice, &c.switched, &c.verdict, &c.strongerOf}
}

// route is the turn's route, or nil for a turn not on Auto or not routed.
func (c routeCells) route(model string) *TurnRoute {
	if !c.auto || c.step == 0 {
		return nil
	}
	return &TurnRoute{Auto: true, Model: model, Label: c.label, Step: c.step, Steps: c.steps, Effort: c.effort,
		Reason: c.reason, Notice: c.notice, Switched: c.switched, VerdictID: c.verdict, StrongerOf: c.strongerOf}
}

// localDowns keeps, for each tenant, when a call to a local endpoint, a
// decision call included, last failed with unavailable: the router's
// Facts.LocalFailedAt. A restart forgets it, which costs one message the
// wait.
type localDowns struct {
	mu sync.Mutex
	at map[string]time.Time
}

func (r *Runner) localFailedAt(tenantID string) time.Time {
	r.downs.mu.Lock()
	defer r.downs.mu.Unlock()
	return r.downs.at[tenantID]
}

func (r *Runner) markLocalDown(tenantID string, at time.Time) {
	r.downs.mu.Lock()
	defer r.downs.mu.Unlock()
	if r.downs.at == nil {
		r.downs.at = map[string]time.Time{}
	}
	if at.After(r.downs.at[tenantID]) {
		r.downs.at[tenantID] = at
	}
}

// noteLocalDown notes a call to a local model that failed as unavailable.
func (r *Runner) noteLocalDown(s *tenant.Store, m models.Model, kind seam.ErrorKind) {
	if m.Class == models.ClassLocal && kind == seam.ErrUnavailable {
		r.markLocalDown(s.TenantID(), time.Now())
	}
}

// noteLocalCall notes a chat call whose last model was local and did not
// answer.
func (r *Runner) noteLocalCall(s *tenant.Store, out called) {
	if out.err != nil && out.failure != nil && out.failure.Code == string(seam.ErrUnavailable) {
		r.noteLocalDown(s, out.model, seam.ErrUnavailable)
	}
}

// ChatAuto reports whether a chat is on Auto. chat.ErrNotFound when the
// chat is not in this tenant's record.
func ChatAuto(ctx context.Context, s *tenant.Store, chatID string) (bool, error) {
	var on bool
	err := s.DB().QueryRowContext(ctx, "SELECT auto FROM chats WHERE id = ?", chatID).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return false, chat.ErrNotFound
	}
	return on, err
}

// SetChatAuto puts a chat on Auto or takes it off. It is what a message
// sent with auto, or with a model named, does; unattended work (P14-06)
// sets it for the chats it makes.
func SetChatAuto(ctx context.Context, s *tenant.Store, chatID string, on bool) error {
	res, err := s.DB().ExecContext(ctx, "UPDATE chats SET auto = ? WHERE id = ?", on, chatID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return chat.ErrNotFound
	}
	return err
}

// autoForNew puts a chat just made on Auto when router.auto_default says so.
func (r *Runner) autoForNew(ctx context.Context, s *tenant.Store, chatID string) error {
	if err := r.setup(); err != nil {
		return err
	}
	return SetChatAuto(ctx, s, chatID, r.setting(ctx, s, KeyAutoDefault) == "true")
}

// routerSettings reads the router's settings in force for a tenant.
func (r *Runner) routerSettings(ctx context.Context, s *tenant.Store) (router.Settings, error) {
	return router.Load(func(key string) (string, bool) {
		v, err := tenantsettings.Get(ctx, s, r.reg, key)
		return v, err == nil
	})
}

// testUsable are the test tenant's models: its one model, the three steps
// of its recorded ladder and its recorded decision model.
func (r *Runner) testUsable() []models.Model {
	base := r.testModel()
	step := func(name, label string, class models.Class) models.Model {
		m := base
		m.Name, m.Label, m.Class = name, label, class
		m.Capabilities = append([]models.Capability(nil), base.Capabilities...)
		return m
	}
	return []models.Model{
		base,
		step(TestSmallModel, "Recorded turns, small (test data)", models.ClassLocal),
		step(TestMiddleModel, "Recorded turns, middle (test data)", models.ClassTrusted),
		step(TestLargeModel, "Recorded turns, large (test data)", models.ClassHosted),
		{Name: TestDecisionModel, Label: "Recorded verdicts (test data)", Adapter: models.AdapterFake, Class: models.ClassLocal,
			ContextWindow: base.ContextWindow, OutputLimit: base.OutputLimit, Capabilities: []models.Capability{models.CapDecision}},
	}
}

// autoUsable are the models the router may choose among and ask: the
// person's usable models whose adapter this build has, decision models
// included.
func (r *Runner) autoUsable(ctx context.Context, s *tenant.Store, test bool) ([]models.Model, error) {
	if test {
		return r.testUsable(), nil
	}
	all, err := models.Usable(ctx, s)
	if err != nil {
		return nil, err
	}
	var out []models.Model
	for _, m := range all {
		if r.set[m.Adapter] != nil {
			out = append(out, m)
		}
	}
	return out, nil
}

func modelLabel(m models.Model) string {
	if strings.TrimSpace(m.Label) != "" {
		return m.Label
	}
	return m.Name
}

// withAuto fills a chat's model choice with what Auto adds to it.
func (r *Runner) withAuto(ctx context.Context, s *tenant.Store, chatID string, out ModelChoice) (ModelChoice, error) {
	out.Ladder, out.Allowed, out.MaySwitchOff = []LadderStep{}, []string{}, true
	for _, m := range out.Models {
		out.Allowed = append(out.Allowed, m.Name)
	}
	held, err := recall.Held(ctx, s, chatID)
	if err != nil {
		return ModelChoice{}, err
	}
	out.Held = held
	on, err := ChatAuto(ctx, s, chatID)
	if err != nil {
		return ModelChoice{}, err
	}
	test, err := testtenant.IsTest(s)
	if err != nil {
		return ModelChoice{}, err
	}
	usable, err := r.autoUsable(ctx, s, test)
	if err != nil {
		return ModelChoice{}, err
	}
	st, err := r.routerSettings(ctx, s)
	if err != nil {
		r.logf("turns: tenant %s: the router's settings do not read, so Auto is not offered: %v", s.TenantID(), err)
		return out, nil
	}
	byName := map[string]models.Model{}
	for _, m := range usable {
		if !m.Has(models.CapEmbedding) && !m.Has(models.CapDecision) {
			byName[m.Name] = m
		}
	}
	for i, rung := range st.Ladder {
		if m, ok := byName[rung.Name]; ok {
			out.Ladder = append(out.Ladder, LadderStep{Name: m.Name, Label: modelLabel(m), Class: string(m.Class), Step: i + 1})
		}
	}
	out.Auto = on && len(out.Ladder) > 0
	err = s.DB().QueryRowContext(ctx, "SELECT model FROM turns WHERE chat_id = ? AND auto = 1 AND model <> '' ORDER BY seq DESC LIMIT 1", chatID).Scan(&out.LastStep)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return out, err
}

// openTurn returns the model a turn runs on and its provider, as open
// does, and routes a turn of a chat on Auto first. Every turn's stream
// gets a route event here.
func (r *Runner) openTurn(ctx context.Context, s *tenant.Store, t *row, say func(string, any)) (models.Model, seam.Provider, []string, *TurnError) {
	broke := func(what string, err error) (models.Model, seam.Provider, []string, *TurnError) {
		r.logf("turns: %s turn %s: %s: %v", t.requestID, t.ID, what, err)
		return models.Model{}, nil, nil, &TurnError{Code: CodeFailed, Message: "The turn failed inside Keystone. The reason is in the server's log."}
	}
	if err := r.setup(); err != nil {
		return broke("setting up the runner", err)
	}
	test, err := testtenant.IsTest(s)
	if err != nil {
		return broke("reading the test tenant's mark", err)
	}
	// P14-05: the admin's policy as it stands now (policy.go).
	if failure := r.policyAtStart(ctx, s, t, test); failure != nil {
		return models.Model{}, nil, nil, failure
	}
	// The person's pick for this message: auto puts the chat on Auto, a
	// named model takes it off.
	if t.input.Auto || strings.TrimSpace(t.input.Model) != "" {
		if err := SetChatAuto(ctx, s, t.ChatID, t.input.Auto); err != nil {
			return broke("keeping the chat's pick", err)
		}
	}
	// A turn routed before, going on after an approval, keeps its route.
	if t.Route != nil && t.Model != "" {
		t.route = &turnRoute{TurnRoute: *t.Route}
		return r.openStep(ctx, s, *t, test, t.Model)
	}
	if t.strongerOf != "" {
		return r.openStronger(ctx, s, t, test, say)
	}
	on, err := ChatAuto(ctx, s, t.ChatID)
	if err != nil {
		return broke("reading whether the chat is on Auto", err)
	}
	var st router.Settings
	if on {
		if st, err = r.routerSettings(ctx, s); err != nil {
			r.logf("turns: %s turn %s: the router's settings do not read, so the chat uses the model picked: %v", t.requestID, t.ID, err)
			on = false
		}
	}
	if !on || len(st.Ladder) == 0 {
		// A turn on the model picked sends no route event: its turn event
		// names its model, as before Auto (decision 0696).
		return r.open(ctx, s, *t, t.input.Model)
	}
	in, err := r.autoInput(ctx, s, *t, test, st)
	if err != nil {
		return broke("reading the facts of the message", err)
	}
	res, err := router.Route(ctx, in)
	return r.takeRoute(ctx, s, t, test, in, res, err, say)
}

// reroute routes a turn's message again after its local step did not
// answer: local steps are skipped and the reply carries the notice.
func (r *Runner) reroute(ctx context.Context, s *tenant.Store, t *row, say func(string, any)) (models.Model, seam.Provider, []string, *TurnError) {
	now := time.Now()
	r.markLocalDown(s.TenantID(), now)
	in := t.route.in
	in.Facts.LocalFailedAt, in.Facts.Now = now, now
	test, err := testtenant.IsTest(s)
	if err != nil {
		r.logf("turns: %s turn %s: reading the test tenant's mark: %v", t.requestID, t.ID, err)
		return models.Model{}, nil, nil, &TurnError{Code: CodeFailed, Message: "The turn failed inside Keystone. The reason is in the server's log."}
	}
	t.route.rerouted = true
	res, err := router.Route(ctx, in)
	return r.takeRoute(ctx, s, t, test, in, res, err, say)
}

// mayReroute says a turn's first call failed because its local step did
// not answer, before any of its reply was passed on, and the message has
// not been routed again yet.
func (r *Runner) mayReroute(t row, out called) bool {
	tr := t.route
	return tr != nil && tr.Auto && tr.routed && !tr.rerouted && tr.StrongerOf == "" &&
		out.err != nil && !out.stopped && !out.closing && out.failure != nil &&
		out.failure.Code == string(seam.ErrUnavailable) && out.model.Class == models.ClassLocal &&
		len(out.partial.Blocks) == 0
}

// openStronger routes a turn that writes another again one step up.
func (r *Runner) openStronger(ctx context.Context, s *tenant.Store, t *row, test bool, say func(string, any)) (models.Model, seam.Provider, []string, *TurnError) {
	of, err := getTurn(ctx, s, t.ChatID, t.strongerOf)
	if err != nil || of.Route == nil {
		return models.Model{}, nil, nil, &TurnError{Code: CodeNotAuto, Message: "Only a reply written on Auto can be written again by a stronger model."}
	}
	in, err := r.strongerInput(ctx, s, *t, test)
	if err != nil {
		r.logf("turns: %s turn %s: reading the facts of the message: %v", t.requestID, t.ID, err)
		return models.Model{}, nil, nil, &TurnError{Code: CodeFailed, Message: "The turn failed inside Keystone. The reason is in the server's log."}
	}
	res, err := router.Stronger(in, of.Model, seam.Effort(of.Route.Effort))
	return r.takeRoute(ctx, s, t, test, in, res, err, say)
}

// strongerInput is the router's input for a stronger model: the person's
// settings, the facts of the message and the usable models.
func (r *Runner) strongerInput(ctx context.Context, s *tenant.Store, t row, test bool) (router.Input, error) {
	st, err := r.routerSettings(ctx, s)
	if err != nil {
		return router.Input{}, err
	}
	return r.autoInput(ctx, s, t, test, st)
}

// takeRoute opens the step the router chose, keeps the verdict and the
// route, and sends the route event. A refusal is the turn's error, with
// the router's code.
func (r *Runner) takeRoute(ctx context.Context, s *tenant.Store, t *row, test bool, in router.Input, res router.Result, err error, say func(string, any)) (models.Model, seam.Provider, []string, *TurnError) {
	none := func(failure *TurnError) (models.Model, seam.Provider, []string, *TurnError) {
		return models.Model{}, nil, nil, failure
	}
	broke := func(what string, err error) (models.Model, seam.Provider, []string, *TurnError) {
		r.logf("turns: %s turn %s: %s: %v", t.requestID, t.ID, what, err)
		return none(&TurnError{Code: CodeFailed, Message: "The turn failed inside Keystone. The reason is in the server's log."})
	}
	if res.Record.LocalUnavailable {
		r.markLocalDown(s.TenantID(), time.Now())
	}
	if err != nil {
		var refused *router.Refusal
		switch {
		case ctx.Err() != nil:
			return none(&TurnError{Code: CodeFailed, Message: "The turn was stopped while its model was chosen."})
		case errors.As(err, &refused):
			return none(&TurnError{Code: refused.Code, Message: refused.Message, Retryable: refused.Code == CodeLocalUnavailable})
		}
		return broke("routing the message", err)
	}
	m, p, secrets, failure := r.openStep(ctx, s, *t, test, res.Model.Name)
	if failure != nil {
		return none(failure)
	}
	tr := &turnRoute{
		TurnRoute: TurnRoute{Auto: true, Model: res.Model.Name, Label: modelLabel(res.Model), Step: res.Step, Steps: res.Steps,
			Effort: string(res.Effort), Reason: res.Reason, Notice: res.Notice, Switched: res.Switched, StrongerOf: t.strongerOf},
		in: in, routed: true, rerouted: t.route != nil && t.route.rerouted,
	}
	id, err := saveVerdict(s, *t, res, tr.TurnRoute)
	if err != nil {
		return broke("keeping the verdict", err)
	}
	tr.VerdictID = id
	if _, err := s.DB().Exec(`UPDATE turns SET auto = 1, ladder_step = ?, ladder_steps = ?, effort = ?, route_label = ?, route_reason = ?,
		route_notice = ?, route_switched = ?, verdict_id = ? WHERE id = ?`,
		tr.Step, tr.Steps, tr.Effort, tr.Label, tr.Reason, tr.Notice, tr.Switched, tr.VerdictID, t.ID); err != nil {
		return broke("keeping the route", err)
	}
	t.route = tr
	say(EventRoute, tr.TurnRoute)
	return m, p, secrets, nil
}

// openStep opens a step's model as the ask's model: the person's route,
// or for the test tenant its recorded models behind the same privacy
// filter.
func (r *Runner) openStep(ctx context.Context, s *tenant.Store, t row, test bool, name string) (models.Model, seam.Provider, []string, *TurnError) {
	if !test {
		return r.routedChat(ctx, s, t, name)
	}
	var chatModels []models.Model
	var picked models.Model
	for _, m := range r.testUsable() {
		if m.Has(models.CapDecision) {
			continue
		}
		chatModels = append(chatModels, m)
		if m.Name == name {
			picked = m
		}
	}
	if picked.Name == "" {
		return models.Model{}, nil, nil, &TurnError{Code: CodeNoModel, Message: "There is no recorded model named " + name + "."}
	}
	// Fixed rule 3, as routed applies it: the call goes only to models
	// that may read sensitive content when it carries some.
	sensitive := r.sensitive(ctx, s, t, picked.MayReadSensitive())
	route, err := routing.Plan(routing.Rules{}, routing.Ask{Kind: routing.KindChat, Model: name, Sensitive: sensitive}, chatModels)
	if err != nil {
		if errors.Is(err, routing.ErrSensitive) {
			return models.Model{}, nil, nil, &TurnError{Code: CodeSensitive, Message: "This chat holds content from a sensitive space, and the step chosen is not local or trusted, so it was not sent."}
		}
		return models.Model{}, nil, nil, &TurnError{Code: CodeNoModel, Message: "No recorded model can take this call."}
	}
	var links []routing.Link
	for _, m := range route.Models {
		links = append(links, routing.Link{Model: m, Provider: budget.Guard(m, r.test, r.o.Config.Budget)})
	}
	chain, err := routing.NewChain(sensitive, links, routing.Hooks{})
	if err != nil {
		return models.Model{}, nil, nil, &TurnError{Code: CodeNoModel, Message: "No recorded model can take this call."}
	}
	return links[0].Model, chain, nil, nil
}

// autoInput is what the router routes a turn's message from.
func (r *Runner) autoInput(ctx context.Context, s *tenant.Store, t row, test bool, st router.Settings) (router.Input, error) {
	usable, err := r.autoUsable(ctx, s, test)
	if err != nil {
		return router.Input{}, err
	}
	msgs, err := r.routingBranch(ctx, s, t)
	if err != nil {
		return router.Input{}, err
	}
	// A need no step of the ladder can meet is not a need: the call goes
	// as it would to a picked model that lacks it, the image or the file
	// left out with a notice and no tools offered (decision 0692).
	ladderCan := func(c models.Capability) bool {
		for _, rung := range st.Ladder {
			for _, m := range usable {
				if m.Name == rung.Name && m.Has(c) {
					return true
				}
			}
		}
		return false
	}
	files := false
	for _, m := range msgs {
		for _, b := range m.Blocks {
			files = files || b.Type == seam.BlockFile
		}
	}
	tools := 0
	if ladderCan(models.CapTools) {
		tools = len(r.offered(ctx, s, models.Model{Capabilities: []models.Capability{models.CapTools}}))
	}
	images := t.media != nil && (t.media.current || t.media.earlier)
	f := router.Facts{
		Text:          newestText(s, t),
		Tokens:        budget.Estimated(seam.Request{Messages: msgs}, r.o.Config.Budget).Tokens,
		ReplyTokens:   r.number(ctx, s, KeyMaxOutputTokens),
		Images:        images && ladderCan(models.CapVision),
		Files:         files && ladderCan(models.CapFiles),
		Tools:         tools,
		Held:          r.sensitive(ctx, s, t, false),
		LocalFailedAt: r.localFailedAt(s.TenantID()),
		Now:           time.Now(),
	}
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM turns WHERE chat_id = ? AND id <> ?", t.ChatID, t.ID).Scan(&f.Turns); err != nil {
		return router.Input{}, err
	}
	err = s.DB().QueryRowContext(ctx, "SELECT model FROM turns WHERE chat_id = ? AND id <> ? AND model <> '' ORDER BY seq DESC LIMIT 1", t.ChatID, t.ID).Scan(&f.PreviousStep)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return router.Input{}, err
	}
	canDecide := func(m models.Model) bool { return r.set[m.Adapter] != nil && adapters.Decides(m.Adapter) }
	if test {
		canDecide = func(m models.Model) bool { return m.Adapter == models.AdapterFake }
	}
	return router.Input{Settings: st, Facts: f, Usable: usable, Verdict: r.verdict(s, t, test), CanDecide: canDecide}, nil
}

// routingBranch is the branch a turn's first call will be built from, with
// the turn's own message, before it is written: for the size estimate and
// for what the call holds.
func (r *Runner) routingBranch(ctx context.Context, s *tenant.Store, t row) ([]seam.Message, error) {
	path, err := chat.Path(s, t.ChatID)
	if err != nil {
		return nil, err
	}
	cut := func(at int64, keep bool) []chat.Message {
		for i, m := range path {
			if m.ID == at {
				if keep {
					return path[:i+1]
				}
				return path[:i]
			}
		}
		return nil
	}
	in := t.input
	switch {
	case in.RetryOf != 0:
		old, err := chat.GetMessage(s, t.ChatID, in.RetryOf)
		if err != nil {
			return nil, err
		}
		path = cut(old.ParentID, true)
	case in.EditOf != 0:
		path = cut(in.EditOf, false)
	}
	msgs := buildMessages(path, map[int64]meta{}, "")
	if in.RetryOf == 0 {
		msgs = append(msgs, seam.Message{Role: seam.RoleUser, Blocks: in.content()})
	}
	return msgs, nil
}

// verdict is the decision call the router makes: a recorded call through
// decision.Run, with its audit entries and its usage record of the kind
// decision. The test tenant's decision model is its recorded verdicts.
func (r *Runner) verdict(s *tenant.Store, t row, test bool) router.Verdict {
	return func(ctx context.Context, m models.Model, sensitive bool, req seam.DecisionRequest) (seam.DecisionResult, error) {
		call := decision.Call{RequestID: t.requestID, Actor: t.actor, Sensitive: sensitive, Request: req}
		if test {
			d, ok := r.test.(seam.Decider)
			if !ok {
				return seam.DecisionResult{}, errors.New("the test tenant's provider answers no decision calls")
			}
			return decision.Run(ctx, s, decision.Opened{Model: m, Decider: d}, call, decision.Options{})
		}
		om, d, err := adapters.OpenDecider(ctx, s, r.set, m.Name)
		if err != nil {
			return seam.DecisionResult{}, err
		}
		var secrets []string
		if om.Key != "" {
			if v, ok, err := s.Secrets().Get(om.Key); err == nil && ok && v != "" {
				secrets = append(secrets, v)
			}
		}
		gate := r.capGate(s)
		return decision.Run(ctx, s, decision.Opened{Model: om, Decider: d, Secrets: secrets}, call, decision.Options{
			// The monthly cap, for a decision model that is not local.
			Gate: func(ctx context.Context, m models.Model, req seam.DecisionRequest) *seam.Error {
				return gate(ctx, m, seam.Request{Model: m.Name, MaxOutputTokens: 16,
					Messages: []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: req.State}}}}})
			},
		})
	}
}

// saveVerdict writes one routing as a row of router_verdicts.
func saveVerdict(s *tenant.Store, t row, res router.Result, tr TurnRoute) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	rec := res.Record
	shown, err := json.Marshal(rec.Shown)
	if err != nil {
		return "", err
	}
	_, err = s.DB().Exec(`INSERT INTO router_verdicts (id, turn_id, request_id, decision_model, shown, step_answered, probability,
		effort_answered, step, effort, moved_up, timed_out, rules_only, rules_only_why, latency_ms, local_unavailable, reason, notice, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, t.ID, t.requestID, rec.DecisionModel, string(shown), rec.StepAnswered, rec.Probability, string(rec.EffortAnswered),
		rec.Step, string(rec.Effort), rec.MovedUp, rec.TimedOut, rec.RulesOnly, rec.RulesOnlyWhy, rec.LatencyMS, rec.LocalUnavailable,
		tr.Reason, tr.Notice, stamp())
	return id, err
}

// markStronger sets stronger_asked on a verdict.
func markStronger(s *tenant.Store, verdictID string) error {
	_, err := s.DB().Exec("UPDATE router_verdicts SET stronger_asked = 1 WHERE id = ?", verdictID)
	return err
}

const verdictColumns = `id, turn_id, request_id, decision_model, shown, step_answered, probability, effort_answered, step, effort,
	moved_up, timed_out, rules_only, rules_only_why, latency_ms, local_unavailable, stronger_asked, reason, notice, created_at`

func scanVerdict(sc interface{ Scan(...any) error }) (Verdict, error) {
	var v Verdict
	var shown, created string
	if err := sc.Scan(&v.ID, &v.TurnID, &v.RequestID, &v.DecisionModel, &shown, &v.StepAnswered, &v.Probability, &v.EffortAnswered,
		&v.Step, &v.Effort, &v.MovedUp, &v.TimedOut, &v.RulesOnly, &v.RulesOnlyWhy, &v.LatencyMS, &v.LocalUnavailable,
		&v.StrongerAsked, &v.Reason, &v.Notice, &created); err != nil {
		return Verdict{}, err
	}
	if err := json.Unmarshal([]byte(shown), &v.Shown); err != nil {
		return Verdict{}, fmt.Errorf("verdict %s: its facts do not read: %w", v.ID, err)
	}
	at, err := time.Parse(timeLayout, created)
	if err != nil {
		return Verdict{}, fmt.Errorf("verdict %s: %w", v.ID, err)
	}
	v.Created = at
	return v, nil
}

// turnVerdict is the verdict of a turn on Auto, for the inspector, or nil.
func turnVerdict(ctx context.Context, s *tenant.Store, t row) (*Verdict, error) {
	if t.Route == nil || t.Route.VerdictID == "" {
		return nil, nil
	}
	v, err := scanVerdict(s.DB().QueryRowContext(ctx, "SELECT "+verdictColumns+" FROM router_verdicts WHERE id = ?", t.Route.VerdictID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// VerdictFilter narrows Verdicts. A field left empty does not narrow.
type VerdictFilter struct {
	// TurnID keeps one turn's verdicts.
	TurnID string
	// From and To keep the verdicts written at From or after, and before To.
	From, To time.Time
}

// Verdicts returns a tenant's router verdicts, oldest first: every routing
// of a message on Auto, a second one when the local machine failed under
// the first step, and the routing of a stronger model. Auto in usage
// (P14-07) sums them.
func Verdicts(ctx context.Context, s *tenant.Store, f VerdictFilter) ([]Verdict, error) {
	q := "SELECT " + verdictColumns + " FROM router_verdicts WHERE 1 = 1"
	var args []any
	if f.TurnID != "" {
		q += " AND turn_id = ?"
		args = append(args, f.TurnID)
	}
	if !f.From.IsZero() {
		q += " AND created_at >= ?"
		args = append(args, f.From.UTC().Format(timeLayout))
	}
	if !f.To.IsZero() {
		q += " AND created_at < ?"
		args = append(args, f.To.UTC().Format(timeLayout))
	}
	rows, err := s.DB().QueryContext(ctx, q+" ORDER BY created_at, rowid", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Verdict
	for rows.Next() {
		v, err := scanVerdict(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// withRoutes gives each reply of a turn on Auto its route, for the chat's
// messages as metaOf reads them.
func withRoutes(ctx context.Context, s *tenant.Store, chatID string, metas map[int64]meta) error {
	rows, err := s.DB().QueryContext(ctx, "SELECT id, model, "+routeColumns+" FROM turns WHERE chat_id = ? AND auto = 1", chatID)
	if err != nil {
		return err
	}
	defer rows.Close()
	routes := map[string]*TurnRoute{}
	for rows.Next() {
		var id, model string
		var rc routeCells
		if err := rows.Scan(append([]any{&id, &model}, rc.targets()...)...); err != nil {
			return err
		}
		if route := rc.route(model); route != nil {
			routes[id] = route
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id, m := range metas {
		if route := routes[m.turnID]; route != nil && m.model != "" {
			m.route = route
			metas[id] = m
		}
	}
	return nil
}
