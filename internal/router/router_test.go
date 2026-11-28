package router

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// An invented person's registry: a local chat model, a trusted one, three
// hosted ones, two decision models and an embedding model.
var (
	localChat = models.Model{Name: "local-chat", Label: "Local model", Adapter: models.AdapterChatCompletions, Class: models.ClassLocal,
		ContextWindow: 32768, OutputLimit: 4096, Capabilities: []models.Capability{models.CapTools, models.CapThinking}}
	trustedChat = models.Model{Name: "trusted-chat", Label: "Trusted model", Adapter: models.AdapterChatCompletions, Class: models.ClassTrusted,
		ContextWindow: 65536, OutputLimit: 8192, Capabilities: []models.Capability{models.CapTools, models.CapVision}}
	smallHosted = models.Model{Name: "small-hosted", Label: "Small hosted", Adapter: models.AdapterAnthropic, Class: models.ClassHosted,
		ContextWindow: 200000, OutputLimit: 8192, Capabilities: []models.Capability{models.CapTools, models.CapVision, models.CapFiles, models.CapThinking}}
	midHosted = models.Model{Name: "mid-hosted", Label: "Mid hosted", Adapter: models.AdapterAnthropic, Class: models.ClassHosted,
		ContextWindow: 200000, OutputLimit: 16384, Capabilities: []models.Capability{models.CapTools, models.CapVision, models.CapFiles, models.CapThinking}}
	largeHosted = models.Model{Name: "large-hosted", Label: "Large hosted", Adapter: models.AdapterAnthropic, Class: models.ClassHosted,
		ContextWindow: 1000000, OutputLimit: 32768, Capabilities: []models.Capability{models.CapTools, models.CapVision, models.CapFiles, models.CapThinking}}
	houseDecide = models.Model{Name: "house-decide", Label: "House decisions", Adapter: models.AdapterSystemone, Class: models.ClassLocal,
		ContextWindow: 8192, OutputLimit: 1, Capabilities: []models.Capability{models.CapDecision}}
	cloudDecide = models.Model{Name: "cloud-decide", Label: "Cloud decisions", Adapter: models.AdapterSystemone, Class: models.ClassHosted,
		ContextWindow: 8192, OutputLimit: 1, Capabilities: []models.Capability{models.CapDecision}}
	embedder = models.Model{Name: "house-embed", Label: "House embeddings", Adapter: models.AdapterChatCompletions, Class: models.ClassLocal,
		ContextWindow: 8192, OutputLimit: 1, Capabilities: []models.Capability{models.CapEmbedding}}

	usable = []models.Model{localChat, trustedChat, smallHosted, midHosted, largeHosted, houseDecide, cloudDecide, embedder}
	now    = time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
)

// base is the spec's defaults with the owner's example ladder.
func base() Settings {
	return Settings{
		Ladder:          []Rung{{Name: "local-chat"}, {Name: "small-hosted", Limit: 100000}, {Name: "mid-hosted"}, {Name: "large-hosted"}},
		DecisionModel:   "house-decide",
		MinConfidence:   0.6,
		VerdictTimeout:  2 * time.Second,
		VerdictMaxChars: 8000,
		DefaultEffort:   seam.EffortMedium,
		MaxEffort:       seam.EffortHigh,
		LocalDownFor:    2 * time.Minute,
	}
}

func names(cands []Candidate) []string {
	var out []string
	for _, c := range cands {
		out = append(out, c.Model.Name)
	}
	return out
}

// answer is a verdict for req: the step chosen with probability p, the rest
// shared among the other steps, and the effort at 0.8.
func answer(req seam.DecisionRequest, step string, p float64, effort string) seam.DecisionResult {
	res := seam.DecisionResult{Model: req.Model}
	for _, q := range req.Questions {
		choice, pc := step, p
		if q.ID == QuestionEffort {
			choice, pc = effort, 0.8
		}
		ids := seam.OptionIDs(q)
		probs := map[string]float64{}
		for _, id := range ids {
			if id == choice {
				probs[id] = pc
			} else {
				probs[id] = (1 - pc) / float64(len(ids)-1)
			}
		}
		res.Answers = append(res.Answers, seam.DecisionAnswer{Question: q.ID, Choice: choice, Probabilities: probs})
	}
	return res
}

// verdicts counts the decision calls and keeps what each was sent.
type verdicts struct {
	mu        sync.Mutex
	calls     int
	models    []string
	sensitive []bool
	sent      []seam.DecisionRequest
	answer    func(ctx context.Context, req seam.DecisionRequest) (seam.DecisionResult, error)
}

func (v *verdicts) fn(ctx context.Context, m models.Model, sensitive bool, req seam.DecisionRequest) (seam.DecisionResult, error) {
	v.mu.Lock()
	v.calls++
	v.models = append(v.models, m.Name)
	v.sensitive = append(v.sensitive, sensitive)
	v.sent = append(v.sent, req)
	v.mu.Unlock()
	return v.answer(ctx, req)
}

