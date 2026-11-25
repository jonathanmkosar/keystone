package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// A chat several times its model's window is answered: the oldest turns
// are left out of the call, the call starts with the standing
// instructions, and what the call was built from is stored with the turn.
func TestAChatLongerThanTheWindowIsAnswered(t *testing.T) {
	g := newRig(t)
	sv := g.serve(nil)
	id := sv.newChat(g.ordinary.ID)
	const earlier = 40
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		m := models.Model{Name: "house-scripted", Label: "House Scripted", Adapter: models.AdapterFake, ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassLocal}
		if err := models.Add(context.Background(), s, models.Limits{MaxModels: 5}, m); err != nil {
			t.Fatal(err)
		}
		// Forty turns of about 500 tokens each, in a window of 8192.
		var parent int64
		for i := 1; i <= earlier; i++ {
			for _, role := range []seam.Role{seam.RoleUser, seam.RoleAssistant} {
				text := fmt.Sprintf("Turn %d from the %s. %s", i, role, strings.Repeat("harbour ", 90))
				msg, err := chat.Append(s, id, chat.NewMessage{ParentID: parent, Role: role, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}})
				if err != nil {
					t.Fatal(err)
				}
				parent = msg.ID
			}
		}
	})

	body, _ := json.Marshal(SendBody{Text: bakery})
	events, turnID, requestID := sv.send(g.ordinary.ID, id, string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v: %s", end, names(events))
	}

	g.with(g.ordinary.ID, func(s *tenant.Store) {
		ctx := context.Background()
		calls, err := Contexts(ctx, s, turnID)
		if err != nil || len(calls) != 1 || calls[0].Step != 1 {
			t.Fatalf("the turn's stored contexts are %+v (%v)", calls, err)
		}
		rec := calls[0].Record
		if rec.Model != "house-scripted" || rec.Window != 8192 || rec.Room != 8192-1024 || rec.Over != 0 || rec.Tokens > rec.Room {
			t.Fatalf("the record's numbers are wrong: %+v", rec)
		}
		recent := rec.Part(contextbuild.PartRecent)
		if recent.State != contextbuild.StatePartly || recent.Kept == 0 || recent.Kept+recent.Dropped != earlier {
			t.Fatalf("the earlier turns were not partly sent: %+v", recent)
		}
		if got := rec.Part(contextbuild.PartCurrent); got.Kept != 1 || rec.Part(contextbuild.PartRules).State != contextbuild.StateKept {
			t.Fatalf("the current turn and the rules are not in the record: %+v", rec.Parts)
		}

		// The record matches what the model received, which the audit log
		// holds.
		entries, err := audit.List(ctx, s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if err != nil || len(entries) != 2 {
			t.Fatalf("the model call is recorded as %d entries (%v)", len(entries), err)
		}
		sent, err := seam.DecodeRequest([]byte(entries[0].Sent))
		if err != nil {
			t.Fatal(err)
		}
		reg, err := settings.Registry()
		if err != nil {
			t.Fatal(err)
		}
		rules, _ := settings.Lookup(reg, contextbuild.KeySystemText)
		note, _ := settings.Lookup(reg, contextbuild.KeyDroppedNote)
		if len(sent.System) != 2 || sent.System[0].Text != rules.Default || sent.System[1].Text != note.Default {
			t.Fatalf("the call's system text is %+v", sent.System)
		}
		if got, want := len(sent.Messages), 2*recent.Kept+1; got != want {
			t.Fatalf("the call held %d messages, and the record says %d", got, want)
		}
		first := fmt.Sprintf("Turn %d from the user.", recent.Dropped+1)
		if !strings.HasPrefix(seam.Text(sent.Messages[0]), first) || seam.Text(sent.Messages[len(sent.Messages)-1]) != bakery {
			t.Fatalf("the call does not run from %q to the current message: it begins %.40q", first, seam.Text(sent.Messages[0]))
		}

		// A person's own system text is used on their next turn.
		if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, contextbuild.KeySystemText, "Answer in one sentence."); err != nil {
			t.Fatal(err)
		}
	})
	events, _, requestID = sv.send(g.ordinary.ID, id, string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the second turn ended as %+v", end)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if err != nil || len(entries) != 2 || !strings.Contains(entries[0].Sent, "Answer in one sentence.") {
			t.Fatalf("the person's own system text was not sent (%d entries, %v)", len(entries), err)
		}
	})
}
