package adminview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/share"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/unattended"
)

var testSignIn = signin.Options{PasswordIterations: 50, PasswordMinLength: 12, SessionLifetime: time.Hour, FailuresBeforeWait: 3, FailureWait: time.Minute}

// hooks counts the switch's calls, as the task runner and the scheduler
// would get them.
type hooks struct{ pause, rearm, resume int }

func (h *hooks) options() Options {
	return Options{
		Pause:        func(context.Context, *tenant.Store) error { h.pause++; return nil },
		Rearm:        func(context.Context, *tenant.Store) error { h.rearm++; return nil },
		Resume:       func(context.Context, *tenant.Store) error { h.resume++; return nil },
		SetupCodeFor: time.Hour,
	}
}

func store(t *testing.T, in *tenant.Install, id string) *tenant.Store {
	t.Helper()
	s, err := in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// The item's proof: a disabled person cannot sign in, their sessions end,
// their schedules do not run and their share links answer as revoked;
// enabling restores it. Both logs have the change with the old and the new
// value.
func TestADisabledPersonIsShutOutAndEnablingRestoresThem(t *testing.T) {
	ctx := context.Background()
	in, admin, _, two := setup(t)
	h := &hooks{}
	v := NewWith(in, h.options())
	a := Asker{AdminID: admin.ID, RequestID: "fedcba9876543210", Action: "POST /api/v1/admin/people/" + two.ID + "/disable"}
	s := store(t, in, two.ID)

	// Before: a session, a live share link and a schedule due now.
	token, _, err := signin.SignIn(s, passwordTwo, testSignIn)
	if err != nil {
		t.Fatal(err)
	}
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Append(s, c.ID, chat.NewMessage{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "Harbour tides"}}}); err != nil {
		t.Fatal(err)
	}
	_, link, err := share.Create(ctx, s, c.ID, share.Draft{MaxOpen: 10})
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(-time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	exec(t, s, `INSERT INTO schedules(id, name, prompt, kind, hour, minute, next_at, created_at, updated_at) VALUES('morning', 'Morning note', 'Write it', 'daily', 7, 0, ?, ?, ?)`, due, due, due)

	p, err := v.SetEnabled(ctx, a, two.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Enabled || p.DisabledAt.IsZero() || h.pause != 1 {
		t.Errorf("after disabling: %+v, pause called %d times", p, h.pause)
	}
	if _, err := signin.Check(s, token); !errors.Is(err, signin.ErrNoSession) {
		t.Errorf("a session of a disabled person: %v", err)
	}
	if _, _, err := signin.SignIn(s, passwordTwo, testSignIn); !errors.Is(err, signin.ErrDisabled) {
		t.Errorf("a disabled person's right password: %v", err)
	}
	if _, _, err := signin.SignIn(s, "not the phrase at all", testSignIn); !errors.Is(err, signin.ErrRefused) {
		t.Errorf("a wrong password for a disabled person: %v, want the plain refusal", err)
	}
	if on, err := unattended.Paused(ctx, s); !on || err != nil {
		t.Errorf("the pause switch of a disabled person reads %v, %v", on, err)
	}
	found, err := share.Get(ctx, s, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := share.Usable(ctx, s, found); !errors.Is(err, share.ErrRevoked) {
		t.Errorf("a disabled person's share link: %v", err)
	}
	// The scheduler holds the schedule: the due time passes, no run.
	sc := schedules.New(schedules.Options{Install: in})
	sc.Tick(ctx)
	var runs int
	var next string
	s.DB().QueryRow("SELECT count(*) FROM schedule_runs").Scan(&runs)
	s.DB().QueryRow("SELECT next_at FROM schedules WHERE id = 'morning'").Scan(&next)
	if runs != 0 || next == due {
		t.Errorf("a disabled person's schedule: %d runs, next %s (was %s)", runs, next, due)
	}
	// A setup code is not made for them.
	if _, err := v.NewSetupCode(ctx, Asker{AdminID: admin.ID, RequestID: "fedcba9876543211", Action: "POST /api/v1/admin/people/" + two.ID + "/setup-code"}, two.ID); !errors.Is(err, ErrDisabled) {
		t.Errorf("a setup code for a disabled person: %v", err)
	}

	// Enabling restores sign-in, the switch and the link.
	a.RequestID, a.Action = "fedcba9876543212", "POST /api/v1/admin/people/"+two.ID+"/enable"
	if p, err = v.SetEnabled(ctx, a, two.ID, true); err != nil {
		t.Fatal(err)
	}
	if !p.Enabled || !p.DisabledAt.IsZero() || h.rearm != 1 || h.resume != 1 {
		t.Errorf("after enabling: %+v, rearm %d, resume %d", p, h.rearm, h.resume)
	}
	if _, _, err := signin.SignIn(s, passwordTwo, testSignIn); err != nil {
		t.Errorf("an enabled person's sign-in: %v", err)
	}
	if on, err := unattended.Paused(ctx, s); on || err != nil {
		t.Errorf("the pause switch after enabling reads %v, %v", on, err)
	}
	if err := share.Usable(ctx, s, found); err != nil {
		t.Errorf("the share link after enabling: %v", err)
	}
	// Enabling an enabled person changes nothing.
	if _, err := v.SetEnabled(ctx, a, two.ID, true); err != nil || h.rearm != 1 {
		t.Errorf("enabling twice: %v, rearm %d", err, h.rearm)
	}

	// The person's log: each change between started and ok, with the old
	// and the new value and the admin's name.
	var changes []Touch
	for _, e := range logOf(t, in, two.ID) {
		if !strings.HasPrefix(e.Actor, ActorPrefix) || e.Outcome != audit.OutcomeStarted {
			continue
		}
		var touch Touch
		json.Unmarshal([]byte(e.Sent), &touch)
		if touch.Key == "enabled" {
			changes = append(changes, touch)
		}
	}
	if len(changes) != 3 || changes[0].Old != "true" || changes[0].New != "false" || changes[1].New != "true" || changes[0].By != "Test Admin" {
		t.Errorf("the changes in the person's log: %+v", changes)
	}
}

// A setup code works once, for its one person, and not after it expires
// or after a newer one; the admin's view refuses what it was not given.
func TestASetupCodeWorksOnce(t *testing.T) {
	ctx := context.Background()
	in, admin, one, two := setup(t)
	v := NewWith(in, (&hooks{}).options())
	a := Asker{AdminID: admin.ID, RequestID: "0011223344556677", Action: "POST /api/v1/admin/people"}

	p, code, err := v.AddPerson(ctx, a, "  Test Person Three ")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Test Person Three" || len(adminaccount.NormalizeCode(code.Code)) != 20 || !code.Expires.After(time.Now()) {
		t.Fatalf("the new person %+v, code %+v", p, code)
	}
	db, err := adminaccount.Open(in.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	if id, err := db.SetupCodeFor(strings.ToLower(code.Code), now); err != nil || id != p.ID {
		t.Errorf("the code, typed in lower case, is for %q: %v", id, err)
	}
	if err := db.UseSetupCode(code.Code, one.ID, now); !errors.Is(err, adminaccount.ErrNoCode) {
		t.Errorf("the code used for another person: %v", err)
	}
	if err := db.UseSetupCode(strings.ReplaceAll(code.Code, "-", " "), p.ID, now); err != nil {
		t.Errorf("the code used once: %v", err)
	}
	if err := db.UseSetupCode(code.Code, p.ID, now); !errors.Is(err, adminaccount.ErrNoCode) {
		t.Errorf("the code used twice: %v", err)
	}

	// Expired, and replaced.
	b := a
	b.RequestID, b.Action = "0011223344556678", "POST /api/v1/admin/people/"+two.ID+"/setup-code"
	first, err := v.NewSetupCode(ctx, b, two.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetupCodeFor(first.Code, first.Expires.Add(time.Second)); !errors.Is(err, adminaccount.ErrNoCode) {
		t.Errorf("an expired code: %v", err)
	}
	b.RequestID = "0011223344556679"
	second, err := v.NewSetupCode(ctx, b, two.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetupCodeFor(first.Code, now); !errors.Is(err, adminaccount.ErrNoCode) {
		t.Errorf("a code replaced by a newer one: %v", err)
	}
	if id, err := db.SetupCodeFor(second.Code, now); err != nil || id != two.ID {
		t.Errorf("the newer code: %q, %v", id, err)
	}
	// The code is in no log: the person's log says one was made.
	for _, e := range logOf(t, in, two.ID) {
		if strings.Contains(e.Sent+e.Received, adminaccount.NormalizeCode(second.Code)) || strings.Contains(e.Sent+e.Received, second.Code) {
			t.Error("the setup code is in the person's log")
		}
	}

	// Bad names, an unknown person, and a view with no options.
	if _, _, err := v.AddPerson(ctx, a, "   "); !errors.Is(err, ErrName) {
		t.Errorf("an empty name: %v", err)
	}
	if _, err := v.NewSetupCode(ctx, b, "0123456789abcdef"); !errors.Is(err, ErrNoPerson) {
		t.Errorf("an unknown person: %v", err)
	}
	if _, _, err := New(in).AddPerson(ctx, a, "Test Person Four"); !errors.Is(err, ErrNotReady) {
		t.Errorf("a view with no options: %v", err)
	}
}