func says(step string, p float64, effort string) *verdicts {
	return &verdicts{answer: func(_ context.Context, req seam.DecisionRequest) (seam.DecisionResult, error) {
		return answer(req, step, p, effort), nil
	}}
}

func TestParseLadder(t *testing.T) {
	cases := []struct {
		in   string
		want []Rung
		err  string
	}{
		{in: "", want: nil},
		{in: " , ,", want: nil},
		{in: "local-chat, small-hosted<=100000, mid-hosted, large-hosted",
			want: []Rung{{Name: "local-chat"}, {Name: "small-hosted", Limit: 100000}, {Name: "mid-hosted"}, {Name: "large-hosted"}}},
		{in: "a <= 5 ,b", want: []Rung{{Name: "a", Limit: 5}, {Name: "b"}}},
		{in: "a<=0", err: "size limit"},
		{in: "a<=-3", err: "size limit"},
		{in: "a<=lots", err: "size limit"},
		{in: "<=100", err: "no model name"},
		{in: "a, b, a<=10", err: "twice"},
	}
	for _, c := range cases {
		got, err := ParseLadder(c.in)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("ParseLadder(%q) = %v, %v; want an error with %q", c.in, got, err, c.err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseLadder(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
}

func TestParseStepNotes(t *testing.T) {
	got, err := ParseStepNotes(" local-chat = everyday questions ; mid-hosted=long documents and code;")
	want := map[string]string{"local-chat": "everyday questions", "mid-hosted": "long documents and code"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("ParseStepNotes = %v, %v; want %v", got, err, want)
	}
	for _, bad := range []string{"local-chat", "=words", "local-chat=", "a=x; a=y"} {
		if _, err := ParseStepNotes(bad); err == nil {
			t.Errorf("ParseStepNotes(%q) was not refused", bad)
		}
	}
}

func TestLoadAndCheck(t *testing.T) {
	values := map[string]string{
		KeyLadder: "local-chat, small-hosted<=100000", KeyDecisionModel: "house-decide", KeyStepNotes: "local-chat=everyday",
		KeyMinConfidence: "0.6", KeyVerdictTimeout: "2s", KeyVerdictMaxChars: "8000",
		KeyDefaultEffort: "medium", KeyMaxEffort: "high", KeyLocalDownFor: "2m",
	}
	get := func(key string) (string, bool) { v, ok := values[key]; return v, ok }
	s, err := Load(get)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Ladder) != 2 || s.Ladder[1].Limit != 100000 || s.DecisionModel != "house-decide" || s.StepNotes["local-chat"] != "everyday" ||
		s.MinConfidence != 0.6 || s.VerdictTimeout != 2*time.Second || s.VerdictMaxChars != 8000 ||
		s.DefaultEffort != seam.EffortMedium || s.MaxEffort != seam.EffortHigh || s.LocalDownFor != 2*time.Minute {
		t.Errorf("Load = %+v", s)
	}
	for key, bad := range map[string]string{
		KeyLadder: "a<=x", KeyStepNotes: "nothing", KeyMinConfidence: "1.5", KeyVerdictTimeout: "0s",
		KeyVerdictMaxChars: "0", KeyDefaultEffort: "huge", KeyMaxEffort: "", KeyLocalDownFor: "-1m",
	} {
		was := values[key]
		values[key] = bad
		if _, err := Load(get); err == nil {
			t.Errorf("Load took %s = %q", key, bad)
		}
		values[key] = was
	}
	delete(values, KeyMaxEffort)
	if _, err := Load(get); err == nil || !strings.Contains(err.Error(), KeyMaxEffort) {
		t.Errorf("Load with no %s: %v", KeyMaxEffort, err)
	}
}

// Each rule, in order, on the owner's ladder.
func TestRules(t *testing.T) {
	cases := []struct {
		name    string
		ladder  []Rung
		facts   Facts
		want    []string
		skipped []Skip
	}{
		{name: "every step", facts: Facts{Tokens: 500}, want: []string{"local-chat", "small-hosted", "mid-hosted", "large-hosted"}},
		{name: "rule 1: a held chat keeps local and trusted steps",
			ladder: []Rung{{Name: "local-chat"}, {Name: "trusted-chat"}, {Name: "small-hosted"}, {Name: "large-hosted"}},
			facts:  Facts{Tokens: 500, Held: true}, want: []string{"local-chat", "trusted-chat"},
			skipped: []Skip{{"small-hosted", SkipSensitive}, {"large-hosted", SkipSensitive}}},
		{name: "rule 2: an image asks for vision", facts: Facts{Tokens: 500, Images: true},
			want: []string{"small-hosted", "mid-hosted", "large-hosted"}, skipped: []Skip{{"local-chat", SkipCapability}}},
		{name: "rule 2: a file asks for files",
			ladder: []Rung{{Name: "local-chat"}, {Name: "trusted-chat"}, {Name: "mid-hosted"}},
			facts:  Facts{Tokens: 500, Files: true}, want: []string{"mid-hosted"},
			skipped: []Skip{{"local-chat", SkipCapability}, {"trusted-chat", SkipCapability}}},
		{name: "rule 2: offered tools ask for tools", ladder: []Rung{{Name: "local-chat"}, {Name: "house-embed"}, {Name: "house-decide"}},
			facts: Facts{Tokens: 500, Tools: 3}, want: []string{"local-chat"},
			skipped: []Skip{{"house-embed", SkipCapability}, {"house-decide", SkipCapability}}},
		{name: "rule 3: over the size limit", facts: Facts{Tokens: 100001},
			want: []string{"mid-hosted", "large-hosted"}, skipped: []Skip{{"local-chat", SkipWindow}, {"small-hosted", SkipSize}}},
		{name: "rule 3: at the size limit", facts: Facts{Tokens: 100000},
			want: []string{"small-hosted", "mid-hosted", "large-hosted"}, skipped: []Skip{{"local-chat", SkipWindow}}},
		{name: "rule 3: the window counts the reply's room", facts: Facts{Tokens: 32768 - 4096},
			want: []string{"local-chat", "small-hosted", "mid-hosted", "large-hosted"}},
		{name: "rule 3: one token over the window", facts: Facts{Tokens: 32768 - 4096 + 1},
			want: []string{"small-hosted", "mid-hosted", "large-hosted"}, skipped: []Skip{{"local-chat", SkipWindow}}},
		{name: "rule 3: a smaller reply asked for", facts: Facts{Tokens: 32000, ReplyTokens: 768},
			want: []string{"local-chat", "small-hosted", "mid-hosted", "large-hosted"}},
		{name: "rule 4: the local machine is not answering", facts: Facts{Tokens: 500, LocalFailedAt: now.Add(-time.Minute), Now: now},
			want: []string{"small-hosted", "mid-hosted", "large-hosted"}, skipped: []Skip{{"local-chat", SkipLocalDown}}},
		{name: "rule 4: the failure is older than local_down_for", facts: Facts{Tokens: 500, LocalFailedAt: now.Add(-2 * time.Minute), Now: now},
			want: []string{"local-chat", "small-hosted", "mid-hosted", "large-hosted"}},
		{name: "rule 4: trusted is not local", ladder: []Rung{{Name: "local-chat"}, {Name: "trusted-chat"}},
			facts: Facts{Tokens: 500, Held: true, LocalFailedAt: now, Now: now}, want: []string{"trusted-chat"},
			skipped: []Skip{{"local-chat", SkipLocalDown}}},
		{name: "a name not in the registry", ladder: []Rung{{Name: "gone"}, {Name: "mid-hosted"}},
			facts: Facts{Tokens: 500}, want: []string{"mid-hosted"}, skipped: []Skip{{"gone", SkipUnknown}}},
		{name: "rule 1 before rule 3", ladder: []Rung{{Name: "small-hosted", Limit: 10}, {Name: "local-chat"}},
			facts: Facts{Tokens: 500, Held: true}, want: []string{"local-chat"}, skipped: []Skip{{"small-hosted", SkipSensitive}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := base()
			if c.ladder != nil {
				s.Ladder = c.ladder
			}
			cands, skipped := Candidates(s, c.facts, usable)
			if got := names(cands); !reflect.DeepEqual(got, c.want) {
				t.Errorf("candidates %v, want %v", got, c.want)
			}
			if !reflect.DeepEqual(skipped, c.skipped) {
				t.Errorf("skipped %v, want %v", skipped, c.skipped)
			}
			for _, cand := range cands {
				if s.Ladder[cand.Step-1].Name != cand.Model.Name {
					t.Errorf("candidate %s says it is step %d", cand.Model.Name, cand.Step)
				}
			}
		})
	}
}

// The router only narrows what routing.Plan allows: every candidate is a
// model the plan routes a chat call to, with the same ask.
func TestCandidatesAreWhatThePlanAllows(t *testing.T) {
	ladder := []Rung{}
	for _, m := range usable {
		ladder = append(ladder, Rung{Name: m.Name})
	}
	ladder = append(ladder, Rung{Name: "not-registered"})
	for _, held := range []bool{false, true} {
		for _, images := range []bool{false, true} {
			s := base()
			s.Ladder = ladder
			f := Facts{Tokens: 100, Held: held, Images: images}
			cands, _ := Candidates(s, f, usable)
			for _, c := range cands {
				route, err := routing.Plan(routing.Rules{}, routing.Ask{Kind: routing.KindChat, Model: c.Model.Name, Sensitive: held, Needs: f.Needs()}, usable)
				if err != nil || route.Models[0].Name != c.Model.Name {
					t.Errorf("held %v, images %v: %s is a candidate and the plan gives %v, %v", held, images, c.Model.Name, route.Models, err)
				}
			}
		}
	}
}

func TestRefusals(t *testing.T) {
	cases := []struct {
		name   string
		ladder []Rung
		facts  Facts
		code   string
	}{
		{name: "no ladder", ladder: []Rung{}, facts: Facts{}, code: CodeNoLadder},
		{name: "a held chat with only hosted steps", ladder: []Rung{{Name: "small-hosted"}, {Name: "large-hosted"}}, facts: Facts{Held: true}, code: CodeSensitive},
		{name: "a held chat whose local step is down", ladder: []Rung{{Name: "local-chat"}, {Name: "small-hosted"}},
			facts: Facts{Held: true, LocalFailedAt: now, Now: now}, code: CodeLocalUnavailable},
		{name: "only a local step, down", ladder: []Rung{{Name: "local-chat"}}, facts: Facts{LocalFailedAt: now, Now: now}, code: CodeLocalUnavailable},
		{name: "too large for every step", facts: Facts{Tokens: 5000000}, code: CodeNoModel},
		{name: "nothing registered", ladder: []Rung{{Name: "gone"}}, code: CodeNoModel},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := base()
			if c.ladder != nil {
				s.Ladder = c.ladder
			}
			v := says("small-hosted", 0.99, "high")
			_, err := Route(context.Background(), Input{Settings: s, Facts: c.facts, Usable: usable, Verdict: v.fn})
			var r *Refusal
			if !errors.As(err, &r) || r.Code != c.code {
				t.Fatalf("Route: %v, want the code %s", err, c.code)
			}
			if errors.Is(err, routing.ErrSensitive) != (c.code == CodeSensitive) {
				t.Errorf("errors.Is(routing.ErrSensitive) is wrong for %s", c.code)
			}
			if v.calls != 0 {
				t.Errorf("a refused message asked for %d verdicts", v.calls)
			}
		})
	}
}

