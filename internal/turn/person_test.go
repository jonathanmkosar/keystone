package turn

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/knowledge/personmap"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// A person's profile is part of what a call is built from. With no
// knowledge store there is no map, and the turn still runs.
func TestTheProfileIsInTheCall(t *testing.T) {
	g := newRig(t)
	sv := g.serve(nil)
	id := sv.newChat(g.ordinary.ID)
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		m := models.Model{Name: "house-scripted", Label: "House Scripted", Adapter: models.AdapterFake, ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassLocal}
		if err := models.Add(context.Background(), s, models.Limits{MaxModels: 5}, m); err != nil {
			t.Fatal(err)
		}
		if _, err := personmap.SetProfile(context.Background(), s, "Test Person One bakes bread and walks by the harbour.", 2000); err != nil {
			t.Fatal(err)
		}
	})

	body, _ := json.Marshal(SendBody{Text: bakery})
	events, turnID, _ := sv.send(g.ordinary.ID, id, string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v: %s", end, names(events))
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		calls, err := Contexts(context.Background(), s, turnID)
		if err != nil || len(calls) != 1 {
			t.Fatalf("the turn's stored contexts are %+v (%v)", calls, err)
		}
		rec := calls[0].Record
		if got := rec.Part(contextbuild.PartProfile); got.State != contextbuild.StateKept || got.Tokens == 0 {
			t.Errorf("the profile is not in the call: %+v", got)
		}
		if got := rec.Part(contextbuild.PartPersonMap); got.Tokens != 0 {
			t.Errorf("a map was sent with no knowledge store: %+v", got)
		}
	})
}
