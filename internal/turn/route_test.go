package turn

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// down is an endpoint that cannot be reached. It counts its calls.
type down struct{ calls *atomic.Int32 }

func (p down) Stream(_ context.Context, _ seam.Request, emit func(seam.Event) error) error {
	p.calls.Add(1)
	failure := &seam.Error{Kind: seam.ErrUnavailable, Message: "the endpoint could not be reached", Retryable: true}
	if err := emit(seam.Event{Type: seam.EventError, Error: failure}); err != nil {
		return err
	}
	return failure
}

// counted is a provider that counts its calls.
type counted struct {
	seam.Provider
	calls *atomic.Int32
}

func (p counted) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	p.calls.Add(1)
	return p.Provider.Stream(ctx, req, emit)
}

// Invented models: one hosted and paid for, one in the house.
var (
	paidModel = models.Model{
		Name: "paid-large", Label: "Paid Large", Adapter: models.AdapterAnthropic, Endpoint: "https://api.provider-one.example",
		ProviderModel: "example-large-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassHosted,
		Price: models.Price{Input: 2, Output: 10},
	}
	houseModel = models.Model{
		Name: "house-small", Label: "House Small", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8080/v1",
		ProviderModel: "small-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassLocal,
	}
)

// set stores a setting for the ordinary tenant.
func (g *rig) set(by tenantsettings.Role, key, value string) {
	g.t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		g.t.Fatal(err)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if err := tenantsettings.Set(context.Background(), s, reg, by, key, value); err != nil {
			g.t.Fatal(err)
		}
	})
}

func (g *rig) addModels(ms ...models.Model) {
	g.t.Helper()
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		for _, m := range ms {
			if err := models.Add(context.Background(), s, models.Limits{MaxModels: 5}, m); err != nil {
				g.t.Fatal(err)
			}
		}
	})
}

// The item's proof: a failing endpoint falls back, and every call has a
// usage record.
func TestAFailingEndpointFallsBackToTheNextModel(t *testing.T) {
	g := newRig(t)
	var paidCalls, houseCalls atomic.Int32
	g.extra = adapters.Set{
		models.AdapterAnthropic: func(models.Model, string) (seam.Provider, error) { return down{&paidCalls}, nil },
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) {
			return counted{answers{chat: "Three names follow."}, &houseCalls}, nil
		},
	}
	g.addModels(paidModel, houseModel)
	g.set(tenantsettings.Person, KeyFallbackModels, "not-there, house-small")
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	sv := g.serve(nil)

	id := sv.newChat(g.ordinary.ID)
	body, _ := json.Marshal(SendBody{Text: bakery, Model: "paid-large"})
	events, _, requestID := sv.send(g.ordinary.ID, id, string(body))
	if got, want := names(events), "turn turn message start text message usage end"; got != want {
		t.Fatalf("the stream was\n\t%s\nwant\n\t%s", got, want)
	}
	if text := joined(t, events, EventText); text != "Three names follow." {
		t.Errorf("the reply is %q", text)
	}
	end := last(t, events)
	if end.Status != StatusDone || end.Model != "paid-large" || end.Error != nil {
		t.Errorf("the turn ended as %+v", end)
	}
	if paidCalls.Load() != 1 || houseCalls.Load() != 1 {
		t.Errorf("the failing model was called %d times and the next %d times, want once each", paidCalls.Load(), houseCalls.Load())
	}
	for _, e := range events {
		if e.name == EventUsage {
			var u UsageEvent
			e.into(t, &u)
			if u.Model != "house-small" || u.CostMicros != 0 {
				t.Errorf("the usage event names %s at a cost of %d", u.Model, u.CostMicros)
			}
		}
	}
	var got ChatReply
	sv.call(g.ordinary.ID, "GET", "chats/"+id, "", http.StatusOK, &got)
	if len(got.Messages) != 2 || got.Messages[1].Model != "house-small" {
		t.Errorf("the reply is recorded as written by %q", got.Messages[len(got.Messages)-1].Model)
	}

	g.with(g.ordinary.ID, func(s *tenant.Store) {
		entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if err != nil {
			t.Fatal(err)
		}
		var seen []string
		for _, e := range entries {
			seen = append(seen, e.Action+":"+string(e.Outcome))
		}
		if got, want := strings.Join(seen, " "), "paid-large:started paid-large:error house-small:started house-small:ok"; got != want {
			t.Errorf("the audit log holds\n\t%s\nwant\n\t%s", got, want)
		}
		if len(entries) == 4 && (!strings.Contains(entries[2].Sent, `"model":"house-small"`) || !strings.Contains(entries[1].Received, "unavailable")) {
			t.Errorf("the second call was recorded as sent %s after %s", entries[2].Sent, entries[1].Received)
		}
		records, err := usage.List(context.Background(), s, usage.Filter{RequestID: requestID})
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 2 || records[0].Model != "paid-large" || records[0].Outcome != usage.OutcomeError || records[0].Class != models.ClassHosted ||
			records[1].Model != "house-small" || records[1].Outcome != usage.OutcomeOK || records[1].InputTokens != 1000 || records[1].Kind != "chat" {
			t.Errorf("the usage records are %+v", records)
		}
	})
}