// The verdict: the step it names, the confidence step, the effort cap and
// the reason lines.
func TestVerdict(t *testing.T) {
	cases := []struct {
		name       string
		settings   func(*Settings)
		facts      Facts
		step       string
		p          float64
		effort     string
		wantModel  string
		wantEffort seam.Effort
		wantReason string
		movedUp    bool
	}{
		{name: "the local step, sure", step: "local-chat", p: 0.91, effort: "low",
			wantModel: "local-chat", wantEffort: seam.EffortLow, wantReason: "Local model, effort low, decision 0.91"},
		{name: "a hosted step", step: "mid-hosted", p: 0.7, effort: "high",
			wantModel: "mid-hosted", wantEffort: seam.EffortHigh, wantReason: "Mid hosted, effort high, decision 0.70"},
		{name: "unsure moves one candidate up", step: "local-chat", p: 0.48, effort: "medium",
			wantModel: "small-hosted", wantEffort: seam.EffortMedium, wantReason: "Moved up: decision unsure (0.48)", movedUp: true},
		{name: "exactly at the level stays", step: "local-chat", p: 0.6, effort: "low",
			wantModel: "local-chat", wantEffort: seam.EffortLow, wantReason: "Local model, effort low, decision 0.60"},
		{name: "unsure moves over a skipped step", facts: Facts{Tokens: 150000}, settings: func(s *Settings) {
			s.Ladder = []Rung{{Name: "small-hosted", Limit: 100000}, {Name: "mid-hosted"}, {Name: "local-chat"}, {Name: "large-hosted"}}
		}, step: "mid-hosted", p: 0.55, effort: "low", wantModel: "large-hosted", wantEffort: seam.EffortLow, wantReason: "Moved up: decision unsure (0.55)", movedUp: true},
		{name: "unsure on the top step stays", step: "large-hosted", p: 0.3, effort: "low",
			wantModel: "large-hosted", wantEffort: seam.EffortLow, wantReason: "Large hosted, effort low, decision 0.30"},
		{name: "the effort is capped", settings: func(s *Settings) { s.MaxEffort = seam.EffortMedium }, step: "small-hosted", p: 0.9, effort: "high",
			wantModel: "small-hosted", wantEffort: seam.EffortMedium, wantReason: "Small hosted, effort medium, decision 0.90"},
		{name: "a switch costs the cache", facts: Facts{PreviousStep: "mid-hosted"}, step: "local-chat", p: 0.95, effort: "low",
			wantModel: "local-chat", wantEffort: seam.EffortLow, wantReason: "Local model, effort low, decision 0.95; switched from Mid hosted"},
		{name: "the same step is no switch", facts: Facts{PreviousStep: "local-chat"}, step: "local-chat", p: 0.95, effort: "low",
			wantModel: "local-chat", wantEffort: seam.EffortLow, wantReason: "Local model, effort low, decision 0.95"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := base()
			if c.settings != nil {
				c.settings(&s)
			}
			v := says(c.step, c.p, c.effort)
			res, err := Route(context.Background(), Input{Settings: s, Facts: c.facts, Usable: usable, Verdict: v.fn})
			if err != nil {
				t.Fatal(err)
			}
			if res.Model.Name != c.wantModel || res.Effort != c.wantEffort || res.Reason != c.wantReason || res.Record.MovedUp != c.movedUp {
				t.Errorf("got %s at %s, %q, moved up %v; want %s at %s, %q, %v", res.Model.Name, res.Effort, res.Reason, res.Record.MovedUp, c.wantModel, c.wantEffort, c.wantReason, c.movedUp)
			}
			if v.calls != 1 || v.models[0] != "house-decide" || v.sensitive[0] {
				t.Errorf("verdict calls %d to %v, sensitive %v; want one to house-decide, not sensitive", v.calls, v.models, v.sensitive)
			}
			r := res.Record
			if r.RulesOnly || r.StepAnswered != c.step || r.Probability != c.p || string(r.EffortAnswered) != c.effort || r.Step != c.wantModel || r.Effort != c.wantEffort || r.DecisionModel != "house-decide" {
				t.Errorf("record %+v", r)
			}
			if res.Switched != (c.facts.PreviousStep != "" && c.facts.PreviousStep != c.wantModel) {
				t.Errorf("switched %v", res.Switched)
			}
			if res.Model.Name != s.Ladder[res.Step-1].Name || res.Steps != len(s.Ladder) {
				t.Errorf("step %d of %d for %s", res.Step, res.Steps, res.Model.Name)
			}
		})
	}
}

