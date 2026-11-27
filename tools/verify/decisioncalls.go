package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/decision"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// Decision calls (P14-02), a case of the "sensitive content" check: a
// decision call whose state may hold sensitive content, as the router's
// verdict for a held chat does, never reaches a hosted decision model, and
// every decision call has its audit entries and its usage record.
//
// An invented person has three decision models on the systemone adapter,
// one local, one hosted and one trusted, each answered by the fake
// provider's recorded decisions (fixtures/fake-provider/decisions.json) so
// nothing leaves this machine. Every decision each model is sent is kept.
//
//  1. A sensitive decision named for the hosted model: decision.Choose
//     refuses it with routing's sensitive error.
//  2. The same, sent straight to the opened hosted model: decision.Run
//     refuses it, records it as refused, and the model is never called.
//  3. Sensitive decisions to the local and the trusted model are answered
//     with each option's probability.
//  4. A plain decision to the hosted model is answered, so the check would
//     notice a guard that refused everything.
//  5. Each call has a started entry, an end entry and a usage record of the
//     kind decision; the hosted model received no state with the marker.

// The parts of decision calls and their docs, and the adapter's place on
// the offline check's list.
func init() {
	componentDocs["internal/decision"] = "docs/architecture/decision-calls.md"
	componentDocs["internal/adapters/systemone"] = "docs/architecture/adapter-systemone.md"
	networkAllowed["internal/adapters/systemone"] = "calls the decision endpoint a registry entry names, Ollama's /v1/systemone; its tests call servers on this machine only"
}

// decisionMarker is an invented word that stands for sensitive content.
const decisionMarker = "quillfeather"

var (
	decideLocal = models.Model{
		Name: "check-house-decide", Label: "House decisions", Adapter: models.AdapterSystemone, Endpoint: "http://127.0.0.1:11434",
		ProviderModel: "example-decide:9b", ContextWindow: 8192, OutputLimit: 1, Capabilities: []models.Capability{models.CapDecision}, Class: models.ClassLocal,
	}
	decideHosted = models.Model{
		Name: "check-cloud-decide", Label: "Cloud decisions", Adapter: models.AdapterSystemone, Endpoint: "https://decide.example",
		ProviderModel: "example-decide", ContextWindow: 8192, OutputLimit: 1, Capabilities: []models.Capability{models.CapDecision}, Class: models.ClassHosted,
	}
	decideTrusted = models.Model{
		Name: "check-friend-decide", Label: "Trusted decisions", Adapter: models.AdapterSystemone, Endpoint: "https://trusted-decide.example",
		ProviderModel: "example-decide", ContextWindow: 8192, OutputLimit: 1, Capabilities: []models.Capability{models.CapDecision}, Class: models.ClassTrusted,
	}
)

// decisionEars keeps the state of every decision each model is sent.
type decisionEars struct {
	mu    sync.Mutex
	f     *fake.Provider
	heard map[string][]string
}

type heardDecider struct {
	e    *decisionEars
	name string
}

func (h heardDecider) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	return h.e.f.Stream(ctx, req, emit)
}

func (h heardDecider) Decide(ctx context.Context, req seam.DecisionRequest) (seam.DecisionResult, error) {
	h.e.mu.Lock()
	h.e.heard[h.name] = append(h.e.heard[h.name], req.State)
	h.e.mu.Unlock()
	return h.e.f.Decide(ctx, req)
}

