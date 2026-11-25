package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// This file is where a turn meets routing and usage (P3-08). The rules,
// the privacy filter and the fallback chain are in internal/routing, and
// the records and the cap are in internal/usage. What is here reads the
// settings, opens the route's models and writes the records.

// The settings routing and the cap read, in settings/models.json. The
// title's rule is chats.title_model, which is older than routing.
const (
	KeySummaryModel   = "routing.summary_model"
	KeyMemoryModel    = "routing.memory_model"
	KeyEmbeddingModel = "routing.embedding_model"
	KeyFallbackModels = "routing.fallback_models"
	KeyMaxFallbacks   = "routing.max_fallbacks"
	KeyMonthlyCap     = "usage.monthly_cap_usd"
	KeyCapWarnPercent = "usage.cap_warn_percent"
)

// CodeSensitive says the turn carries content from a sensitive space and
// the person has no local or trusted model to send it to.
const CodeSensitive = "sensitive"

// rules reads the routing rules in force for a tenant.
func (r *Runner) rules(ctx context.Context, s *tenant.Store) routing.Rules {
	return routing.Rules{
		Models: map[routing.Kind]string{
			routing.KindTitle:     r.setting(ctx, s, KeyTitleModel),
			routing.KindSummary:   r.setting(ctx, s, KeySummaryModel),
			routing.KindMemory:    r.setting(ctx, s, KeyMemoryModel),
			routing.KindEmbedding: r.setting(ctx, s, KeyEmbeddingModel),
		},
		Fallback:     routing.Names(r.setting(ctx, s, KeyFallbackModels)),
		MaxFallbacks: r.number(ctx, s, KeyMaxFallbacks),
	}
}

// routed returns the model a call of one kind is meant for and a provider
// that is the whole route: that model first, then the fallback chain,
// each behind the budget's guard, with the monthly cap asked before every
// call. picked is the model the chat runs on. secrets are the keys of
// every model in the route, for the audit log to remove.
//
// A chat's picked model must open, and why it did not is the turn's
// error. Any other model of a route that does not open is left out.
func (r *Runner) routed(ctx context.Context, s *tenant.Store, t row, kind routing.Kind, picked string) (models.Model, seam.Provider, []string, *TurnError) {
	none := func(failure *TurnError) (models.Model, seam.Provider, []string, *TurnError) {
		return models.Model{}, nil, nil, failure
	}
	type opened struct {
		m       models.Model
		p       seam.Provider
		secrets []string
	}
	var first *opened
	if kind == routing.KindChat {
		m, p, secrets, failure := r.openNamed(ctx, s, picked)
		if failure != nil {
			return none(failure)
		}
		first = &opened{m, p, secrets}
	}
	usable, err := r.candidates(ctx, s)
	if err != nil {
		r.logf("turns: tenant %s: reading the registry: %v", s.TenantID(), err)
		return none(&TurnError{Code: CodeFailed, Message: "The turn failed inside Keystone. The reason is in the server's log."})
	}
	// Fixed rule 3: a call that carries content from a sensitive space goes
	// only to models that may read it, first or as a fallback (P6-05).
	sensitive := r.sensitive(ctx, s, t, first != nil && first.m.MayReadSensitive())
	route, err := routing.Plan(r.rules(ctx, s), routing.Ask{Kind: kind, Model: picked, Sensitive: sensitive}, usable)
	if err != nil {
		if errors.Is(err, routing.ErrSensitive) {
			return none(&TurnError{Code: CodeSensitive, Message: "This chat holds content from a sensitive space, and none of the models it could be sent to is local or trusted, so it was not sent. Pick a local or trusted model for it."})
		}
		return none(&TurnError{Code: CodeNoModel, Message: "No model in this person's registry can take this call."})
	}

	var links []routing.Link
	var secrets []string
	for _, m := range route.Models {
		o := first
		if o == nil || o.m.Name != m.Name {
			om, op, os, failure := r.openNamed(ctx, s, m.Name)
			if failure != nil {
				continue // openNamed wrote why to the log
			}
			o = &opened{om, op, os}
		}
		links = append(links, routing.Link{Model: o.m, Provider: o.p})
		secrets = append(secrets, o.secrets...)
	}
	chain, err := routing.NewChain(sensitive, links, routing.Hooks{
		Gate:  r.optionGate(s, t, r.gate(s, kind)), // the cap and the memory reserve (P6-06), then Options.Gate (P8-01, jobs.go)
		Moved: r.moved(s, t, kind, &secrets),
	})
	if err != nil {
		r.logf("turns: tenant %s: no model of the route for a %s call could be opened: %v", s.TenantID(), kind, err)
		return none(&TurnError{Code: CodeModelUnavailable, Message: "None of the models this call could be sent to can be used."})
	}
	return links[0].Model, chain, secrets, nil
}