// When the rules alone decide: the lowest candidate at the default effort.
func TestRulesOnly(t *testing.T) {
	slow := &verdicts{answer: func(ctx context.Context, req seam.DecisionRequest) (seam.DecisionResult, error) {
		<-ctx.Done()
		return seam.DecisionResult{}, ctx.Err()
	}}
	late := &verdicts{answer: func(ctx context.Context, req seam.DecisionRequest) (seam.DecisionResult, error) {
		<-ctx.Done()
		return answer(req, "large-hosted", 0.99, "high"), nil
	}}
	failing := &verdicts{answer: func(context.Context, seam.DecisionRequest) (seam.DecisionResult, error) {
		return seam.DecisionResult{}, &seam.Error{Kind: seam.ErrOverloaded, Message: "busy"}
	}}
	hostedDown := &verdicts{answer: func(context.Context, seam.DecisionRequest) (seam.DecisionResult, error) {
		return seam.DecisionResult{}, &seam.Error{Kind: seam.ErrUnavailable, Message: "no answer"}
	}}
	notACandidate := says("trusted-chat", 0.99, "high")
	noEffort := &verdicts{answer: func(_ context.Context, req seam.DecisionRequest) (seam.DecisionResult, error) {
		r := answer(req, "mid-hosted", 0.99, "high")
		r.Answers = r.Answers[:1]
		return r, nil
	}}
	sure := says("large-hosted", 0.99, "high")
	cases := []struct {
		name      string
		settings  func(*Settings)
		facts     Facts
		v         *verdicts
		noVerdict bool
		calls     int
		why       string
		reason    string
		timedOut  bool
	}{
		{name: "the verdict times out", settings: func(s *Settings) { s.VerdictTimeout = 20 * time.Millisecond }, v: slow, calls: 1,
			why: OnlyTimeout, reason: "Rules only: no decision within 20ms", timedOut: true},
		{name: "an answer after the timeout is not used", settings: func(s *Settings) { s.VerdictTimeout = 20 * time.Millisecond }, v: late, calls: 1,
			why: OnlyTimeout, reason: "Rules only: no decision within 20ms", timedOut: true},
		{name: "the verdict fails", v: failing, calls: 1, why: OnlyError, reason: "Rules only: the decision failed"},
		{name: "a hosted decision model does not answer", settings: func(s *Settings) { s.DecisionModel = "cloud-decide" }, v: hostedDown, calls: 1,
			why: OnlyError, reason: "Rules only: the decision failed"},
		{name: "the verdict names a step that is not a candidate", v: notACandidate, calls: 1, why: OnlyError, reason: "Rules only: the decision failed"},
		{name: "the verdict does not answer both questions", v: noEffort, calls: 1, why: OnlyError, reason: "Rules only: the decision failed"},
		{name: "no decision model named", settings: func(s *Settings) { s.DecisionModel = "" }, v: sure, why: OnlyNoDecisionModel, reason: "Rules only: no decision model"},
		{name: "no verdict function", v: sure, noVerdict: true, why: OnlyNoDecisionModel, reason: "Rules only: no decision model"},
		{name: "the decision model is not registered", settings: func(s *Settings) { s.DecisionModel = "gone" }, v: sure,
			why: OnlyDecisionModelUnusable, reason: "Rules only: the decision model cannot be used"},
		{name: "the decision model is a chat model", settings: func(s *Settings) { s.DecisionModel = "local-chat" }, v: sure,
			why: OnlyDecisionModelUnusable, reason: "Rules only: the decision model cannot be used"},
		{name: "one candidate asks no verdict", settings: func(s *Settings) { s.Ladder = []Rung{{Name: "local-chat"}, {Name: "small-hosted"}} },
			facts: Facts{Images: true}, v: sure, why: OnlyOneCandidate, reason: "Rules only: one step can take this message"},
		{name: "a held chat's verdict never goes to a hosted decision model",
			settings: func(s *Settings) {
				s.DecisionModel = "cloud-decide"
				s.Ladder = []Rung{{Name: "local-chat"}, {Name: "trusted-chat"}, {Name: "large-hosted"}}
			}, facts: Facts{Held: true}, v: sure,
			why: OnlyHeld, reason: "Rules only: the decision model may not read this chat"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := base()
			if c.settings != nil {
				c.settings(&s)
			}
			in := Input{Settings: s, Facts: c.facts, Usable: usable, Verdict: c.v.fn}
			if c.noVerdict {
				in.Verdict = nil
			}
			res, err := Route(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			cands, _ := Candidates(s, c.facts, usable)
			if res.Model.Name != cands[0].Model.Name || res.Effort != seam.EffortMedium {
				t.Errorf("got %s at %s, want the lowest candidate %s at medium", res.Model.Name, res.Effort, cands[0].Model.Name)
			}
			if !res.Record.RulesOnly || res.Record.RulesOnlyWhy != c.why || res.Reason != c.reason || res.Record.TimedOut != c.timedOut {
				t.Errorf("record %+v, reason %q; want rules only %s, %q, timed out %v", res.Record, res.Reason, c.why, c.reason, c.timedOut)
			}
			if c.v.calls != c.calls {
				t.Errorf("%d verdict calls, want %d", c.v.calls, c.calls)
			}
			if res.Notice != "" {
				t.Errorf("notice %q", res.Notice)
			}
		})
	}
}

