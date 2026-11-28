package usagereport

import (
	"context"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// Auto in the usage report (P14-07): what the period's replies on Auto
// cost, with the router's decision calls, against the same tokens priced
// at the ladder's top step, and how often a stronger model was asked for.
// It is summed from the same usage records as the rest of the report and
// from the router's verdicts (router_verdicts), and keeps no total of its
// own. docs/architecture/auto-in-usage.md describes it.

// Auto is the period's use of Auto.
type Auto struct {
	// Turns is how many messages were routed on Auto: the turns whose
	// first verdict was written in the period, a stronger model's rewrite
	// included.
	Turns int `json:"turns"`
	// StrongerAsked is how many of those turns a stronger model was asked
	// for, by "Try a stronger model".
	StrongerAsked int `json:"stronger_asked"`
	// Rewrites is how many of those turns were a stronger model's rewrite
	// of another.
	Rewrites int `json:"rewrites"`
	// Replies are the period's records of chat calls under a request id
	// the router routed: the replies on Auto, every tool step included.
	Replies Sum `json:"replies"`
	// Decisions are the period's records of the router's decision calls.
	Decisions Sum `json:"decisions"`
	// CostMicros is what Auto cost: the replies and the decisions.
	CostMicros int64 `json:"cost_micros"`
	// TopStep is the ladder's top step, and what the replies' tokens cost
	// priced at it.
	TopStep TopStep `json:"top_step"`
	// SavedMicros is the top step's cost less CostMicros. Below zero, Auto
	// cost more than the top step would have. Zero when the top step has a
	// Problem.
	SavedMicros int64 `json:"saved_micros"`
}

// TopStep is the ladder's top step as it is set now: the highest name on
// router.ladder that is a chat model in the person's registry.
type TopStep struct {
	Name  string       `json:"name,omitempty"`
	Label string       `json:"label,omitempty"`
	Class models.Class `json:"class,omitempty"`
	// CostMicros is the replies' token counts priced at this step's price,
	// call by call, as the turn prices a call to it. A refused call, one
	// that was never sent, costs nothing.
	CostMicros int64 `json:"cost_micros"`
	// Problem says, in words, why there is no top step to price the
	// replies at: no ladder, a ladder that does not read, or no step of it
	// in the registry.
	Problem string `json:"problem,omitempty"`
}

// The kinds of the usage records Auto counts.
const (
	autoReply    = string(routing.KindChat)
	autoDecision = string(routing.KindDecision)
	// strongerWhy is the verdict's rules_only_why on a stronger model's
	// rewrite (router.OnlyStronger).
	strongerWhy = router.OnlyStronger
)

// stamps are written as the usage records and the verdicts write them.
const stampTime = "2006-01-02T15:04:05.000Z"

// ReadAuto is the Auto figures of a period, from its usage records as
// Records read them and from the tenant's verdicts. Read calls it on the
// records of its report.
func ReadAuto(ctx context.Context, s *tenant.Store, reg []settings.Setting, recs []usage.Record, p Period) (Auto, error) {
	if p.end.IsZero() || !p.end.After(p.start) {
		return Auto{}, fmt.Errorf("usage report: the period %s to %s was not worked out by Resolve", p.From, p.To)
	}
	var out Auto
	var price models.Price
	var err error
	if out.TopStep, price, err = topStep(ctx, s, reg); err != nil {
		return Auto{}, err
	}
	routed, err := routedRequests(ctx, s, p)
	if err != nil {
		return Auto{}, err
	}
	for _, r := range recs {
		if r.RequestID == "" || !routed[r.RequestID] {
			continue
		}
		switch r.Kind {
		case autoReply:
			out.Replies.add(r)
			out.TopStep.CostMicros += priceAt(price, r)
		case autoDecision:
			out.Decisions.add(r)
		}
	}
	out.CostMicros = out.Replies.CostMicros + out.Decisions.CostMicros
	if out.TopStep.Problem != "" {
		out.TopStep.CostMicros = 0
	} else {
		out.SavedMicros = out.TopStep.CostMicros - out.CostMicros
	}
	if err := countTurns(ctx, s, p, &out); err != nil {
		return Auto{}, err
	}
	return out, nil
}

// priceAt is what one call's tokens cost at a price, as the turn prices a
// call: the four counts, which do not overlap. A refused call was never
// sent.
func priceAt(p models.Price, r usage.Record) int64 {
	if r.Outcome == usage.OutcomeRefused {
		return 0
	}
	return p.CostMicros(r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens)
}

// topStep is the ladder's top step as the person's settings and registry
// stand now, with its price. A setting that cannot be read at all is an
// error; a ladder with no top step is a Problem.
func topStep(ctx context.Context, s *tenant.Store, reg []settings.Setting) (TopStep, models.Price, error) {
	text, err := tenantsettings.Get(ctx, s, reg, router.KeyLadder)
	if err != nil {
		return TopStep{}, models.Price{}, fmt.Errorf("usage report: %s: %w", router.KeyLadder, err)
	}
	ladder, err := router.ParseLadder(text)
	if err != nil {
		return TopStep{Problem: "the ladder does not read: " + err.Error()}, models.Price{}, nil
	}
	if len(ladder) == 0 {
		return TopStep{Problem: "no ladder is set, so there is no top step to price the replies at"}, models.Price{}, nil
	}
	entries, err := models.List(ctx, s)
	if err != nil {
		return TopStep{}, models.Price{}, fmt.Errorf("usage report: reading the model registry: %w", err)
	}
	byName := map[string]models.Model{}
	for _, e := range entries {
		if e.Problem == "" {
			byName[e.Model.Name] = e.Model
		}
	}
	for i := len(ladder) - 1; i >= 0; i-- {
		m, ok := byName[ladder[i].Name]
		if !ok || m.Has(models.CapEmbedding) || m.Has(models.CapDecision) {
			continue
		}
		label := m.Label
		if label == "" {
			label = m.Name
		}
		return TopStep{Name: m.Name, Label: label, Class: m.Class}, m.Price, nil
	}
	return TopStep{Problem: fmt.Sprintf("no step of the ladder is a chat model in your registry, so the replies cannot be priced at its top step, %s", ladder[len(ladder)-1].Name)}, models.Price{}, nil
}

// routedRequests are the request ids of the period's months that have a
// verdict: the calls under them were made for messages on Auto. The join
// starts from the usage records of those months and finds each verdict by
// router_verdicts_by_request.
func routedRequests(ctx context.Context, s *tenant.Store, p Period) (map[string]bool, error) {
	first, last := usage.MonthOf(p.start), usage.MonthOf(p.end.Add(-time.Millisecond))
	rows, err := s.DB().QueryContext(ctx, `SELECT DISTINCT u.request_id FROM usage_records u
		JOIN router_verdicts v ON v.request_id = u.request_id
		WHERE u.month >= ? AND u.month <= ? AND u.request_id <> '' AND u.kind IN (?, ?)`, first, last, autoReply, autoDecision)
	if err != nil {
		return nil, fmt.Errorf("usage report: reading the requests routed on Auto: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("usage report: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// countTurns counts the turns routed on Auto in the period from their
// verdicts: a turn is in the period of its first verdict (a turn routed
// again when its local step did not answer has two), it was asked to be
// written again when any of its verdicts is marked stronger_asked, and it
// is a rewrite when its verdict says so.
func countTurns(ctx context.Context, s *tenant.Store, p Period, out *Auto) error {
	from, to := p.start.UTC().Format(stampTime), p.end.UTC().Format(stampTime)
	err := s.DB().QueryRowContext(ctx, `SELECT count(*), coalesce(sum(asked), 0), coalesce(sum(rewrite), 0) FROM (
			SELECT max(stronger_asked) AS asked, max(rules_only_why = ?) AS rewrite FROM router_verdicts
			WHERE turn_id IN (SELECT turn_id FROM router_verdicts WHERE created_at >= ? AND created_at < ?)
			GROUP BY turn_id HAVING min(created_at) >= ?)`, strongerWhy, from, to, from).
		Scan(&out.Turns, &out.StrongerAsked, &out.Rewrites)
	if err != nil {
		return fmt.Errorf("usage report: counting the turns on Auto: %w", err)
	}
	return nil
}
