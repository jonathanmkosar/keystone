package operations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Invented, for throwaway folders only.
const (
	routePhraseOne = "operations person one's phrase"
	routePhraseTwo = "operations person two's phrase"
)

// The two routes answer for the signed-in person's own tenant only: the
// report as Read gives it, and the crossed watch levels the home screen
// reads; another person's chats, traces and spend are in neither, and no
// key reaches either.
func TestTheRoutes(t *testing.T) {
	ctx := context.Background()
	in := newInstall(t)
	one := newStore(t, in, "Operations Route One")
	two := newStore(t, in, "Operations Route Two")
	reg := registry(t)

	// The first person is near their cap of $5, with a failed turn whose
	// error names a key; the second has one cheap call.
	setAs(t, one, reg, tenantsettings.Admin, "usage.monthly_cap_usd", "5")
	c := newChat(t, one, "Lighthouse rota")
	addTurn(t, one, "turn-one", c, "req-one", "failed", `{"code":"auth","message":"refused `+fakeKey+`"}`, now.Add(-time.Hour))
	addRecord(t, one, usage.Record{At: now.Add(-time.Hour), RequestID: "req-one", Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: 4_500_000})
	oc := newChat(t, two, "Ferry timetable")
	addTurn(t, two, "turn-two", oc, "req-two", "done", "", now.Add(-time.Hour))
	addRecord(t, two, usage.Record{At: now.Add(-time.Hour), RequestID: "req-two", Kind: "chat", Model: "example-small", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: 1234})

	o, err := signin.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	for who, pair := range map[string]struct {
		s      *tenant.Store
		phrase string
	}{"one": {one, routePhraseOne}, "two": {two, routePhraseTwo}} {
		if err := signin.SetPassword(pair.s, pair.phrase, o); err != nil {
			t.Fatal(err)
		}
		token, _, err := signin.SignIn(pair.s, pair.phrase, o)
		if err != nil {
			t.Fatal(err)
		}
		tokens[who] = token
	}
	listed := 0
	list := func(_ context.Context, s *tenant.Store) (schedules.SchedulesReply, error) {
		listed++
		reply := schedules.SchedulesReply{Zone: schedules.Zone{Label: "UTC"}}
		if s.TenantID() == one.TenantID() {
			reply.Schedules = []schedules.Schedule{{ID: "s1", Name: "Harbour note", Says: "Daily at 07:00", Enabled: true,
				LastRun: &schedules.Run{Status: schedules.RunFailed, Error: "no model " + fakeKey}}}
		}
		return reply, nil
	}
	h, err := api.Handler(api.Options{Install: in, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute},
		Routes(Options{Schedules: list, Now: func() time.Time { return now }}))
	if err != nil {
		t.Fatal(err)
	}
	get := func(who, path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", api.Prefix+path, nil)
		if who != "" {
			r.AddCookie(&http.Cookie{Name: api.CookieName, Value: tokens[who]})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	rec := get("one", "operations")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET operations: %d %s", rec.Code, rec.Body)
	}
	var mine Report
	if err := json.Unmarshal(rec.Body.Bytes(), &mine); err != nil {
		t.Fatal(err)
	}
	want, err := Read(ctx, one, reg, Sources{Schedules: list}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(mine.Traces) != 1 || mine.Traces[0].RequestID != "req-one" || len(want.Traces) != 1 {
		t.Errorf("the first person's traces: %+v", mine.Traces)
	}
	gotCap, wantCap := find(t, mine.Limits, LimitCap), find(t, want.Limits, LimitCap)
	if len(mine.Limits) != len(want.Limits) || gotCap.Used != wantCap.Used || gotCap.Of != wantCap.Of || gotCap.State != wantCap.State {
		t.Errorf("the route's limits are not Read's")
	}
	if len(mine.Schedules.Schedules) != 1 || mine.Schedules.Failing != 1 || listed < 2 {
		t.Errorf("the first person's schedules: %+v", mine.Schedules)
	}
	if mine.Crossed < 1 || !find(t, mine.Limits, LimitCap).Crossed {
		t.Errorf("$4.50 of $5 is not crossed: %d crossed", mine.Crossed)
	}
	if strings.Contains(rec.Body.String(), fakeKey) {
		t.Error("the report holds a key")
	}
	if strings.Contains(rec.Body.String(), "Ferry timetable") || strings.Contains(rec.Body.String(), "req-two") {
		t.Error("the first person's report holds the second person's chat")
	}

	// The watch route says so; for the second person it does not.
	var watched WatchReply
	rec = get("one", "operations/watch")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &watched) != nil {
		t.Fatalf("GET operations/watch: %d %s", rec.Code, rec.Body)
	}
	capCrossed := false
	for _, l := range watched.Crossed {
		capCrossed = capCrossed || (l.Key == LimitCap && l.Percent == 90 && l.State == StateWatch)
	}
	if !capCrossed {
		t.Errorf("the watch route does not have the cap at 90%%: %+v", watched.Crossed)
	}
	var theirs WatchReply
	rec = get("two", "operations/watch")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &theirs) != nil {
		t.Fatalf("the second person's GET operations/watch: %d", rec.Code)
	}
	for _, l := range theirs.Crossed {
		if l.Key != LimitDisk {
			t.Errorf("the second person has a crossed limit: %+v", l)
		}
	}
	var report2 Report
	rec = get("two", "operations")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &report2) != nil {
		t.Fatalf("the second person's GET operations: %d", rec.Code)
	}
	if c := find(t, report2.Limits, LimitCap); c.Used != 1234 || len(report2.Traces) != 1 || report2.Traces[0].Title != "Ferry timetable" || len(report2.Schedules.Schedules) != 0 {
		t.Errorf("the second person's report: cap %+v, traces %+v", c, report2.Traces)
	}
	if strings.Contains(rec.Body.String(), "Lighthouse rota") {
		t.Error("the second person's report holds the first person's chat")
	}

	// Without a session there is neither.
	for _, path := range []string{"operations", "operations/watch"} {
		if rec := get("", path); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session: %d", path, rec.Code)
		}
	}
}
