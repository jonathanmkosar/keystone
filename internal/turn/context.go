package turn

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
)

// build makes the request of one model call of a turn: the context builder
// packs the branch under the model's window, with the person's own values
// of its settings, and the record of what it packed is stored beside the
// turn. A record that cannot be stored does not stop the call: the audit
// log still holds what was sent.
func (r *Runner) build(ctx context.Context, s *tenant.Store, t row, step int, m models.Model, p seam.Provider, messages []seam.Message, maxOutput, thinking int) (seam.Request, error) {
	opts, err := contextbuild.FromSettings(r.reg, func(key string) (string, bool) {
		v, err := tenantsettings.Get(ctx, s, r.reg, key)
		return v, err == nil
	})
	if err != nil {
		return seam.Request{}, err
	}
	mayRead := readsSensitive(p, m) // for the least of the route's models (P6-05)
	profile, personMap := r.person(ctx, s, t, mayRead)
	recalled, rule := r.recallFor(ctx, s, t, step, mayRead)
	opts.SystemText = withRule(opts.SystemText, rule)
	offered := r.offered(ctx, s, m)                                       // the tool loop's (P7-01)
	offered = append(offered, r.lookupTools(ctx, s, t, p, m, offered)...) // the knowledge lookups (P6-05)
	req, rec, err := contextbuild.Build(ctx, contextbuild.Input{
		Tools: offered,
		Model: m, Budget: r.o.Config.Budget, Options: opts,
		MaxOutputTokens: maxOutput, ThinkingTokens: thinking,
		Provider: p, Messages: messages,
		Profile: profile, PersonMap: personMap, Recall: recalled,
	})
	if err != nil {
		return seam.Request{}, err
	}
	if err := saveContext(s, t.ID, step, rec); err != nil {
		r.logf("turns: %s turn %s: storing what call %d was built from: %v", t.requestID, t.ID, step, err)
	}
	return req, nil
}

func saveContext(s *tenant.Store, turnID string, step int, rec contextbuild.Record) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = s.DB().Exec("INSERT OR REPLACE INTO turn_contexts (turn_id, step, record) VALUES (?, ?, ?)", turnID, step, string(raw))
	return err
}

// CallContext is what one model call of a turn was built from.
type CallContext struct {
	// Step counts the turn's model calls from 1.
	Step   int                 `json:"step"`
	Record contextbuild.Record `json:"record"`
}

// Contexts returns what each model call of a turn was built from, in the
// order the calls were made. The turn inspector (P5-04) reads it. A turn
// that made no call, or is not in this tenant's record, has none.
func Contexts(ctx context.Context, s *tenant.Store, turnID string) ([]CallContext, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT step, record FROM turn_contexts WHERE turn_id = ? ORDER BY step", turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CallContext
	for rows.Next() {
		var c CallContext
		var raw string
		if err := rows.Scan(&c.Step, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &c.Record); err != nil {
			return nil, fmt.Errorf("turn %s, call %d: its stored context does not read: %w", turnID, c.Step, err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
