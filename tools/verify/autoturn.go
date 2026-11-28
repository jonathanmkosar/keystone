package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// Auto in the turn (P14-04), a case of the "sensitive content" check: a
// held chat on Auto, sent through the real server, reaches no hosted model,
// whatever the verdict says.
//
// The test tenant is given its recorded ladder (a local, a trusted and a
// hosted step, all played by the fake provider) and its recorded verdicts,
// whose answer to the message below names the hosted step, sure of it.
//
//  1. In the chat the check's first step sent sensitive content to, which
//     is held, the message on Auto is answered by a step that may read it:
//     no call of the turn goes to the hosted step, the verdict was asked
//     and offered no hosted step, and the verdict kept says so.
//  2. In a new chat that is not held, the same message reaches the hosted
//     step, so the check would notice a turn that refused everything.
//
// The ladder is taken away again, so the rest of the check runs as before.

// autoHeldText is the message the recorded verdicts answer with the hosted
// step (fixtures/test-tenant/turns/router.json).
const autoHeldText = "A held question for the large step."

func autoHeldProblems(g *knowledgeRig, token, heldChat string) ([]string, error) {
	ctx := context.Background()
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	ladder := func(values ...string) error {
		return g.with(g.test.ID, func(s *tenant.Store) error {
			for i := 0; i+1 < len(values); i += 2 {
				if err := tenantsettings.Set(ctx, s, g.reg, tenantsettings.Admin, values[i], values[i+1]); err != nil {
					return fmt.Errorf("setting %s: %w", values[i], err)
				}
			}
			return nil
		})
	}
	if err := ladder(router.KeyLadder, turn.TestSmallModel+", "+turn.TestMiddleModel+", "+turn.TestLargeModel, router.KeyDecisionModel, turn.TestDecisionModel); err != nil {
		return nil, err
	}
	defer ladder(router.KeyLadder, "", router.KeyDecisionModel, "")

	err := g.with(g.test.ID, func(s *tenant.Store) error {
		held, err := recall.Held(ctx, s, heldChat)
		if err == nil && !held {
			say("auto: the test tenant's chat that was sent sensitive content is not held, so the check proves nothing")
		}
		return err
	})
	if err != nil {
		return nil, err
	}

	// calls are the model calls a turn made, by model, and the decision
	// requests it sent.
	calls := func(requestID string) (models map[string]int, decisions []string, err error) {
		models = map[string]int{}
		err = g.with(g.test.ID, func(s *tenant.Store) error {
			entries, err := audit.List(ctx, s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
			if err != nil {
				return err
			}
			for _, e := range entries {
				if e.Outcome != audit.OutcomeStarted {
					continue
				}
				models[e.Action]++
				if e.Action == turn.TestDecisionModel {
					decisions = append(decisions, e.Sent)
				}
			}
			return nil
		})
		return models, decisions, err
	}

	// 1. The held chat.
	t, err := g.send(token, heldChat, autoHeldText, "")
	if err != nil {
		return nil, err
	}
	if !strings.Contains(t.stream, "event: "+turn.EventRoute+"\n") || !strings.Contains(t.stream, `"status":"done"`) {
		say("auto: the held chat's turn on Auto was not routed, or did not end as done")
	}
	models, decisions, err := calls(t.requestID)
	if err != nil {
		return nil, err
	}
	if n := models[turn.TestLargeModel]; n != 0 {
		say("auto: a held chat on Auto, with a verdict naming the hosted step, called it %d times", n)
	}
	if len(decisions) != 1 {
		say("auto: the held chat's turn asked %d verdicts, want one", len(decisions))
	}
	for _, sent := range decisions {
		if strings.Contains(sent, `"`+turn.TestLargeModel+`"`) {
			say("auto: the held chat's verdict was offered the hosted step")
		}
	}
	err = g.with(g.test.ID, func(s *tenant.Store) error {
		vs, err := turn.Verdicts(ctx, s, turn.VerdictFilter{TurnID: t.turnID})
		if err != nil {
			return err
		}
		if len(vs) != 1 || vs[0].Step == turn.TestLargeModel {
			say("auto: the held chat's verdicts are %+v, want one that is not the hosted step", vs)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 2. A chat that is not held follows the verdict.
	plain, err := g.send(token, "", autoHeldText, "")
	if err != nil {
		return nil, err
	}
	if models, _, err = calls(plain.requestID); err != nil {
		return nil, err
	}
	if models[turn.TestLargeModel] == 0 {
		say("auto: a chat that is not held, with a verdict naming the hosted step, did not reach it, so the check proves nothing")
	}
	return problems, nil
}