func TestTheDefaultEffortIsCapped(t *testing.T) {
	s := base()
	s.DecisionModel = ""
	s.DefaultEffort, s.MaxEffort = seam.EffortHigh, seam.EffortLow
	res, err := Route(context.Background(), Input{Settings: s, Usable: usable})
	if err != nil || res.Effort != seam.EffortLow {
		t.Errorf("got %s, %v; want low", res.Effort, err)
	}
}

// A held chat never gets a hosted step, whatever the verdict says: every
// answer a verdict could give, sure or unsure, on ladders that put hosted
// steps first, last and between.
func TestAHeldChatNeverGetsAHostedStep(t *testing.T) {
	ladders := [][]Rung{
		base().Ladder,
		{{Name: "large-hosted"}, {Name: "mid-hosted"}, {Name: "local-chat"}, {Name: "small-hosted"}, {Name: "trusted-chat"}},
		{{Name: "local-chat"}, {Name: "small-hosted"}, {Name: "trusted-chat"}, {Name: "large-hosted"}},
	}
	for li, ladder := range ladders {
		for _, m := range usable {
			for _, p := range []float64{0.99, 0.6, 0.34} {
				for _, dm := range []string{"house-decide", "cloud-decide", ""} {
					for _, down := range []bool{false, true} {
						s := base()
						s.Ladder, s.DecisionModel = ladder, dm
						f := Facts{Held: true, Tokens: 1000, Now: now}
						if down {
							f.LocalFailedAt = now
						}
						v := says(m.Name, p, "high")
						res, err := Route(context.Background(), Input{Settings: s, Facts: f, Usable: usable, Verdict: v.fn})
						where := fmt.Sprintf("ladder %d, verdict %s at %.2f, decision model %q, local down %v", li, m.Name, p, dm, down)
						if err == nil && !res.Model.MayReadSensitive() {
							t.Errorf("%s: a held chat got the hosted step %s", where, res.Model.Name)
						}
						for i, name := range v.models {
							if name != "house-decide" || !v.sensitive[i] {
								t.Errorf("%s: a held chat's verdict went to %s, sensitive %v", where, name, v.sensitive[i])
							}
						}
						for _, req := range v.sent {
							for _, o := range req.Questions[0].Options {
								if o.ID != "local-chat" && o.ID != "trusted-chat" {
									t.Errorf("%s: a held chat's verdict was offered %s", where, o.ID)
								}
							}
						}
					}
				}
			}
		}
	}
}

