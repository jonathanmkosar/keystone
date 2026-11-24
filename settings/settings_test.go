package settings

import (
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

func TestRegistryLoadsAndIsSorted(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg) == 0 {
		t.Fatal("the registry is empty")
	}
	keys := make([]string, len(reg))
	for i, s := range reg {
		keys[i] = s.Key
		if s.Lane == "" {
			t.Errorf("%s has no lane", s.Key)
		}
	}
	if !sort.StringsAreSorted(keys) {
		t.Errorf("keys are not sorted: %v", keys)
	}
	for _, want := range []string{"server.address", "data.dir", "appearance.theme"} {
		if _, ok := Lookup(reg, want); !ok {
			t.Errorf("%s is missing from the registry", want)
		}
	}
	if _, ok := Lookup(reg, "no.such.setting"); ok {
		t.Error("Lookup found a setting that does not exist")
	}
}

func TestLaneFilesAreMerged(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	lanes := map[string]bool{}
	for _, s := range reg {
		lanes[s.Lane] = true
	}
	if !lanes["core"] || !lanes["interface"] {
		t.Errorf("want settings from the core and interface lanes, got %v", lanes)
	}
}

func file(lane, body string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(`{"lane":"` + lane + `","settings":[` + body + `]}`)}
}

const good = `{"key":"a.b","type":"int","default":"3","scope":"tenant","description":"A number."}`

func TestReadRefusesBadFiles(t *testing.T) {
	cases := []struct {
		name, want string
		fsys       fstest.MapFS
	}{
		{"lane and file name disagree", "file name says", fstest.MapFS{"core.json": file("models", good)}},
		{"a key defined twice", "already defined", fstest.MapFS{"core.json": file("core", good), "models.json": file("models", good)}},
		{"an unknown field", "unknown field", fstest.MapFS{"core.json": file("core", `{"key":"a.b","type":"int","default":"3","scope":"tenant","description":"A number.","secret":"x"}`)}},
		{"a setting naming another lane", "inside the", fstest.MapFS{"core.json": file("core", `{"key":"a.b","lane":"models","type":"int","default":"3","scope":"tenant","description":"A number."}`)}},
	}
	for _, c := range cases {
		_, err := Read(c.fsys)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error containing %q, got %v", c.name, c.want, err)
		}
	}
	if reg, err := Read(fstest.MapFS{"core.json": file("core", good)}); err != nil || len(reg) != 1 || reg[0].Lane != "core" {
		t.Errorf("a good file was refused or misread: %v %v", reg, err)
	}
}

func TestValidate(t *testing.T) {
	ok := Setting{Key: "spend.cap", Lane: "models", Type: "float", Default: "100", Scope: ScopeTenantAdmin, Description: "The cap."}
	if err := Validate(ok); err != nil {
		t.Fatalf("a good setting was refused: %v", err)
	}
	change := func(f func(*Setting)) Setting { s := ok; f(&s); return s }
	bad := map[string]Setting{
		"key with no dot":             change(func(s *Setting) { s.Key = "cap" }),
		"key in capitals":             change(func(s *Setting) { s.Key = "Spend.Cap" }),
		"unknown type":                change(func(s *Setting) { s.Type = "money" }),
		"default of the wrong type":   change(func(s *Setting) { s.Default = "lots" }),
		"options on a number":         change(func(s *Setting) { s.Options = []string{"100"} }),
		"unknown scope":               change(func(s *Setting) { s.Scope = "everyone" }),
		"no description":              change(func(s *Setting) { s.Description = " " }),
		"description not a sentence":  change(func(s *Setting) { s.Description = "the cap" }),
		"default outside its options": {Key: "a.theme", Type: "string", Default: "blue", Options: []string{"dark", "light"}, Scope: ScopeTenant, Description: "The theme."},
		"bad duration":                {Key: "a.wait", Type: "duration", Default: "soon", Scope: ScopeInstall, Description: "How long."},
		"bad bool":                    {Key: "a.on", Type: "bool", Default: "yes please", Scope: ScopeInstall, Description: "On or off."},
	}
	for name, s := range bad {
		if err := Validate(s); err == nil {
			t.Errorf("%s: was accepted", name)
		}
	}
}

func TestScopeDecidesWhoMayChange(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	theme, _ := Lookup(reg, "appearance.theme")
	addr, _ := Lookup(reg, "server.address")
	if !TenantMayOverride(theme) {
		t.Error("a person should be able to change their own theme")
	}
	if TenantMayOverride(addr) {
		t.Error("a person must not be able to change the install's address")
	}
}

func TestCheckValue(t *testing.T) {
	reg, _ := Registry()
	theme, _ := Lookup(reg, "appearance.theme")
	if err := CheckValue(theme, "light"); err != nil {
		t.Errorf("light was refused: %v", err)
	}
	if err := CheckValue(theme, "purple"); err == nil {
		t.Error("purple was accepted as a theme")
	}
	n := Setting{Key: "a.n", Type: "int"}
	if err := CheckValue(n, "seven"); err == nil {
		t.Error("seven was accepted as an int")
	}
}
