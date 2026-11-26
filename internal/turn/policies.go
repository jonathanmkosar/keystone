package turn

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools/approval"
)

// Each person's policy for each tool (P7-02): always, ask or never. A tool
// with no stored policy has its default, always for a tool that only reads
// and ask for one that changes something. The policy is kept in the
// tenant's tool_policies table. It is not a setting: tools come and go
// with connectors, and the rule that reads it, approval.Decide, is fixed
// rule 2's guard and reads no setting.

// ToolPolicy is one tool as the approvals screen lists it.
type ToolPolicy struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// ReadOnly says the tool only reads. Inside says its result is
	// Keystone's own and not outside content.
	ReadOnly bool `json:"read_only"`
	Inside   bool `json:"inside"`
	// ReachesOthers says some calls of the tool reach other people, and
	// those always ask, whatever the policy (P12-02).
	ReachesOthers bool `json:"reaches_others"`
	// Default is the policy the tool has until the person sets one.
	Default string `json:"default"`
	// Policy is the one in force: the person's own, or the default.
	Policy string `json:"policy"`
	// Overridden says the policy is the person's own.
	Overridden bool `json:"overridden"`
}

// ToolsReply answers GET tools.
type ToolsReply struct {
	// Tools are by name.
	Tools []ToolPolicy `json:"tools"`
	// Policies are the three a tool may have.
	Policies []string `json:"policies"`
}

// PolicyBody sets a tool's policy.
type PolicyBody struct {
	// Policy is always, ask or never.
	Policy string `json:"policy"`
}

// toolPolicies returns the person's own policy for each tool that has one.
// ok is false when they could not be read: every call then asks.
func (r *Runner) toolPolicies(ctx context.Context, s *tenant.Store) (map[string]approval.Policy, bool) {
	out := map[string]approval.Policy{}
	rows, err := s.DB().QueryContext(ctx, "SELECT tool, policy FROM tool_policies")
	if err != nil {
		r.logf("turns: reading the tool policies of tenant %s, so every call asks: %v", s.TenantID(), err)
		return out, false
	}
	defer rows.Close()
	for rows.Next() {
		var name, p string
		if err := rows.Scan(&name, &p); err != nil {
			r.logf("turns: reading the tool policies of tenant %s, so every call asks: %v", s.TenantID(), err)
			return out, false
		}
		out[name] = approval.Policy(p)
	}
	if err := rows.Err(); err != nil {
		r.logf("turns: reading the tool policies of tenant %s, so every call asks: %v", s.TenantID(), err)
		return out, false
	}
	return out, true
}

// withoutNever leaves out of a call's tools those the person has set never
// to run. A call the model makes to one anyway is refused.
func (r *Runner) withoutNever(ctx context.Context, s *tenant.Store, defs []seam.Tool) []seam.Tool {
	pols, _ := r.toolPolicies(ctx, s)
	var out []seam.Tool
	for _, d := range defs {
		if pols[d.Name] != approval.Never {
			out = append(out, d)
		}
	}
	return out
}

// knownTools is every tool a person can set a policy for: the registry's,
// the three knowledge lookups, and the tools of the person's connectors.
func (r *Runner) knownTools(ctx context.Context, s *tenant.Store) []ToolPolicy {
	var out []ToolPolicy
	for _, t := range r.o.Tools.List() {
		out = append(out, ToolPolicy{Name: t.Name, Description: t.Description, ReadOnly: t.ReadOnly, Inside: t.Inside, ReachesOthers: t.ReachesOthers})
	}
	if list, err := recall.Lookups(false, recall.LookupOptions{MaxResults: 10, MaxChars: 6000}); err == nil {
		for _, l := range list {
			out = append(out, ToolPolicy{Name: l.Name, Description: l.Description, ReadOnly: l.ReadOnly, Inside: lookupInside(l.Name)})
		}
	}
	out = append(out, r.knownConnectorTools(ctx, s)...) // P7-04 (connectors.go)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// lookupInside says whether a knowledge lookup's result is Keystone's own.
// A page is: the person and Keystone's upkeep write it. A source is not:
// it was brought in from outside. A search may name sources, so it counts
// as outside until it has run, and then by what it found (noteOutside).
func lookupInside(name string) bool { return name == recall.LookupReadPage }

func (r *Runner) policyRows(ctx context.Context, s *tenant.Store) ([]ToolPolicy, error) {
	pols, ok := r.toolPolicies(ctx, s)
	if !ok {
		return nil, errors.New("the tool policies could not be read")
	}
	out := r.knownTools(ctx, s)
	for i := range out {
		out[i].Default = string(approval.Default(out[i].ReadOnly))
		out[i].Policy = out[i].Default
		if p, set := pols[out[i].Name]; set {
			out[i].Policy, out[i].Overridden = string(p), true
		}
	}
	return out, nil
}

func (r *Runner) listTools(w http.ResponseWriter, req *http.Request, c api.Call) {
	rows, err := r.policyRows(req.Context(), c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply := ToolsReply{Tools: rows, Policies: []string{}}
	if reply.Tools == nil {
		reply.Tools = []ToolPolicy{}
	}
	for _, p := range approval.Policies {
		reply.Policies = append(reply.Policies, string(p))
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

// oneTool answers with one tool's row, or 404 when no tool has the name.
func (r *Runner) oneTool(w http.ResponseWriter, req *http.Request, c api.Call, name string) {
	rows, err := r.policyRows(req.Context(), c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	for _, t := range rows {
		if t.Name == name {
			api.WriteJSON(w, http.StatusOK, t)
			return
		}
	}
	api.Error(w, req, http.StatusNotFound, "not_found", "there is no tool of that name")
}

func (r *Runner) known(ctx context.Context, s *tenant.Store, name string) bool {
	for _, t := range r.knownTools(ctx, s) {
		if t.Name == name {
			return true
		}
	}
	return false
}

func (r *Runner) setPolicy(w http.ResponseWriter, req *http.Request, c api.Call) {
	name := req.PathValue("name")
	if !r.known(req.Context(), c.Store, name) {
		api.Error(w, req, http.StatusNotFound, "not_found", "there is no tool of that name")
		return
	}
	var body PolicyBody
	if !api.ReadJSON(w, req, &body) {
		return
	}
	p, err := approval.ParsePolicy(body.Policy)
	if err != nil {
		refused(w, req, err.Error())
		return
	}
	if _, err := c.Store.DB().ExecContext(req.Context(),
		"INSERT INTO tool_policies(tool, policy, updated_at) VALUES(?, ?, ?) ON CONFLICT(tool) DO UPDATE SET policy = excluded.policy, updated_at = excluded.updated_at",
		name, string(p), stamp()); err != nil {
		r.answer(w, req, c, err)
		return
	}
	r.oneTool(w, req, c, name)
}

func (r *Runner) clearPolicy(w http.ResponseWriter, req *http.Request, c api.Call) {
	name := req.PathValue("name")
	if !r.known(req.Context(), c.Store, name) {
		api.Error(w, req, http.StatusNotFound, "not_found", "there is no tool of that name")
		return
	}
	if _, err := c.Store.DB().ExecContext(req.Context(), "DELETE FROM tool_policies WHERE tool = ?", name); err != nil {
		r.answer(w, req, c, err)
		return
	}
	r.oneTool(w, req, c, name)
}