// Rule 4 met by the verdict itself: a local decision model that does not
// answer passes over the local steps for this message, with the notice.
func TestTheLocalMachineNotAnswering(t *testing.T) {
	down := func() *verdicts {
		return &verdicts{answer: func(context.Context, seam.DecisionRequest) (seam.DecisionResult, error) {
			return seam.DecisionResult{}, &seam.Error{Kind: seam.ErrUnavailable, Message: "connection refused"}
		}}
	}
	t.Run("the decision call finds it down", func(t *testing.T) {
		v := down()
		res, err := Route(context.Background(), Input{Settings: base(), Facts: Facts{Tokens: 500}, Usable: usable, Verdict: v.fn})
		if err != nil {
			t.Fatal(err)
		}
		if res.Model.Name != "small-hosted" || res.Notice != NoticeLocalUnavailable || res.Reason != "Rules only: local machine not answering" ||
			!res.Record.LocalUnavailable || res.Record.RulesOnlyWhy != OnlyLocalUnavailable || res.Effort != seam.EffortMedium {
			t.Errorf("got %s, notice %q, %q, record %+v", res.Model.Name, res.Notice, res.Reason, res.Record)
		}
		if !hasSkip(res.Skipped, SkipLocalDown) || v.calls != 1 {
			t.Errorf("skipped %v, %d calls", res.Skipped, v.calls)
		}
	})
	t.Run("a held chat moves to a trusted step, never a hosted one", func(t *testing.T) {
		s := base()
		s.Ladder = []Rung{{Name: "local-chat"}, {Name: "trusted-chat"}, {Name: "small-hosted"}}
		v := down()
		res, err := Route(context.Background(), Input{Settings: s, Facts: Facts{Tokens: 500, Held: true}, Usable: usable, Verdict: v.fn})
		if err != nil || res.Model.Name != "trusted-chat" || res.Notice != NoticeLocalUnavailable {
			t.Errorf("got %s, notice %q, %v; want the trusted step with the notice", res.Model.Name, res.Notice, err)
		}
	})
	t.Run("a held chat with two local steps waits too", func(t *testing.T) {
		s := base()
		local2 := localChat
		local2.Name, local2.Label = "local-chat-2", "Second local model"
		s.Ladder = []Rung{{Name: "local-chat"}, {Name: "local-chat-2"}, {Name: "small-hosted"}}
		v := down()
		_, err := Route(context.Background(), Input{Settings: s, Facts: Facts{Tokens: 500, Held: true}, Usable: append(usable, local2), Verdict: v.fn})
		var r *Refusal
		if !errors.As(err, &r) || r.Code != CodeLocalUnavailable || v.calls != 1 {
			t.Errorf("got %v after %d calls, want local_unavailable after one", err, v.calls)
		}
	})
	t.Run("known down: no verdict is asked of a local decision model", func(t *testing.T) {
		v := says("mid-hosted", 0.99, "high")
		res, err := Route(context.Background(), Input{Settings: base(), Facts: Facts{Tokens: 500, LocalFailedAt: now.Add(-30 * time.Second), Now: now}, Usable: usable, Verdict: v.fn})
		if err != nil {
			t.Fatal(err)
		}
		if v.calls != 0 || res.Model.Name != "small-hosted" || res.Notice != NoticeLocalUnavailable || res.Reason != "Rules only: local machine not answering" {
			t.Errorf("%d calls, got %s, notice %q, %q", v.calls, res.Model.Name, res.Notice, res.Reason)
		}
	})
	t.Run("known down: a hosted decision model still decides, with the notice", func(t *testing.T) {
		s := base()
		s.DecisionModel = "cloud-decide"
		v := says("mid-hosted", 0.99, "high")
		res, err := Route(context.Background(), Input{Settings: s, Facts: Facts{Tokens: 500, LocalFailedAt: now, Now: now}, Usable: usable, Verdict: v.fn})
		if err != nil {
			t.Fatal(err)
		}
		if v.calls != 1 || res.Model.Name != "mid-hosted" || res.Notice != NoticeLocalUnavailable {
			t.Errorf("%d calls, got %s, notice %q", v.calls, res.Model.Name, res.Notice)
		}
		for _, o := range v.sent[0].Questions[0].Options {
			if o.ID == "local-chat" {
				t.Errorf("the verdict was offered the local step while the machine is down")
			}
		}
	})
}

