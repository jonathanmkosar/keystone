package knowledgetest

import (
	"slices"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
)

var every = knowledge.Capabilities{AddSource: true, CatalogMarker: true, VectorSearch: true, PageVectors: true}

// A correct store passes, whichever capabilities it has. Each flag is tried
// alone, so both the "set" and the "not set" side of every flag is run.
func TestCorrectStorePasses(t *testing.T) {
	for name, caps := range map[string]knowledge.Capabilities{
		"every capability":    every,
		"no capability":       {},
		"add source only":     {AddSource: true},
		"catalog marker only": {CatalogMarker: true},
		"vector search only":  {VectorSearch: true},
		"page vectors only":   {PageVectors: true},
	} {
		t.Run(name, func(t *testing.T) {
			Run(t, memoryHarness(caps, ""))
		})
	}
}

// A store that can be given nothing passes with the seeded checks skipped.
func TestStoreWithNoSeedPasses(t *testing.T) {
	h := memoryHarness(knowledge.Capabilities{}, "")
	h.Seed = nil
	Run(t, h)
}

// problems runs every check and returns what each found wrong, by check.
func problems(t *testing.T, h Harness) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, c := range checks {
		if found, _ := runCheck(t, h, c); len(found) > 0 {
			out[c.name] = found
		}
	}
	return out
}

// A store with one fault fails, in the check that is there to catch it.
// Without this the suite could pass everything and nobody would know.
func TestFaultyStoreFails(t *testing.T) {
	for _, tc := range []struct{ fault, caughtBy string }{
		{faultBlankLabel, "catalog"},
		{faultBlankLabel, "search"},
		{faultAllNormal, "catalog"},
		{faultAllNormal, "read page and source"},
		{faultAllNormal, "search"},
		{faultAllNormal, "add source"},
		{faultAllNormal, "page vectors"},
		{faultKeepsFirstLabel, "space made sensitive later"},
		{faultIgnoresCancel, "cancelled context"},
		{faultFindsAnything, "fresh store"},
		{faultUnsorted, "catalog"},
		{faultIgnoresSpace, "search"},
		{faultIgnoresLimit, "search"},
		{faultWorstFirst, "vector search"},
		{faultClaimsAddSource, "add source"},
		{faultDropsAdded, "add source"},
		{faultFrozenMarker, "catalog marker"},
		{faultIgnoresVector, "vector search"},
		{faultAcceptsStale, "page vectors"},
		{faultForgetsStale, "page vectors"},
	} {
		t.Run(tc.fault+", caught by "+tc.caughtBy, func(t *testing.T) {
			found := problems(t, memoryHarness(every, tc.fault))
			if len(found[tc.caughtBy]) == 0 {
				t.Errorf("the %q check passed a store that %s; the checks that failed: %v", tc.caughtBy, tc.fault, found)
			}
		})
	}
}

// A store that claims a capability must be seeded, so its claim is checked.
func TestStoreWithNoSeedMayClaimNothing(t *testing.T) {
	h := memoryHarness(every, "")
	h.Seed = nil
	if found := problems(t, h); len(found["capabilities"]) == 0 {
		t.Errorf("the suite passed a store that claims every capability and cannot be seeded: %v", found)
	}
}

// A flag that is set and not honoured is caught for each flag in turn.
func TestUnsupportedCallsAreChecked(t *testing.T) {
	// A store with no capability whose flags say it has them all: every
	// optional call answers "not supported".
	h := Harness{
		New: func(*testing.T) knowledge.Store {
			return claims{newMemory(knowledge.Capabilities{}, ""), every}
		},
		Seed: func(_ *testing.T, s knowledge.Store, c Content) { s.(claims).memory.seed(c) },
	}
	found := problems(t, h)
	for _, name := range []string{"catalog marker", "add source", "vector search", "page vectors"} {
		if len(found[name]) == 0 {
			t.Errorf("the %q check passed a store that sets the flag and does not support the call", name)
		}
	}
}

// claims is a memory store that reports other capabilities than it has.
type claims struct {
	*memory
	caps knowledge.Capabilities
}

func (c claims) Capabilities() knowledge.Capabilities { return c.caps }

// The memory store is for this package's tests only. It is not an adapter a
// tenant can choose.
func TestMemoryStoreIsNotAnAdapter(t *testing.T) {
	if got := knowledge.Adapters(); !slices.Equal(got, []string{knowledge.None}) {
		t.Errorf("the adapters in this build are %v, want only %q", got, knowledge.None)
	}
}
