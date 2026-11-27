package main

import "testing"

func TestTheAdminDenyList(t *testing.T) {
	for path, want := range map[string]bool{
		keystoneModule + "internal/chat":                  true,
		keystoneModule + "internal/turn":                  true,
		keystoneModule + "internal/knowledge":             true,
		keystoneModule + "internal/knowledge/memory":      true,
		keystoneModule + "internal/tools/builtin":         true,
		keystoneModule + "internal/share":                 true,
		keystoneModule + "internal/usagereport":           false, // not on the list; reaches knowledge, so held all the same
		keystoneModule + "internal/usage":                 false,
		keystoneModule + "internal/tenant":                false,
		keystoneModule + "internal/audit":                 false,
		keystoneModule + "internal/signin":                false,
		keystoneModule + "internal/chatty":                false, // a name that only starts like one
		keystoneModule + "internal/knowledgeable":         false,
		"example.com/elsewhere/internal/chat":             false,
		keystoneModule + "internal/mcp/mcptest":           true,
		keystoneModule + "internal/subagents/carry":       true,
		keystoneModule + "internal/research/cite":         true,
		keystoneModule + "internal/wikijobs/scope":        true,
		keystoneModule + "internal/importer/importertest": true,
	} {
		if got := forbidden(path); got != want {
			t.Errorf("forbidden(%s) = %v, want %v", path, got, want)
		}
	}
}

func TestTheChainNamesTheWay(t *testing.T) {
	m := keystoneModule
	imports := map[string][]string{
		m + "internal/adminview":        {m + "internal/usagereport", "fmt"},
		m + "internal/usagereport":      {m + "internal/knowledge/memory"},
		m + "internal/knowledge/memory": {m + "internal/knowledge"},
	}
	got := chain(imports, m+"internal/adminview", m+"internal/knowledge")
	want := "internal/adminview -> internal/usagereport -> internal/knowledge/memory -> internal/knowledge"
	if got != want {
		t.Errorf("chain = %q, want %q", got, want)
	}
}