// What the decision model is shown: the newest message, cut, and the
// call's facts; and the two questions.
func TestTheVerdictsRequest(t *testing.T) {
	s := base()
	s.VerdictMaxChars = 12
	s.StepNotes = map[string]string{"mid-hosted": "long documents and code"}
	v := says("small-hosted", 0.8, "medium")
	f := Facts{Text: "  Please plan my week, with every meeting.  ", Tokens: 4321, Images: true, Tools: 2, PreviousStep: "mid-hosted", Turns: 7}
	res, err := Route(context.Background(), Input{Settings: s, Facts: f, Usable: usable, Verdict: v.fn})
	if err != nil {
		t.Fatal(err)
	}
	req := v.sent[0]
	if err := seam.ValidateDecisionRequest(req); err != nil {
		t.Fatalf("the request is not valid: %v", err)
	}
	want := "Newest message:\nPlease plan \n\nFacts about the call:\n- Estimated tokens: 4321\n- Holds images: yes\n- Holds files: no\n- Tools offered: 2\n- Step the previous message used: Mid hosted\n- Turns in the chat so far: 7"
	if req.Model != "house-decide" || req.State != want {
		t.Errorf("model %s, state:\n%s\nwant:\n%s", req.Model, req.State, want)
	}
	if strings.Contains(req.State, "every meeting") {
		t.Errorf("the state holds more than router.verdict_max_chars of the message")
	}
	step, effort := req.Questions[0], req.Questions[1]
	wantSteps := []seam.DecisionOption{
		{ID: "small-hosted", Description: "Small hosted: step 1 of 3, smallest first"},
		{ID: "mid-hosted", Description: "long documents and code"},
		{ID: "large-hosted", Description: "Large hosted: step 3 of 3, smallest first"},
	}
	if step.ID != QuestionStep || step.Type != seam.QuestionChoice || !reflect.DeepEqual(step.Options, wantSteps) {
		t.Errorf("step question %+v", step)
	}
	if effort.ID != QuestionEffort || !reflect.DeepEqual(seam.OptionIDs(effort), []string{"low", "medium", "high"}) {
		t.Errorf("effort question %+v", effort)
	}
	if res.Record.Shown != (Shown{Tokens: 4321, Images: true, Tools: 2, PreviousStep: "mid-hosted", Turns: 7}) {
		t.Errorf("shown %+v", res.Record.Shown)
	}
	// A message with no text is still a state.
	v = says("small-hosted", 0.8, "medium")
	if _, err := Route(context.Background(), Input{Settings: s, Facts: Facts{Images: true}, Usable: usable, Verdict: v.fn}); err != nil || !strings.HasPrefix(v.sent[0].State, "Newest message:\n(no text)\n") {
		t.Errorf("no text: %v, %q", err, v.sent[0].State)
	}
}

