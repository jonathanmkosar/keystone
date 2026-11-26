package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// The "fixed rules" check: no setting can switch off any of the four rules
// in PLAN.md. It proves four things.
//
//  1. The rules in the settings package are the plan's, word for word.
//  2. No setting in the registry has a key in the space kept for the rules.
//  3. No override path stores a value for a rule: not a person, not the
//     admin, and not a row written straight into a tenant's database.
//  4. The packages that enforce a rule cannot read a setting at all.
//
// An item that gives a rule its code adds its package to fixedRuleGuards,
// and adds a step here if the rule needs a proof of its own.

const module = "github.com/jonathanmkosar/keystone"

// fixedRuleGuards lists, for each fixed rule, the packages whose code
// enforces it. A guard may not import the settings registry or the tenant
// overrides, itself or through another package, so there is no value it
// could read that would switch the rule off. A rule with no guard has no
// code yet.
var fixedRuleGuards = map[string][]string{
	"rules.tenant_isolation":  {"internal/tenant"},
	"rules.approval":          {}, // the approval policy, P7-02
	"rules.sensitive_content": {"internal/models", "internal/routing"},
	"rules.call_recording":    {"internal/audit"},
}

// The person map (P6-04) makes two versions of its text, and the one for a
// model that may not read sensitive content leaves every sensitive space
// out. It enforces rule 3 beside the model registry.
func init() {
	fixedRuleGuards["rules.sensitive_content"] = append(fixedRuleGuards["rules.sensitive_content"], "internal/knowledge/personmap")
}

// The tool loop (P7-01) gives the approval rule its code: the rule's test
// in internal/tools/approval, and the registry in internal/tools, which is
// the only place a tool runs and asks that test first. The approval policy
// itself (P7-02) adds to the first.
func init() {
	fixedRuleGuards["rules.approval"] = append(fixedRuleGuards["rules.approval"], "internal/tools/approval", "internal/tools")
}

// Recall (P6-05) brings content from the knowledge store into a call and
// gives the lookups their results. Found.For and Lookups take the model's
// plain answer and are the only ways to the content, so the package
// enforces rule 3 too.
func init() {
	fixedRuleGuards["rules.sensitive_content"] = append(fixedRuleGuards["rules.sensitive_content"], "internal/knowledge/recall")
}

// Embeddings (P6-08) decide which pages reach an embedding model, and
// whether a held chat's query may: embedding.MayEmbed and MayEmbedQuery take
// the registry's plain answer, and Refresh is the one way a page is sent.
// The package enforces rule 3 for embedding models.
func init() {
	fixedRuleGuards["rules.sensitive_content"] = append(fixedRuleGuards["rules.sensitive_content"], "internal/embedding")
}

// settingsPackages are the ways to read a setting's value.
var settingsPackages = []string{module + "/settings", module + "/internal/tenantsettings"}

