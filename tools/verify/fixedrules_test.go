package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/settings"
)

func TestTheFixedRulesCheckPassesOnTheRepo(t *testing.T) {
	root, err := project.Root()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkFixedRules(root); err != nil {
		t.Fatal(err)
	}
}

func TestPlanRulesReadsTheNumberedList(t *testing.T) {
	plan := "## The door\n\n1. Not a rule.\n\n" + planRulesHeading + "\n\n1. One.\n2. Two, with a comma.\n\nA line of prose.\n3. Three.\r\n4. Four.\n\n## What is inside\n\n1. Not a rule either.\n"
	got := planRules(plan)
	want := []string{"One.", "Two, with a comma.", "Three.", "Four."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("planRules read %q, want %q", got, want)
	}
	if got := planRules("# A plan with no rules\n\n1. A step.\n"); len(got) != 0 {
		t.Errorf("planRules found rules where there is no heading for them: %q", got)
	}
}

func TestTheRuleListMustMatchThePlan(t *testing.T) {
	root, err := project.Root()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "docs", "plan", "PLAN.md"))
	if err != nil {
		t.Fatal(err)
	}
	plan := string(raw)
	rules := settings.FixedRules()
	if p := ruleListProblems(plan, rules, fixedRuleGuards); len(p) != 0 {
		t.Fatalf("the repo's own rules were refused: %v", p)
	}

	refused := func(name string, plan string, rules []settings.FixedRule, guards map[string][]string) {
		t.Helper()
		if p := ruleListProblems(plan, rules, guards); len(p) == 0 {
			t.Errorf("%s: no problem was reported", name)
		}
	}
	// The plan's wording changes and the code's does not.
	refused("a rule reworded in the plan", strings.Replace(plan, "A tenant never reads", "A tenant seldom reads", 1), rules, fixedRuleGuards)
	// A rule is dropped from the plan, or from the code.
	refused("a rule dropped from the plan", strings.Replace(plan, "4. "+rules[3].Rule, "", 1), rules, fixedRuleGuards)
	refused("a rule dropped from the code", plan, rules[:3], fixedRuleGuards)
	// The code's wording changes.
	weaker := append([]settings.FixedRule(nil), rules...)
	weaker[3].Rule = "Most model calls are recorded."
	refused("a rule reworded in the code", plan, weaker, fixedRuleGuards)
	// A rule is given a key outside the kept space, where a setting could have it.
	moved := append([]settings.FixedRule(nil), rules...)
	moved[0].Key = "tenant.isolation"
	refused("a rule keyed like a setting", plan, moved, fixedRuleGuards)
	// A rule with no line of guards, and a line for a rule that does not exist.
	fewer := map[string][]string{}
	for k, v := range fixedRuleGuards {
		if k != rules[1].Key {
			fewer[k] = v
		}
	}
	refused("a rule with no guards line", plan, rules, fewer)
	more := map[string][]string{"rules.made_up": nil}
	for k, v := range fixedRuleGuards {
		more[k] = v
	}
	refused("a guards line for no rule", plan, rules, more)
}

func TestAGuardMayNotReachSettings(t *testing.T) {
	root, err := project.Root()
	if err != nil {
		t.Fatal(err)
	}
	// Every package that enforces a rule today is clean.
	for rule, guards := range fixedRuleGuards {
		for _, g := range guards {
			if p, err := guardProblems(root, rule, g); err != nil || len(p) != 0 {
				t.Errorf("%s guard %s: %v %v", rule, g, p, err)
			}
		}
	}
	// A package that reads settings is refused as a guard: directly, and
	// through another package.
	for _, g := range []string{"internal/tenantsettings", "tools/tenant"} {
		p, err := guardProblems(root, "rules.call_recording", g)
		if err != nil {
			t.Fatal(err)
		}
		if len(p) == 0 {
			t.Errorf("%s reads settings and was accepted as a guard", g)
		}
	}
	if p, _ := guardProblems(root, "rules.call_recording", "internal/no_such_package"); len(p) == 0 {
		t.Error("a guard that does not exist was accepted")
	}
}
