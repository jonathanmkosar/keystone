package settings

import "strings"

// fixedPrefix is the part of the key space kept for the fixed rules. No
// setting may have a key under it.
const fixedPrefix = "rules."

// FixedRule is one of the four rules in the plan that no setting can switch
// off. It is not a setting: it has no value, no default and no scope. Its
// key only gives it a name, in a part of the key space the registry and the
// tenant overrides both refuse.
type FixedRule struct {
	// Key names the rule, such as rules.tenant_isolation.
	Key string `json:"key"`
	// Rule is the rule, word for word from docs/plan/PLAN.md.
	Rule string `json:"rule"`
}

// FixedRules returns the four fixed rules, in the plan's order. The
// "fixed rules" check in verify fails when this list and the plan disagree.
func FixedRules() []FixedRule {
	return []FixedRule{
		{"rules.tenant_isolation", "A tenant never reads another tenant's data."},
		{"rules.approval", "A side effect outside Keystone happens only under the approval policy, and content the agent read never grants approval."},
		{"rules.sensitive_content", "Content from a sensitive space reaches only models the registry marks local or trusted."},
		{"rules.call_recording", "Every model call and every tool call is recorded: what was sent, what came back, tokens and cost."},
	}
}

// IsFixedRule reports whether key is in the part of the key space kept for
// the fixed rules. Such a key is never a setting and never has a stored
// value.
func IsFixedRule(key string) bool {
	return strings.HasPrefix(key, fixedPrefix)
}
