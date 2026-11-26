package usagereport

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
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Invented, for throwaway folders only.
const (
	routePhraseOne = "usage report person one's phrase"
	routePhraseTwo = "usage report person two's phrase"
)

// The route gives what Read gives, for the signed-in person's own tenant
// only, and refuses a period or a number of rows it cannot use.
func TestTheRoute(t *testing.T) {
	w := newWorld(t, 17, 200)
	ctx := context.Background()
	two := newStore(t, w.in, "Report Person Four")
	at := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	if _, err := usage.Add(ctx, two, usage.Record{At: at, Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, OutputTokens: 7, CostMicros: 4321}); err != nil {
		t.Fatal(err)
	}
	o, err := signin.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	for who, pair := range map[string]struct {
		s      interface{ TenantID() string }
		phrase string
	}{"one": {w.s, routePhraseOne}, "two": {two, routePhraseTwo}} {
		s, err := w.in.Store(pair.s.TenantID())
		if err != nil {
			t.Fatal(err)
		}
		if err := signin.SetPassword(s, pair.phrase, o); err != nil {
			t.Fatal(err)
		}
		token, _, err := signin.SignIn(s, pair.phrase, o)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
		tokens[who] = token
	}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	h, err := api.Handler(api.Options{Install: w.in, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute},
		Routes(Options{PageLimit: 50, Now: func() time.Time { return now }}))
	if err != nil {
		t.Fatal(err)
	}
	get := func(who, query string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", api.Prefix+"usage"+query, nil)
		if who != "" {
			r.AddCookie(&http.Cookie{Name: api.CookieName, Value: tokens[who]})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	for query, ask := range map[string]Ask{
		"":                                {},
		"?month=2026-10":                  {Month: "2026-10"},
		"?period=last_7_days&rows=2":      {Period: PeriodLast7Days, Rows: 2},
		"?from=2026-10-30&to=2026-11-02":  {From: "2026-10-30", To: "2026-11-02"},
		"?from=2026-10-30&to=2026-11-02&": {From: "2026-10-30", To: "2026-11-02"},
	} {
		rec := get("one", query)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET usage%s: %d %s", query, rec.Code, rec.Body)
		}
		var got Report
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want, err := Read(ctx, w.s, w.reg, ask, now)
		if err != nil {
			t.Fatal(err)
		}
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("GET usage%s is not what Read gives:\n got %s\nwant %s", query, gotJSON, wantJSON)
		}
		if got.Total.Calls == 0 && query == "" {
			t.Errorf("GET usage%s found no calls", query)
		}
	}

	// The second person sees their one call and none of the first's.
	var theirs Report
	rec := get("two", "?month=2026-11")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &theirs) != nil {
		t.Fatalf("the second person's report: %d %s", rec.Code, rec.Body)
	}
	if theirs.Total.Calls != 1 || theirs.Total.CostMicros != 4321 || theirs.Cap.SpentMicros != 4321 {
		t.Errorf("the second person's report holds %+v and a cap spend of %d", theirs.Total, theirs.Cap.SpentMicros)
	}
	if strings.Contains(rec.Body.String(), "Harbour bakery names") {
		t.Error("the second person's report names the first person's chat")
	}

	// Without a session there is no report.
	if rec := get("", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a session: %d", rec.Code)
	}

	for query, code := range map[string]string{
		"?month=2026-13":                    "bad_period",
		"?period=forever":                   "bad_period",
		"?month=2026-11&period=last_7_days": "bad_period",
		"?from=2026-11-01":                  "bad_period",
		"?from=2025-01-01&to=2026-11-01":    "bad_period",
		"?rows=0":                           "bad_request",
		"?rows=51":                          "bad_request",
		"?rows=ten":                         "bad_request",
	} {
		rec := get("one", query)
		var e api.ErrorBody
		json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != http.StatusBadRequest || e.Code != code || e.Error == "" {
			t.Errorf("GET usage%s: %d %q %q, want 400 %s", query, rec.Code, e.Code, e.Error, code)
		}
	}
}