func decisionCallProblems(root string) ([]string, error) {
	ctx := context.Background()
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	scripts, err := fake.Load(os.DirFS(filepath.Join(root, "fixtures", "fake-provider")))
	if err != nil {
		return nil, err
	}
	f, err := fake.New(scripts, fake.Options{})
	if err != nil {
		return nil, err
	}
	e := &decisionEars{f: f, heard: map[string][]string{}}
	set := adapters.Set{models.AdapterSystemone: func(m models.Model, _ string) (seam.Provider, error) { return heardDecider{e, m.Name}, nil }}

	dir, err := os.MkdirTemp("", "keystone-decisions-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	person, err := in.Create("Decision Check Person")
	if err != nil {
		return nil, err
	}
	s, err := in.Store(person.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	lim, err := adapters.Limits(reg)
	if err != nil {
		return nil, err
	}
	for _, m := range []models.Model{decideLocal, decideHosted, decideTrusted} {
		if err := models.Add(ctx, s, lim, m); err != nil {
			return nil, err
		}
	}
	usable, err := models.Usable(ctx, s)
	if err != nil {
		return nil, err
	}
	can := func(m models.Model) bool { return set[m.Adapter] != nil && adapters.Decides(m.Adapter) }
	verdict := func(state string) seam.DecisionRequest {
		return seam.DecisionRequest{State: state, Questions: []seam.DecisionQuestion{
			{ID: "step", Instructions: "Which is the smallest model that answers this request well?", Type: seam.QuestionChoice, Options: []seam.DecisionOption{
				{ID: "house-chat"}, {ID: "small-hosted"}, {ID: "mid-hosted"},
			}},
			{ID: "effort", Instructions: "How much thinking does this request need?", Type: seam.QuestionChoice, Options: []seam.DecisionOption{
				{ID: "low"}, {ID: "medium"}, {ID: "high"},
			}},
		}}
	}
	held := "Newest message: what day is it, and is the " + decisionMarker + " letter in the box?"
	open := func(name string) (decision.Opened, error) {
		m, d, err := adapters.OpenDecider(ctx, s, set, name)
		return decision.Opened{Model: m, Decider: d}, err
	}
	run := func(id string, o decision.Opened, sensitive bool, state string) (seam.DecisionResult, error) {
		return decision.Run(ctx, s, o, decision.Call{RequestID: id, Actor: "decision-check", Sensitive: sensitive, Request: verdict(state)}, decision.Options{})
	}

	// 1. Chosen for a sensitive call: refused.
	if _, err := decision.Choose(decideHosted.Name, true, usable, can); !errors.Is(err, routing.ErrSensitive) {
		say("decision calls: a sensitive decision named for the hosted decision model was not refused by routing: %v", err)
	}
	// 2. Sent straight to it: refused, recorded, never called.
	hosted, err := open(decideHosted.Name)
	if err != nil {
		return nil, err
	}
	if _, err := run("decision-held-hosted", hosted, true, held); err == nil {
		say("decision calls: a sensitive decision sent straight to the hosted decision model was answered")
	}
	// 3. Sensitive to the local and the trusted model: answered.
	for i, name := range []string{decideLocal.Name, decideTrusted.Name} {
		m, err := decision.Choose(name, true, usable, can)
		if err != nil {
			say("decision calls: a sensitive decision to %s was refused: %v", name, err)
			continue
		}
		o, err := open(m.Name)
		if err != nil {
			return nil, err
		}
		res, err := run(fmt.Sprintf("decision-held-%d", i), o, true, held)
		step, _ := res.Answer("step")
		if err != nil || step.Choice != "house-chat" || len(step.Probabilities) != 3 {
			say("decision calls: the sensitive decision to %s gave %+v, %v; want each option's probability", name, res, err)
		}
	}
	// 4. A plain decision to the hosted model is answered.
	if res, err := run("decision-plain-hosted", hosted, false, "Newest message: what day is it?"); err != nil || len(res.Answers) != 2 {
		say("decision calls: a plain decision to the hosted decision model gave %+v, %v", res, err)
	}

	// 5. The records, and what the hosted model heard.
	e.mu.Lock()
	for _, state := range e.heard[decideHosted.Name] {
		if strings.Contains(strings.ToLower(state), decisionMarker) {
			say("decision calls: the hosted decision model was sent sensitive state")
		}
	}
	if len(e.heard[decideHosted.Name]) != 1 {
		say("decision calls: the hosted decision model was called %d times, want once, for the plain decision", len(e.heard[decideHosted.Name]))
	}
	e.mu.Unlock()
	for _, id := range []string{"decision-held-hosted", "decision-held-0", "decision-held-1", "decision-plain-hosted"} {
		entries, err := audit.List(ctx, s, audit.Filter{RequestID: id})
		if err != nil {
			return nil, err
		}
		if len(entries) != 2 || entries[0].Outcome != audit.OutcomeStarted || entries[1].Outcome == audit.OutcomeStarted {
			say("decision calls: %s has %d audit entries, want a started entry and an end entry", id, len(entries))
		}
		recs, err := usage.List(ctx, s, usage.Filter{RequestID: id})
		if err != nil {
			return nil, err
		}
		if len(recs) != 1 || recs[0].Kind != decision.Kind {
			say("decision calls: %s has %d usage records, want one of the kind decision", id, len(recs))
		}
		if id == "decision-held-hosted" && len(recs) == 1 && recs[0].Outcome != usage.OutcomeRefused {
			say("decision calls: the sensitive decision to the hosted model is recorded as %s, want refused", recs[0].Outcome)
		}
	}
	return problems, nil
}