func TestAStoppedTurnStopsTheVerdict(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	v := &verdicts{answer: func(c context.Context, req seam.DecisionRequest) (seam.DecisionResult, error) {
		cancel()
		<-c.Done()
		return seam.DecisionResult{}, c.Err()
	}}
	if _, err := Route(ctx, Input{Settings: base(), Usable: usable, Verdict: v.fn}); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want the context's error", err)
	}
}

func TestBadSettingsAreRefused(t *testing.T) {
	s := base()
	s.VerdictTimeout = 0
	if _, err := Route(context.Background(), Input{Settings: s, Usable: usable}); err == nil {
		t.Error("Route took a verdict timeout of zero")
	}
	if _, err := Stronger(Input{Settings: s, Usable: usable}, "local-chat", seam.EffortLow); err == nil {
		t.Error("Stronger took a verdict timeout of zero")
	}
}

func TestStronger(t *testing.T) {
	cases := []struct {
		name     string
		facts    Facts
		answered string
		effort   seam.Effort
		want     string
		code     string
	}{
		{name: "one step up", answered: "local-chat", effort: seam.EffortLow, want: "small-hosted"},
		{name: "over a step that cannot take the call", facts: Facts{Tokens: 150000}, answered: "local-chat", effort: seam.EffortLow, want: "mid-hosted"},
		{name: "the effort is capped", answered: "small-hosted", effort: seam.EffortMax, want: "mid-hosted"},
		{name: "the top step", answered: "large-hosted", code: CodeNoStronger},
		{name: "a held chat has nothing above the local step", facts: Facts{Held: true}, answered: "local-chat", code: CodeNoStronger},
		{name: "not on the ladder", answered: "trusted-chat", code: CodeNoStronger},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := Stronger(Input{Settings: base(), Facts: c.facts, Usable: usable}, c.answered, c.effort)
			if c.code != "" {
				var r *Refusal
				if !errors.As(err, &r) || r.Code != c.code {
					t.Errorf("got %s, %v; want %s", res.Model.Name, err, c.code)
				}
				return
			}
			if err != nil || res.Model.Name != c.want || res.Record.RulesOnlyWhy != OnlyStronger || res.Reason != "Stronger model asked for" {
				t.Errorf("got %s, %q, %+v, %v; want %s", res.Model.Name, res.Reason, res.Record, err, c.want)
			}
			if res.Effort.Known() && rank(res.Effort) > rank(base().MaxEffort) {
				t.Errorf("effort %s is over the cap", res.Effort)
			}
		})
	}
}
