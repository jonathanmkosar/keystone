package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/settings"
)

// The router (P14-03), a case of the "sensitive content" check: a held chat
// on Auto never gets a hosted step, whatever the verdict says, and its
// verdict never goes to a hosted decision model.
//
// An invented person's ladder mixes hosted steps with a local and a trusted
// one, in an order that puts a hosted step first. The settings are the
// registry's defaults with that ladder. The verdict is a function that
// always names the step it is told to, sure of it, and keeps every request
// it is sent; nothing leaves this machine.
//
//  1. A held chat, with a verdict naming each hosted step at 0.99, gets a
//     local or trusted step; the verdict went to the local decision model,
//     marked sensitive, and was offered no hosted step.
//  2. A held chat whose decision model is hosted asks no verdict.
//  3. A held chat whose ladder is hosted only is refused as sensitive.
//  4. A held chat whose local machine is not answering, with no trusted
//     step, is refused as local_unavailable and never moves to a hosted one.
//  5. A chat that is not held, with the same verdict, gets the hosted step,
//     so the check would notice a router that refused everything.

// The router's doc, and its place among the guards of rule 3.
func init() {
	componentDocs["internal/router"] = "docs/architecture/router.md"
	fixedRuleGuards["rules.sensitive_content"] = append(fixedRuleGuards["rules.sensitive_content"], "internal/router")
}

var (
	routerLocal = models.Model{Name: "check-house-chat", Label: "House chat", Adapter: models.AdapterChatCompletions, Class: models.ClassLocal,
		ContextWindow: 32768, OutputLimit: 4096, Capabilities: []models.Capability{models.CapTools}}
	routerTrusted = models.Model{Name: "check-friend-chat", Label: "Trusted chat", Adapter: models.AdapterChatCompletions, Class: models.ClassTrusted,
		ContextWindow: 65536, OutputLimit: 8192, Capabilities: []models.Capability{models.CapTools}}
	routerSmall = models.Model{Name: "check-small-cloud", Label: "Small cloud", Adapter: models.AdapterAnthropic, Class: models.ClassHosted,
		ContextWindow: 200000, OutputLimit: 8192, Capabilities: []models.Capability{models.CapTools}}
	routerLarge = models.Model{Name: "check-large-cloud", Label: "Large cloud", Adapter: models.AdapterAnthropic, Class: models.ClassHosted,
		ContextWindow: 200000, OutputLimit: 8192, Capabilities: []models.Capability{models.CapTools}}
	routerUsable = []models.Model{routerLocal, routerTrusted, routerSmall, routerLarge, decideLocal, decideHosted}
	routerNow    = time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
)

func routerProblems() []string {
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	reg, err := settings.Registry()
	if err != nil {
		return []string{"router: reading the settings registry: " + err.Error()}
	}
	base, err := router.Load(func(key string) (string, bool) {
		st, ok := settings.Lookup(reg, key)
		return st.Default, ok
	})
	if err != nil {
		return []string{"router: the registry's defaults do not load: " + err.Error()}
	}
	ladder := func(ms ...models.Model) []router.Rung {
		var out []router.Rung
		for _, m := range ms {
			out = append(out, router.Rung{Name: m.Name})
		}
		return out
	}
	type heard struct {
		model     string
		sensitive bool
		offered   []string
	}
	var calls []heard
	naming := func(step string) router.Verdict {
		return func(_ context.Context, m models.Model, sensitive bool, req seam.DecisionRequest) (seam.DecisionResult, error) {
			h := heard{model: m.Name, sensitive: sensitive}
			res := seam.DecisionResult{Model: m.Name}
			for _, q := range req.Questions {
				ids := seam.OptionIDs(q)
				if q.ID == router.QuestionStep {
					h.offered = ids
				}
				choice := ids[len(ids)-1]
				if q.ID == router.QuestionStep {
					choice = step
				}
				probs := map[string]float64{}
				for _, id := range ids {
					probs[id] = 0.01 / float64(len(ids)-1)
				}
				probs[choice] = 0.99
				res.Answers = append(res.Answers, seam.DecisionAnswer{Question: q.ID, Choice: choice, Probabilities: probs})
			}
			calls = append(calls, h)
			return res, nil
		}
	}
	route := func(s router.Settings, f router.Facts, v router.Verdict) (router.Result, error) {
		return router.Route(context.Background(), router.Input{Settings: s, Facts: f, Usable: routerUsable, Verdict: v})
	}
	held := router.Facts{Text: "Is the letter from the clinic in the box?", Tokens: 800, Held: true}

	// 1. A held chat, a verdict naming each hosted step.
	s := base
	s.Ladder, s.DecisionModel = ladder(routerSmall, routerLocal, routerLarge, routerTrusted), decideLocal.Name
	for _, hosted := range []models.Model{routerSmall, routerLarge} {
		calls = nil
		res, err := route(s, held, naming(hosted.Name))
		switch {
		case err != nil:
			say("router: a held chat with a local and a trusted step was refused: %v", err)
		case !res.Model.MayReadSensitive():
			say("router: a held chat on Auto was given the hosted step %s by a verdict naming %s", res.Model.Name, hosted.Name)
		}
		for _, c := range calls {
			if c.model != decideLocal.Name || !c.sensitive {
				say("router: a held chat's verdict went to %s, marked sensitive %v", c.model, c.sensitive)
			}
			for _, id := range c.offered {
				if id == routerSmall.Name || id == routerLarge.Name {
					say("router: a held chat's verdict was offered the hosted step %s", id)
				}
			}
		}
		if len(calls) != 1 {
			say("router: a held chat with two candidates asked %d verdicts, want one", len(calls))
		}
	}
	// 2. A hosted decision model is not asked about a held chat.
	calls = nil
	s.DecisionModel = decideHosted.Name
	if res, err := route(s, held, naming(routerLarge.Name)); err != nil || !res.Model.MayReadSensitive() || len(calls) != 0 {
		say("router: a held chat whose decision model is hosted gave %s, %v, after %d verdicts; want a local or trusted step and none", res.Model.Name, err, len(calls))
	}
	// 3. Only hosted steps.
	s.Ladder, s.DecisionModel = ladder(routerSmall, routerLarge), decideLocal.Name
	if res, err := route(s, held, naming(routerLarge.Name)); !errors.Is(err, routing.ErrSensitive) {
		say("router: a held chat with only hosted steps gave %s, %v; want the sensitive refusal", res.Model.Name, err)
	}
	// 4. The local machine not answering.
	s.Ladder = ladder(routerLocal, routerSmall, routerLarge)
	down := held
	down.LocalFailedAt = routerNow
	down.Now = routerNow
	var refused *router.Refusal
	if res, err := route(s, down, naming(routerSmall.Name)); !errors.As(err, &refused) || refused.Code != router.CodeLocalUnavailable {
		say("router: a held chat whose local machine is down gave %s, %v; want local_unavailable", res.Model.Name, err)
	}
	// 5. Not held: the verdict is followed.
	s.Ladder = ladder(routerSmall, routerLocal, routerLarge, routerTrusted)
	plain := held
	plain.Held = false
	if res, err := route(s, plain, naming(routerLarge.Name)); err != nil || res.Model.Name != routerLarge.Name {
		say("router: a chat that is not held, with a verdict naming %s, gave %s, %v", routerLarge.Name, res.Model.Name, err)
	}
	return problems
}