// With no fallback named, the failure is the turn's.
func TestAFailingEndpointWithNoFallbackFailsTheTurn(t *testing.T) {
	g := newRig(t)
	var paidCalls atomic.Int32
	g.extra = adapters.Set{
		models.AdapterAnthropic:       func(models.Model, string) (seam.Provider, error) { return down{&paidCalls}, nil },
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return answers{chat: "no"}, nil },
	}
	g.addModels(paidModel, houseModel)
	sv := g.serve(nil)
	id := sv.newChat(g.ordinary.ID)
	body, _ := json.Marshal(SendBody{Text: bakery, Model: "paid-large"})
	events, _, requestID := sv.send(g.ordinary.ID, id, string(body))
	end := last(t, events)
	if end.Status != StatusFailed || end.Error == nil || end.Error.Code != string(seam.ErrUnavailable) || paidCalls.Load() != 1 {
		t.Errorf("the turn ended as %+v after %d calls", end, paidCalls.Load())
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		records, err := usage.List(context.Background(), s, usage.Filter{RequestID: requestID})
		if err != nil || len(records) != 1 || records[0].Outcome != usage.OutcomeError || records[0].Model != "paid-large" {
			t.Errorf("the usage records are %+v (%v)", records, err)
		}
	})
}

// The item's proof: a call over the cap is stopped, and one under it runs.
func TestACallOverTheMonthlyCapIsStopped(t *testing.T) {
	g := newRig(t)
	var paidCalls, houseCalls atomic.Int32
	g.extra = adapters.Set{
		models.AdapterAnthropic: func(models.Model, string) (seam.Provider, error) {
			return counted{answers{chat: "From the paid model.", title: "A title"}, &paidCalls}, nil
		},
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) {
			return counted{answers{chat: "From the house.", title: "A title"}, &houseCalls}, nil
		},
	}
	g.addModels(paidModel, houseModel)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	sv := g.serve(nil)
	send := func(model string) (Turn, string) {
		t.Helper()
		id := sv.newChat(g.ordinary.ID)
		body, _ := json.Marshal(SendBody{Text: bakery, Model: model})
		events, _, requestID := sv.send(g.ordinary.ID, id, string(body))
		return last(t, events), requestID
	}

	// Under the shipped cap the call runs, and costs 1000 x 2 + 200 x 10.
	end, _ := send("paid-large")
	if end.Status != StatusDone || end.CostMicros != 4000 || paidCalls.Load() != 1 {
		t.Fatalf("under the cap the turn ended as %+v after %d calls", end, paidCalls.Load())
	}

	// The admin sets a cap of one cent. 0.4 cents are spent, and the next
	// call could cost more than the 0.6 that is left.
	g.set(tenantsettings.Admin, KeyMonthlyCap, "0.01")
	end, requestID := send("paid-large")
	if end.Status != StatusFailed || end.Error == nil || end.Error.Code != string(seam.ErrSpendCap) || !strings.Contains(end.Error.Message, "$0.01") {
		t.Fatalf("over the cap the turn ended as %+v", end)
	}
	if paidCalls.Load() != 1 {
		t.Errorf("a call over the cap reached the provider: %d calls", paidCalls.Load())
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		entries, _ := audit.List(context.Background(), s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if len(entries) != 2 || entries[1].Outcome != audit.OutcomeRefused || entries[1].CostMicros != 0 {
			t.Errorf("the stopped call is in the audit log as %d entries", len(entries))
		}
		records, _ := usage.List(context.Background(), s, usage.Filter{RequestID: requestID})
		if len(records) != 1 || records[0].Outcome != usage.OutcomeRefused || records[0].CostMicros != 0 {
			t.Errorf("the stopped call's usage records are %+v", records)
		}
		if spent, _ := usage.Spent(context.Background(), s, usage.MonthOf(records[0].At)); spent != 4000 {
			t.Errorf("the month's spend is %d", spent)
		}
	})

	// A local model is not held by the cap.
	if end, _ := send("house-small"); end.Status != StatusDone || houseCalls.Load() != 1 {
		t.Errorf("over the cap, a local model's turn ended as %+v", end)
	}
	// With a local model in the chain, a paid call over the cap falls back to it.
	g.set(tenantsettings.Person, KeyFallbackModels, "house-small")
	end, requestID = send("paid-large")
	if end.Status != StatusDone || paidCalls.Load() != 1 || houseCalls.Load() != 2 {
		t.Errorf("over the cap with a local fallback the turn ended as %+v", end)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		records, _ := usage.List(context.Background(), s, usage.Filter{RequestID: requestID})
		if len(records) != 2 || records[0].Outcome != usage.OutcomeRefused || records[1].Model != "house-small" || records[1].Outcome != usage.OutcomeOK {
			t.Errorf("the usage records are %+v", records)
		}
	})

	// A cap that does not read stops a paid call. It never lets one through.
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if _, err := s.DB().Exec("UPDATE tenant_settings SET value = '-5' WHERE key = ?", KeyMonthlyCap); err != nil {
			t.Fatal(err)
		}
	})
	g.set(tenantsettings.Person, KeyFallbackModels, "")
	if end, _ := send("paid-large"); end.Status != StatusFailed || end.Error.Code != string(seam.ErrSpendCap) || paidCalls.Load() != 1 {
		t.Errorf("with a cap that does not read the turn ended as %+v", end)
	}
}

