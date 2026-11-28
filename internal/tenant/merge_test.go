package tenant

import (
	"context"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/secrets"
)

// A person added by another process while this one runs, as the command
// line adds one while the server runs, is kept when this one saves the
// registry, and served (P15-02, decision 0704).
func TestASaveKeepsAPersonAddedElsewhere(t *testing.T) {
	dir := t.TempDir()
	k := secrets.NewMemoryKeeper()
	server, err := OpenWithKeeper(dir, k)
	if err != nil {
		t.Fatal(err)
	}
	first, err := server.Create("Merge Person One")
	if err != nil {
		t.Fatal(err)
	}
	command, err := OpenWithKeeper(dir, k)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := command.Create("Merge Person Two")
	if err != nil {
		t.Fatal(err)
	}
	// The server, which does not know the second, adds a third and saves.
	third, err := server.Create("Merge Person Three")
	if err != nil {
		t.Fatal(err)
	}
	again, err := OpenWithKeeper(dir, k)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, tn := range again.Tenants() {
		ids[tn.ID] = true
	}
	if len(ids) != 3 || !ids[first.ID] || !ids[elsewhere.ID] || !ids[third.ID] {
		t.Fatalf("the registry on disk holds %v, want all three", again.Tenants())
	}
	s, err := server.Store(elsewhere.ID)
	if err != nil {
		t.Fatalf("the server does not serve the merged person: %v", err)
	}
	s.Close()
}

func TestDisabledReadsTheMark(t *testing.T) {
	in, err := OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	p, err := in.Create("Disabled Person One")
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, off, err := s.Disabled(ctx); off || err != nil {
		t.Fatalf("a new person reads disabled %v, %v", off, err)
	}
	at := time.Date(2026, 11, 28, 11, 0, 0, 0, time.UTC)
	if _, err := s.DB().Exec("INSERT INTO tenant_meta(key, value) VALUES(?, ?)", DisabledKey, DisabledStamp(at)); err != nil {
		t.Fatal(err)
	}
	if got, off, err := s.Disabled(ctx); !off || err != nil || !got.Equal(at) {
		t.Errorf("a marked person reads %v %v, %v", got, off, err)
	}
}
