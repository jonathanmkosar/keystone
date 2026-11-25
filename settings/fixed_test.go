package settings

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestFixedRulesAreFourAndNamed(t *testing.T) {
	rules := FixedRules()
	if len(rules) != 4 {
		t.Fatalf("there are %d fixed rules, want 4", len(rules))
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if !keyShape.MatchString(r.Key) || !IsFixedRule(r.Key) {
			t.Errorf("%q is not a key under %s", r.Key, fixedPrefix)
		}
		if seen[r.Key] {
			t.Errorf("%s is listed twice", r.Key)
		}
		seen[r.Key] = true
		if !strings.HasSuffix(r.Rule, ".") {
			t.Errorf("%s: the rule is not a full sentence", r.Key)
		}
	}
}

func TestNoSettingMayBeNamedAsAFixedRule(t *testing.T) {
	for _, r := range FixedRules() {
		s := Setting{Key: r.Key, Type: "bool", Default: "true", Scope: ScopeTenant, Description: "Switches a rule."}
		if err := Validate(s); err == nil {
			t.Errorf("a setting named %s was accepted", r.Key)
		}
		body := `{"key":"` + r.Key + `","type":"bool","default":"true","scope":"install","description":"Switches a rule."}`
		if _, err := Read(fstest.MapFS{"core.json": file("core", body)}); err == nil || !strings.Contains(err.Error(), "fixed rules") {
			t.Errorf("a lane file with a setting named %s was read: %v", r.Key, err)
		}
	}
	// The whole space is kept, not only the four names in use.
	if err := Validate(Setting{Key: "rules.anything_else", Type: "bool", Default: "true", Scope: ScopeInstall, Description: "Switches a rule."}); err == nil {
		t.Error("a setting under rules. was accepted")
	}
	// A key that only looks alike is an ordinary setting.
	if err := Validate(Setting{Key: "rulesets.strict", Type: "bool", Default: "true", Scope: ScopeInstall, Description: "An ordinary setting."}); err != nil {
		t.Errorf("rulesets.strict was refused: %v", err)
	}
}

func TestTheRegistryHoldsNoFixedRule(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range reg {
		if IsFixedRule(s.Key) {
			t.Errorf("%s is in the registry", s.Key)
		}
	}
}
