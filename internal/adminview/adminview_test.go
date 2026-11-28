package adminview

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every name and password here is invented.
const passwordTwo = "test person two's phrase"

// setup makes an install with two people, the second signed in once, and
// one admin account in the admin database.
func setup(t *testing.T) (in *tenant.Install, admin adminaccount.Account, one, two tenant.Tenant) {
	t.Helper()
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if one, err = in.Create("Test Person One"); err != nil {
		t.Fatal(err)
	}
	if two, err = in.Create("Test Person Two"); err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(two.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	o := signin.Options{PasswordIterations: 50, PasswordMinLength: 12, SessionLifetime: time.Hour, FailuresBeforeWait: 3, FailureWait: time.Minute}
	if err := signin.SetPassword(s, passwordTwo, o); err != nil {
		t.Fatal(err)
	}
	if _, _, err := signin.SignIn(s, passwordTwo, o); err != nil {
		t.Fatal(err)
	}
	db, err := adminaccount.Open(in.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if admin, err = db.AddAccount("Test Admin"); err != nil {
		t.Fatal(err)
	}
	return in, admin, one, two
}

func logOf(t *testing.T, in *tenant.Install, id string) []audit.Entry {
	t.Helper()
	s, err := in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := audit.List(context.Background(), s, audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPeopleIsWrittenInEachPersonsLog(t *testing.T) {
	in, admin, one, two := setup(t)
	v := New(in)
	a := Asker{AdminID: admin.ID, RequestID: "0123456789abcdef", Action: "GET /api/v1/admin/people"}

	people, err := v.People(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 2 || people[0].ID != one.ID || people[1].ID != two.ID {
		t.Fatalf("the people are %+v", people)
	}
	if people[1].Name != "Test Person Two" || !people[1].Added.Equal(two.Created) || people[1].LastSignIn.IsZero() {
		t.Errorf("the second person is %+v", people[1])
	}
	if !people[0].LastSignIn.IsZero() {
		t.Errorf("a person who never signed in has a last sign-in: %+v", people[0])
	}
	// Numbers and settings only: the reply has no other field.
	raw, _ := json.Marshal(people[0])
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	for k := range fields {
		switch k {
		case "id", "name", "added", "last_sign_in", "enabled", "has_password", "spend", "storage", "jobs", "schedules", "models", "crossed":
		default:
			t.Errorf("a person, as the admin sees them, has the field %q", k)
		}
	}

	// Each person's log: started, then ok, under the admin's call, with the
	// admin account's name and what was read.
	for _, p := range []tenant.Tenant{one, two} {
		got := logOf(t, in, p.ID)
		if len(got) != 2 {
			t.Fatalf("%s's log holds %d entries, want 2: %+v", p.Name, len(got), got)
		}
		for i, want := range []audit.Outcome{audit.OutcomeStarted, audit.OutcomeOK} {
			e := got[i]
			var touch Touch
			json.Unmarshal([]byte(e.Sent), &touch)
			if e.Actor != ActorPrefix+admin.ID || e.RequestID != a.RequestID || e.Action != a.Action || e.Outcome != want || touch.By != "Test Admin" || len(touch.Read) != len(numbersRead) || touch.Read[0] != "last_sign_in" {
				t.Errorf("entry %d in %s's log is %+v", i, p.Name, e)
			}
		}
	}
}

func TestTheViewAsksForAnAdminAccount(t *testing.T) {
	in, admin, one, _ := setup(t)
	v := New(in)
	ctx := context.Background()
	good := Asker{AdminID: admin.ID, RequestID: "0123456789abcdef", Action: "GET /api/v1/admin/people"}

	for what, a := range map[string]Asker{
		"a person's id":                        {AdminID: one.ID, RequestID: good.RequestID, Action: good.Action},
		"no request id":                        {AdminID: admin.ID, Action: good.Action},
		"a route outside the admin's":          {AdminID: admin.ID, RequestID: good.RequestID, Action: "GET /api/v1/chats"},
		"something that is no id":              {AdminID: "../" + admin.ID, RequestID: good.RequestID, Action: good.Action},
		"an account id that is not an admin's": {AdminID: "0123456789abcdef", RequestID: good.RequestID, Action: good.Action},
	} {
		if _, err := v.People(ctx, a); err == nil {
			t.Errorf("the view read people for %s", what)
		}
	}
	if _, err := v.People(ctx, Asker{AdminID: one.ID, RequestID: good.RequestID, Action: good.Action}); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("a person's id: %v", err)
	}
	if got := logOf(t, in, one.ID); len(got) != 0 {
		t.Errorf("refused reads left %d entries in a person's log", len(got))
	}

	// The account removed from the command line: the view refuses at once.
	db, err := adminaccount.Open(in.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RemoveAccount(admin.ID); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := v.People(ctx, good); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("after the account was removed: %v", err)
	}
}