// capGate is the monthly spend cap as a chain's gate. A local model is
// never asked about. For any other, the most the call could cost is its
// estimated input and the whole room kept for the reply, at the model's
// prices. A cap that cannot be read stops the call.
func (r *Runner) capGate(s *tenant.Store) func(context.Context, models.Model, seam.Request) *seam.Error {
	return func(ctx context.Context, m models.Model, req seam.Request) *seam.Error {
		if !usage.Counts(m.Class) {
			return nil
		}
		unread := func(what string, err error) *seam.Error {
			r.logf("turns: tenant %s: %s: %v", s.TenantID(), what, err)
			return &seam.Error{Kind: seam.ErrSpendCap, Message: "The monthly spend cap could not be read, so the call was not sent. The reason is in the server's log."}
		}
		limit, err := strconv.ParseFloat(r.setting(ctx, s, KeyMonthlyCap), 64)
		if err != nil {
			return unread("reading "+KeyMonthlyCap, err)
		}
		warn, err := strconv.Atoi(r.setting(ctx, s, KeyCapWarnPercent))
		if err != nil {
			return unread("reading "+KeyCapWarnPercent, err)
		}
		c, err := usage.NewCap(limit, warn)
		if err != nil {
			return unread("reading the cap", err)
		}
		st, err := usage.Allow(context.Background(), s, c, time.Now(), mostCost(m, req, r.o.Config.Budget))
		if err != nil {
			return unread("summing the month's usage", err)
		}
		switch st.State {
		case usage.StateStopped:
			return &seam.Error{Kind: seam.ErrSpendCap, Message: fmt.Sprintf(
				"The call to %s was not sent. This month's spending on models that are not local is %s, the monthly cap is %s, and this call could cost up to %s.",
				m.Name, usage.Dollars(st.SpentMicros), usage.Dollars(st.LimitMicros), usage.Dollars(st.NextMicros))}
		case usage.StateWarning:
			r.logf("turns: tenant %s: spending is near the monthly cap: %s of %s in %s", s.TenantID(), usage.Dollars(st.SpentMicros), usage.Dollars(st.LimitMicros), st.Month)
		}
		return nil
	}
}

// mostCost is the most one call could cost, in millionths of a US dollar:
// the input as the budget estimates it, with the margin, at the dearer of
// the input price and the price of writing the cache, and the whole room
// kept for the reply at the output price.
func mostCost(m models.Model, req seam.Request, o budget.Options) int64 {
	input := budget.Estimated(req, o).Tokens
	reply := budget.Reply(m, req)
	if m.Price.CacheWrite > m.Price.Input {
		return m.Price.CostMicros(0, reply, 0, input)
	}
	return m.Price.CostMicros(input, reply, 0, 0)
}

