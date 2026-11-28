package turn

import (
	"context"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Unattended work starts on Auto when its call names no model, the admin's
// router.auto_default is on and the person has a ladder (P14-06); in every
// other case the chat it made is left off Auto.
func TestUnattendedWorkStartsOnAutoOnlyWhenNoModelIsNamed(t *testing.T) {
	g := newRig(t)
	sv := g.serveAuto()
	ctx := context.Background()
	start := func(named string) (bool, bool) {
		t.Helper()
		var said, on bool
		g.with(g.test.ID, func(s *tenant.Store) {
			made, err := chat.Create(s)
			if err != nil {
				t.Fatal(err)
			}
			if said, err = sv.runner.StartOnAuto(ctx, s, made.ID, named); err != nil {
				t.Fatal(err)
			}
			if on, err = ChatAuto(ctx, s, made.ID); err != nil {
				t.Fatal(err)
			}
		})
		return said, on
	}

	// No ladder: Auto is not offered, and the work names the model it did.
	if said, on := start(""); said || on {
		t.Errorf("with no ladder the chat was put on Auto (%v, %v)", said, on)
	}

	g.recordedLadder()
	g.with(g.test.ID, func(s *tenant.Store) {
		if !sv.runner.UnattendedAuto(ctx, s) {
			t.Error("with a ladder unattended work does not start on Auto")
		}
	})
	if said, on := start(""); !said || !on {
		t.Errorf("with a ladder and no model named the chat is not on Auto (%v, %v)", said, on)
	}
	// A model named by the call that started the work keeps the chat off.
	if said, on := start(TestMiddleModel); said || on {
		t.Errorf("a named model put the chat on Auto (%v, %v)", said, on)
	}

	// The admin's switch that starts new chats on Auto holds for unattended
	// work too (decision 0730).
	g.adminSet(g.test.ID, KeyAutoDefault, "false")
	if said, on := start(""); said || on {
		t.Errorf("with router.auto_default off the chat was put on Auto (%v, %v)", said, on)
	}
	g.adminSet(g.test.ID, KeyAutoDefault, "true")

	// A ladder taken away again: off.
	g.adminSet(g.test.ID, router.KeyLadder, "")
	g.with(g.test.ID, func(s *tenant.Store) {
		if sv.runner.UnattendedAuto(ctx, s) {
			t.Error("with the ladder taken away unattended work still starts on Auto")
		}
	})

	// A chat that is not the tenant's is refused, as SetChatAuto refuses it.
	g.recordedLadder()
	g.with(g.test.ID, func(s *tenant.Store) {
		if _, err := sv.runner.StartOnAuto(ctx, s, "no-such-chat", ""); err == nil {
			t.Error("a chat that is not in the record was put on Auto")
		}
	})
}
