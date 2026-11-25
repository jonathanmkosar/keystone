package knowledge

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// Only "normal" lets content reach a hosted model. A label that is blank,
// misspelt or new reads as sensitive.
func TestOnlyNormalIsNotSensitive(t *testing.T) {
	for label, want := range map[Label]bool{
		LabelNormal:    false,
		LabelSensitive: true,
		"":             true,
		"Normal":       true,
		"private":      true,
	} {
		if got := label.Sensitive(); got != want {
			t.Errorf("Label(%q).Sensitive() = %v, want %v", label, got, want)
		}
	}
	// The zero value of every type that carries content is sensitive.
	if !(Entry{}).Label.Sensitive() || !(Page{}).Label.Sensitive() || !(Source{}).Label.Sensitive() ||
		!(Hit{}).Label.Sensitive() || !(PageNeed{}).Label.Sensitive() {
		t.Error("a value with no label must read as sensitive")
	}
}

func TestCheckNewSource(t *testing.T) {
	good := NewSource{Space: "notes", Name: "chat-facts", Facts: []Fact{{Text: "An invented fact."}}}
	if err := CheckNewSource(good); err != nil {
		t.Errorf("a source with a space, a name and a fact was refused: %v", err)
	}
	noSpace, noName, noFacts, blank := good, good, good, good
	noSpace.Space = " "
	noName.Name = ""
	noFacts.Facts = nil
	blank.Facts = []Fact{{Text: "An invented fact."}, {Text: "\n"}}
	for name, n := range map[string]NewSource{"no space": noSpace, "no name": noName, "no facts": noFacts, "a blank fact": blank} {
		if err := CheckNewSource(n); !errors.Is(err, ErrInvalid) {
			t.Errorf("a source with %s: error %v, want ErrInvalid", name, err)
		}
	}
}

// The setting that names a tenant's adapter is in the registry, a person may
// change it for themselves, and with nothing set Keystone runs with no store.
func TestTheDefaultAdapterIsNone(t *testing.T) {
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := settings.Lookup(reg, SettingAdapter)
	if !ok {
		t.Fatalf("the registry has no %s setting", SettingAdapter)
	}
	if s.Default != None {
		t.Errorf("%s defaults to %q, want %q", SettingAdapter, s.Default, None)
	}
	if !settings.TenantMayOverride(s) {
		t.Errorf("%s has scope %q: a person chooses their own knowledge store", SettingAdapter, s.Scope)
	}
	// Every adapter in this build can be chosen.
	for _, name := range Adapters() {
		if err := settings.CheckValue(s, name); err != nil {
			t.Errorf("adapter %q is registered and cannot be chosen: %v", name, err)
		}
	}
	// The default opens with no tenant handle, no folder and no network.
	store, err := Open(context.Background(), s.Default, nil)
	if err != nil {
		t.Fatalf("the default adapter does not open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Error(err)
	}
}

func TestOpenRefusesAnUnknownAdapter(t *testing.T) {
	_, err := Open(context.Background(), "no-such-adapter", nil)
	if err == nil || !strings.Contains(err.Error(), "no-such-adapter") || !strings.Contains(err.Error(), None) {
		t.Errorf("error %v, want one that names the adapter asked for and the ones this build has", err)
	}
}

func TestOpenStopsOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Open(ctx, None, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("error %v, want the context's", err)
	}
}

// register adds an adapter for one test and takes it out again, so the
// registry other tests see holds only what the build has.
func register(t *testing.T, name string, open Opener) {
	t.Helper()
	Register(name, open)
	t.Cleanup(func() {
		mu.Lock()
		delete(openers, name)
		mu.Unlock()
	})
}

func TestRegisterAndOpen(t *testing.T) {
	failed := errors.New("the address is not set")
	var got *tenant.Store
	handle := &tenant.Store{}
	register(t, "test-works", func(_ context.Context, ts *tenant.Store) (Store, error) {
		got = ts
		return none{}, nil
	})
	register(t, "test-fails", func(context.Context, *tenant.Store) (Store, error) { return nil, failed })
	register(t, "test-empty", func(context.Context, *tenant.Store) (Store, error) { return nil, nil })

	if !slices.Equal(Adapters(), []string{None, "test-empty", "test-fails", "test-works"}) {
		t.Errorf("Adapters() = %v, want them sorted by name", Adapters())
	}
	if s, err := Open(context.Background(), "test-works", handle); err != nil || s == nil {
		t.Errorf("Open: %v, %v", s, err)
	}
	if got != handle {
		t.Error("the opener was not given the tenant's handle")
	}
	if _, err := Open(context.Background(), "test-fails", nil); !errors.Is(err, failed) {
		t.Errorf("error %v, want the opener's own", err)
	}
	if s, err := Open(context.Background(), "test-empty", nil); err == nil {
		t.Errorf("an opener that returned no store and no error gave %v", s)
	}
}

func TestRegisterRefusesAMistake(t *testing.T) {
	open := func(context.Context, *tenant.Store) (Store, error) { return none{}, nil }
	for name, try := range map[string]func(){
		"a name already taken": func() { Register(None, open) },
		"an empty name":        func() { Register("", open) },
		"a name with a space":  func() { Register("Built In", open) },
		"no opener":            func() { Register("test-no-opener", nil) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Register with %s did not panic", name)
				}
			}()
			try()
		}()
	}
	if !slices.Equal(Adapters(), []string{None}) {
		t.Errorf("a refused registration left %v", Adapters())
	}
}
