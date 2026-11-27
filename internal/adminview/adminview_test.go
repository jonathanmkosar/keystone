package adminview

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every name and password here is invented.
const passwordTwo = "test person two's phrase"

func setup(t *testing.T) (in *tenant.Install, admin, other tenant.Tenant) {
	t.Helper()
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if admin, err = in.Create("Test Admin"); err != nil {
		t.Fatal(err)
	}
	if other, err = in.Create("Test Person Two"); err != nil {
		t.Fatal(err)
	}
	if _, err := in.SetAdmin(admin.ID, true); err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(other.ID)
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
	return in, admin, other
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

func TestPeopleIsWrittenInEachOtherPersonsLog(t *testing.T) {
	in, admin, other := setup(t)
	v := New(in)
	a := Asker{AdminID: admin.ID, RequestID: "0123456789abcdef", Action: "GET /api/v1/admin/people"}

	people, err := v.People(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 2 || people[0].ID != admin.ID || !people[0].Admin || people[1].ID != other.ID || people[1].Admin {
		t.Fatalf("the people are %+v", people)
	}
	if people[1].Name != "Test Person Two" || !people[1].Added.Equal(other.Created) || people[1].LastSignIn.IsZero() {
		t.Errorf("the other person is %+v", people[1])
	}
	if !people[0].LastSignIn.IsZero() {
		t.Errorf("the admin, who never signed in, has a last sign-in: %+v", people[0])
	}

	// The other person's log: started, then ok, under the admin's call.
	got := logOf(t, in, other.ID)
	if len(got) != 2 {
		t.Fatalf("the other person's log holds %d entries, want 2: %+v", len(got), got)
	}
	for i, want := range []audit.Outcome{audit.OutcomeStarted, audit.OutcomeOK} {
		e := got[i]
		if e.Actor != ActorPrefix+admin.ID || e.RequestID != a.RequestID || e.Action != a.Action || e.Outcome != want || !strings.Contains(e.Sent, "last_sign_in") {
			t.Errorf("entry %d in the other person's log is %+v", i, e)
		}
	}
	// The admin's own facts leave nothing in their log: the API shell's
	// wrapper records the call there.
	if got := logOf(t, in, admin.ID); len(got) != 0 {
		t.Errorf("the view wrote %d entries in the admin's own log", len(got))
	}
}

func TestTheViewAsksForTheMarkItself(t *testing.T) {
	in, admin, other := setup(t)
	v := New(in)
	ctx := context.Background()
	good := Asker{AdminID: admin.ID, RequestID: "0123456789abcdef", Action: "GET /api/v1/admin/people"}

	for what, a := range map[string]Asker{
		"a person without the mark": {AdminID: other.ID, RequestID: good.RequestID, Action: good.Action},
		"no request id":             {AdminID: admin.ID, Action: good.Action},
		"a route outside the admin": {AdminID: admin.ID, RequestID: good.RequestID, Action: "GET /api/v1/chats"},
		"something that is no id":   {AdminID: "../" + admin.ID, RequestID: good.RequestID, Action: good.Action},
	} {
		if _, err := v.People(ctx, a); err == nil {
			t.Errorf("the view read people for %s", what)
		}
	}
	if _, err := v.People(ctx, Asker{AdminID: other.ID, RequestID: good.RequestID, Action: good.Action}); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("a person without the mark: %v", err)
	}
	if got := logOf(t, in, other.ID); len(got) != 0 {
		t.Errorf("refused reads left %d entries in the other person's log", len(got))
	}

	// The mark taken away: the view refuses at once.
	if _, err := in.SetAdmin(admin.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := v.People(ctx, good); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("after the mark was taken away: %v", err)
	}
}