// The test tenant's calls have usage records too, and the title's call is
// recorded as a title.
func TestEveryCallHasAUsageRecord(t *testing.T) {
	g := newRig(t)
	g.extra = adapters.Set{models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) {
		return answers{chat: "Three names follow.", title: "Names for a bakery"}, nil
	}}
	g.addModels(houseModel)
	sv := g.serve(nil)
	body, _ := json.Marshal(SendBody{Text: bakery})
	for _, tenantID := range []string{g.test.ID, g.ordinary.ID} {
		id := sv.newChat(tenantID)
		_, _, requestID := sv.send(tenantID, id, string(body))
		g.with(tenantID, func(s *tenant.Store) {
			entries, _ := audit.List(context.Background(), s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
			ended := 0
			for _, e := range entries {
				if e.Outcome != audit.OutcomeStarted {
					ended++
				}
			}
			records, err := usage.List(context.Background(), s, usage.Filter{RequestID: requestID})
			if err != nil || ended == 0 || len(records) != ended {
				t.Fatalf("%d model calls ended and there are %d usage records (%v)", ended, len(records), err)
			}
			if records[0].Kind != "chat" || records[0].Outcome != usage.OutcomeOK || records[0].OutputTokens == 0 {
				t.Errorf("the chat call's record is %+v", records[0])
			}
			if tenantID == g.ordinary.ID && (len(records) != 2 || records[1].Kind != "title") {
				t.Errorf("the title call's record is missing or of the wrong kind: %+v", records)
			}
		})
	}
}