func checkFixedRules(root string) error {
	var problems []string
	rules := settings.FixedRules()

	// 1. The list in code is the plan's.
	plan, err := os.ReadFile(filepath.Join(root, "docs", "plan", "PLAN.md"))
	if err != nil {
		return err
	}
	problems = append(problems, ruleListProblems(string(plan), rules, fixedRuleGuards)...)

	// 2. No setting is named as a rule.
	reg, err := settings.Registry()
	if err != nil {
		return err
	}
	for _, s := range reg {
		if settings.IsFixedRule(s.Key) {
			problems = append(problems, "the registry holds "+s.Key+", and a fixed rule is not a setting")
		}
	}

	// 3. No override path stores a value for a rule.
	p, err := overrideProblems(reg, rules)
	if err != nil {
		return err
	}
	problems = append(problems, p...)

	// 4. The code that enforces a rule cannot read a setting.
	for _, r := range rules {
		for _, guard := range fixedRuleGuards[r.Key] {
			p, err := guardProblems(root, r.Key, guard)
			if err != nil {
				return err
			}
			problems = append(problems, p...)
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// ruleListProblems compares the rules in code with the plan's text, and
// checks that each rule, and nothing else, has a line of guards.
func ruleListProblems(plan string, rules []settings.FixedRule, guards map[string][]string) []string {
	var problems []string
	inPlan := planRules(plan)
	if len(inPlan) != 4 {
		problems = append(problems, fmt.Sprintf("PLAN.md lists %d rules under %q, and there are four", len(inPlan), planRulesHeading))
	}
	if len(rules) != len(inPlan) {
		problems = append(problems, fmt.Sprintf("settings.FixedRules has %d rules and PLAN.md has %d", len(rules), len(inPlan)))
	}
	for i, r := range rules {
		if i < len(inPlan) && r.Rule != inPlan[i] {
			problems = append(problems, fmt.Sprintf("%s does not match rule %d in PLAN.md:\n  code: %s\n  plan: %s", r.Key, i+1, r.Rule, inPlan[i]))
		}
		if !settings.IsFixedRule(r.Key) {
			problems = append(problems, r.Key+" is not in the key space kept for the fixed rules")
		}
		if _, ok := guards[r.Key]; !ok {
			problems = append(problems, r.Key+" has no line in fixedRuleGuards in tools/verify/fixedrules.go")
		}
	}
	for key := range guards {
		known := false
		for _, r := range rules {
			known = known || r.Key == key
		}
		if !known {
			problems = append(problems, "fixedRuleGuards names "+key+", which is not a fixed rule")
		}
	}
	return problems
}

const planRulesHeading = "### Four rules no setting can switch off"

var planRuleLine = regexp.MustCompile(`^\d+\. (.+)$`)

// planRules returns the numbered rules under the plan's heading for them.
func planRules(plan string) []string {
	var rules []string
	in := false
	for _, l := range strings.Split(strings.ReplaceAll(plan, "\r\n", "\n"), "\n") {
		switch {
		case strings.TrimSpace(l) == planRulesHeading:
			in = true
		case in && strings.HasPrefix(l, "#"):
			return rules
		case in:
			if m := planRuleLine.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
				rules = append(rules, m[1])
			}
		}
	}
	return rules
}

// overrideProblems makes a tenant in a folder of its own and tries to store
// a value for each rule every way there is.
func overrideProblems(reg []settings.Setting, rules []settings.FixedRule) (problems []string, err error) {
	dir, err := os.MkdirTemp("", "keystone-verify-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.Open(dir)
	if err != nil {
		return nil, err
	}
	t, err := in.Create("Test Person One")
	if err != nil {
		return nil, err
	}
	s, err := in.Store(t.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()

	ctx := context.Background()
	for _, r := range rules {
		for _, by := range []tenantsettings.Role{tenantsettings.Person, tenantsettings.Admin} {
			for _, value := range []string{"false", "off", ""} {
				if err := tenantsettings.Set(ctx, s, reg, by, r.Key, value); !errors.Is(err, tenantsettings.ErrNotAllowed) {
					problems = append(problems, fmt.Sprintf("the %s setting %s to %q was not refused: %v", by, r.Key, value, err))
				}
			}
			if err := tenantsettings.MayChange(settings.Setting{Key: r.Key, Scope: settings.ScopeTenant}, by); err == nil {
				problems = append(problems, fmt.Sprintf("tenantsettings.MayChange lets the %s change %s", by, r.Key))
			}
		}
		if _, err := tenantsettings.Get(ctx, s, reg, r.Key); !errors.Is(err, tenantsettings.ErrNotAllowed) {
			problems = append(problems, fmt.Sprintf("%s can be read as a setting: %v", r.Key, err))
		}
		_, err := s.DB().Exec("INSERT INTO tenant_settings(key, value, set_by, updated_at) VALUES(?, 'false', 'admin', '2026-11-24T00:00:00Z')", r.Key)
		if err == nil {
			problems = append(problems, "a tenant's database stored a value for "+r.Key)
		}
	}
	var n int
	if err := s.DB().QueryRow("SELECT count(*) FROM tenant_settings").Scan(&n); err != nil {
		return nil, err
	}
	if n != 0 {
		problems = append(problems, fmt.Sprintf("%d values were stored while trying to override the fixed rules", n))
	}
	return problems, nil
}

// guardProblems reports a guard package that is missing, or that can reach
// a setting through what it imports.
func guardProblems(root, rule, guard string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(guard))); err != nil {
		return []string{fmt.Sprintf("%s: its guard %s does not exist", rule, guard)}, nil
	}
	out, err := project.Run(root, "go", "list", "-deps", "./"+guard)
	if err != nil {
		return nil, fmt.Errorf("go list -deps ./%s failed:\n%s", guard, out)
	}
	var problems []string
	for _, dep := range strings.Fields(out) {
		for _, banned := range settingsPackages {
			if dep == banned {
				problems = append(problems, fmt.Sprintf("%s: %s enforces it and can read settings through %s, so a setting could switch the rule off", rule, guard, strings.TrimPrefix(banned, module+"/")))
			}
		}
	}
	return problems, nil
}