// moved records a model of a route that did not answer, and the start of
// the call to the next one, as a chain's hook. The audit log then holds a
// started entry and an end entry for each of the two calls, and the usage
// records hold the failed call. secrets points at the route's keys, which
// are known only once every model is opened.
func (r *Runner) moved(s *tenant.Store, t row, kind routing.Kind, secrets *[]string) func(context.Context, routing.Attempt, models.Model, seam.Request) error {
	return func(_ context.Context, failed routing.Attempt, next models.Model, req seam.Request) error {
		bg := context.Background()
		received, _ := json.Marshal(failed.Error)
		end := audit.Entry{
			Kind: audit.KindModel, RequestID: t.requestID, Actor: t.actor, Action: failed.Model.Name,
			Outcome: audit.OutcomeError, Received: string(received), DurationMS: failed.Duration.Milliseconds(),
		}
		if failed.Refused || failed.Error.Kind == seam.ErrTooLong {
			end.Outcome = audit.OutcomeRefused
		}
		if _, err := audit.Append(bg, s, end, *secrets...); err != nil {
			return fmt.Errorf("recording the failed call to %s: %w", failed.Model.Name, err)
		}
		r.record(s, t, kind, failed.Model, end, false)
		r.logf("turns: %s turn %s: model %s did not answer (%s), trying %s", t.requestID, t.ID, failed.Model.Name, failed.Error.Kind, next.Name)

		sent, err := seam.EncodeRequest(req)
		if err != nil {
			return fmt.Errorf("building the request for %s: %w", next.Name, err)
		}
		start := audit.Entry{Kind: audit.KindModel, RequestID: t.requestID, Actor: t.actor, Action: next.Name, Outcome: audit.OutcomeStarted, Sent: string(sent)}
		if _, err := audit.Append(bg, s, start, *secrets...); err != nil {
			return fmt.Errorf("recording the call to %s: %w", next.Name, err)
		}
		return nil
	}
}

// used writes the usage record of a model call that has ended, from the
// audit entry of its end. step is the turn's step, and zero for the call
// that writes a chat's title.
func (r *Runner) used(s *tenant.Store, t row, step int, m models.Model, end audit.Entry, stopped bool) {
	kind := routing.KindChat
	if step == 0 {
		kind = routing.KindTitle
	}
	if step == stepSummary { // a summary of the chat's oldest turns (P5-02)
		kind = routing.KindSummary
	}
	if step == stepMemory { // the memory pass after a turn (P6-06)
		kind = routing.KindMemory
	}
	r.record(s, t, kind, m, end, stopped)
}

// record writes one usage record. A record that cannot be written is in
// the server's log: the call itself is in the audit log either way.
func (r *Runner) record(s *tenant.Store, t row, kind routing.Kind, m models.Model, end audit.Entry, stopped bool) {
	rec := usage.Record{
		RequestID: t.requestID, Kind: string(kind), Model: m.Name, Class: m.Class, Outcome: usage.OutcomeError,
		InputTokens: end.Usage.InputTokens, OutputTokens: end.Usage.OutputTokens,
		CacheReadTokens: end.Usage.CacheReadTokens, CacheWriteTokens: end.Usage.CacheWriteTokens,
		ThinkingTokens: end.Usage.ThinkingTokens, Estimated: end.Usage.Estimated,
		CostMicros: end.CostMicros, DurationMS: end.DurationMS,
	}
	switch {
	case stopped:
		rec.Outcome = usage.OutcomeStopped
	case end.Outcome == audit.OutcomeOK:
		rec.Outcome = usage.OutcomeOK
	case end.Outcome == audit.OutcomeRefused:
		rec.Outcome = usage.OutcomeRefused
	}
	if _, err := usage.Add(context.Background(), s, rec); err != nil {
		r.logf("turns: %s turn %s: writing the usage record of the call to %s: %v", t.requestID, t.ID, m.Name, err)
	}
}

// routedChat is the route for a chat's reply, on the model picked.
func (r *Runner) routedChat(ctx context.Context, s *tenant.Store, t row, picked string) (models.Model, seam.Provider, []string, *TurnError) {
	return r.routed(ctx, s, t, routing.KindChat, picked)
}

// routedTitle is the route for a chat's title: the title rule's model,
// then the chat's own.
func (r *Runner) routedTitle(ctx context.Context, s *tenant.Store, t row, chatModel string) (models.Model, seam.Provider, []string, *TurnError) {
	return r.routed(ctx, s, t, routing.KindTitle, chatModel)
}

// answered is the model a call through p ended on: the one that answered
// or the last one that failed, when p is a route, and otherwise m.
func answered(p seam.Provider, m models.Model) models.Model { return routing.Answered(p, m) }
