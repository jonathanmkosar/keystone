package adminview

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/installsettings"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// Settings from the browser (P15-03). Every key here is in the registry
// with the scope its name says.
const (
	forOnePerson = "share.max_links"       // tenant-admin
	personsOwn   = "appearance.theme"      // tenant
	installWide  = "usage.report_max_days" // install
)

func adminLogOf(t *testing.T, in *tenant.Install, requestID string) []audit.Entry {
	t.Helper()
	db, err := adminaccount.Open(in.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.Log(context.Background(), audit.Filter{RequestID: requestID})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func changeIn(t *testing.T, e audit.Entry) logged {
	t.Helper()
	var l logged
	if err := json.Unmarshal([]byte(e.Sent), &l); err != nil {
		t.Fatalf("an entry's sent field is not a change: %q", e.Sent)
	}
	return l
}

func TestAnAdminSetsAPersonsTenantAdminSettingInBothLogs(t *testing.T) {
	t.Cleanup(func() { settings.UseInstalled(nil) })
	in, admin, one, two := setup(t)
	v := New(in)
	ctx := context.Background()
	reg, _ := settings.Registry()
	st, _ := settings.Lookup(reg, forOnePerson)

	// The read: tenant-admin settings alone, in the person's log.
	read := Asker{AdminID: admin.ID, RequestID: "1111111111111111", Action: "GET /api/v1/admin/people/" + one.ID + "/settings"}
	list, err := v.PersonSettings(ctx, read, one.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range list {
		if s.Scope != string(settings.ScopeTenantAdmin) {
			t.Errorf("the admin read %s, a %s setting", s.Key, s.Scope)
		}
		found = found || s.Key == forOnePerson
	}
	if !found {
		t.Errorf("%s is not among the person's tenant-admin settings", forOnePerson)
	}
	if got := logOf(t, in, one.ID); len(got) != 2 || got[0].RequestID != read.RequestID || got[0].Actor != ActorPrefix+admin.ID {
		t.Errorf("the read is in the person's log as %+v", got)
	}

	// The change.
	a := Asker{AdminID: admin.ID, RequestID: "2222222222222222", Action: "PUT /api/v1/admin/people/" + one.ID + "/settings"}
	after, c, err := v.ChangePersonSetting(ctx, a, one.ID, forOnePerson, "3", false)
	if err != nil {
		t.Fatal(err)
	}
	if after.Value != "3" || !after.Overridden || c.Old != st.Default || c.New != "3" || c.Person != one.ID || c.By != "Test Admin" {
		t.Errorf("the change answered %+v and %+v", after, c)
	}
	theirs := logOf(t, in, one.ID)[2:]
	if len(theirs) != 2 || theirs[0].Outcome != audit.OutcomeStarted || theirs[1].Outcome != audit.OutcomeOK {
		t.Fatalf("the change is in the person's log as %+v", theirs)
	}
	if l := changeIn(t, theirs[0]); l.Key != forOnePerson || l.New != "3" || l.Old != nil || l.By != "Test Admin" {
		t.Errorf("the person's first entry holds %+v", l)
	}
	if l := changeIn(t, theirs[1]); l.Old == nil || *l.Old != st.Default || l.New != "3" {
		t.Errorf("the person's second entry holds %+v", l)
	}
	ours := adminLogOf(t, in, a.RequestID)
	if len(ours) != 2 || ours[1].Outcome != audit.OutcomeOK {
		t.Fatalf("the change is in the admin log as %+v", ours)
	}
	if l := changeIn(t, ours[1]); l.Person != one.ID || l.Key != forOnePerson || l.Old == nil || *l.Old != st.Default || l.New != "3" {
		t.Errorf("the admin log's entry holds %+v", l)
	}
	if got := logOf(t, in, two.ID); len(got) != 0 {
		t.Errorf("the other person's log has %d entries", len(got))
	}
	s, _ := in.Store(one.ID)
	if got, _ := tenantsettings.Get(ctx, s, reg, forOnePerson); got != "3" {
		t.Errorf("the person's setting is %q", got)
	}
	s.Close()

	// Back to the default.
	a.RequestID = "3333333333333333"
	if after, c, err = v.ChangePersonSetting(ctx, a, one.ID, forOnePerson, "", true); err != nil || after.Overridden || c.Old != "3" || c.New != st.Default {
		t.Errorf("the reset answered %+v, %+v, %v", after, c, err)
	}
}

func TestARefusedChangeReadsNothingOfThePersons(t *testing.T) {
	in, admin, one, _ := setup(t)
	v := New(in)
	a := Asker{AdminID: admin.ID, RequestID: "4444444444444444", Action: "PUT /api/v1/admin/people/" + one.ID + "/settings"}
	for _, c := range []struct {
		person, key, value string
		want               error
	}{
		{one.ID, personsOwn, "dark", tenantsettings.ErrNotAllowed},
		{one.ID, installWide, "10", tenantsettings.ErrNotAllowed},
		{one.ID, "rules.sensitive_content", "false", tenantsettings.ErrNotAllowed},
		{one.ID, "no.such_key", "1", tenantsettings.ErrUnknown},
		{one.ID, forOnePerson, "several", tenantsettings.ErrBadValue},
		{"0123456789abcdef", forOnePerson, "3", ErrNoPerson},
	} {
		if _, _, err := v.ChangePersonSetting(context.Background(), a, c.person, c.key, c.value, false); !errors.Is(err, c.want) {
			t.Errorf("changing %s gave %v, want %v", c.key, err, c.want)
		}
	}
	if got := logOf(t, in, one.ID); len(got) != 0 {
		t.Errorf("refused changes left %d entries in the person's log", len(got))
	}
	if got := adminLogOf(t, in, a.RequestID); len(got) != 0 {
		t.Errorf("refused changes left %d entries of their own in the admin log", len(got))
	}
	// Not an admin account: refused before anything.
	if _, _, err := v.ChangePersonSetting(context.Background(), Asker{AdminID: "ffffffffffffffff", RequestID: "5", Action: a.Action}, one.ID, forOnePerson, "3", false); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("an account that is not there gave %v", err)
	}
}

func TestAnInstallChangeIsInTheAdminLogAlone(t *testing.T) {
	t.Cleanup(func() { settings.UseInstalled(nil) })
	in, admin, one, _ := setup(t)
	v := New(in)
	if err := installsettings.Load(in); err != nil {
		t.Fatal(err)
	}
	reg, _ := settings.Registry()
	st, _ := settings.Lookup(reg, installWide)
	a := Asker{AdminID: admin.ID, RequestID: "6666666666666666", Action: "PUT /api/v1/admin/install/settings"}
	after, c, err := v.ChangeInstallSetting(context.Background(), a, installWide, "30", false)
	if err != nil {
		t.Fatal(err)
	}
	if after.Value != "30" || after.InForce != "30" || after.Default != st.Default || c.Old != st.Default || c.New != "30" || c.Person != "" {
		t.Errorf("the change answered %+v and %+v", after, c)
	}
	ours := adminLogOf(t, in, a.RequestID)
	if len(ours) != 2 {
		t.Fatalf("the change is in the admin log as %d entries", len(ours))
	}
	if l := changeIn(t, ours[0]); l.Old == nil || *l.Old != st.Default || l.New != "30" {
		t.Errorf("the admin log's entry holds %+v", l)
	}
	if got := logOf(t, in, one.ID); len(got) != 0 {
		t.Errorf("an install change left %d entries in a person's log", len(got))
	}
	// A person's default is set here; a tenant-admin setting is not.
	if _, _, err := v.ChangeInstallSetting(context.Background(), a, personsOwn, "dark", false); err != nil {
		t.Error(err)
	}
	if _, _, err := v.ChangeInstallSetting(context.Background(), a, forOnePerson, "3", false); !errors.Is(err, installsettings.ErrNotAllowed) {
		t.Errorf("a tenant-admin setting set for the install gave %v", err)
	}
	all, err := v.InstallSettings(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s.Key == personsOwn && (s.Value != "dark" || !s.Overridden) {
			t.Errorf("the person's default is listed as %+v", s)
		}
	}
}
