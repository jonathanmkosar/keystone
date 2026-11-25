package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// The "spend cap" check (P3-08): a call that would take a tenant past its
// monthly cap is stopped before it is sent, a call under the cap runs, and
// every model call has a usage record.
//
// It runs the real server, with the shipped settings, on a throwaway data
// folder. An invented tenant has one model that is marked hosted and has a
// price, and the fake provider answers for it, so nothing leaves this
// machine and nothing is spent. Each reply is reported as 1,000 tokens in
// and 200 out, which at the model's price is 4,000 millionths of a dollar.
//
//  1. With a cap of one dollar, a message is answered. Its calls are in
//     the usage records with their cost, and the month's spend is their
//     sum.
//  2. The admin sets the cap to one cent. The next message is stopped with
//     the code spend_cap, the provider is not called, and the stopped call
//     has a usage record that cost nothing.
//  3. Every model call the audit log says ended has a usage record under
//     the same request id, for this tenant and for the test tenant.

// spendCapPassword is invented, and guards a folder that is removed.
const spendCapPassword = "an invented phrase for the cap check"

// tallied counts the calls that reach a provider.
type tallied struct {
	seam.Provider
	calls *atomic.Int32
}

func (p tallied) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	p.calls.Add(1)
	return p.Provider.Stream(ctx, req, emit)
}

func checkSpendCap(string) error {
	problems, err := spendCapProblems()
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

func spendCapProblems() (problems []string, err error) {
	dir, err := os.MkdirTemp("", "keystone-spend-cap-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	person, err := in.Create("Capped Person")
	if err != nil {
		return nil, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()

	// The fake provider, answering for a model that is marked hosted.
	reply := seam.Response{
		Message:    seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: "A reply from the fake provider."}}},
		StopReason: seam.StopEnd,
		Usage:      seam.Usage{InputTokens: 1000, OutputTokens: 200},
	}
	scripted, err := fake.New([]fake.Script{{Name: "any message", Steps: []fake.Step{{Reply: &reply}}}}, fake.Options{})
	if err != nil {
		return nil, err
	}
	var calls atomic.Int32
	paid := models.Model{
		Name: "paid-example", Label: "Paid Example", Adapter: models.AdapterChatCompletions, Endpoint: "https://models.example/v1",
		ProviderModel: "example-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassHosted,
		Price: models.Price{Input: 2, Output: 10},
	}
	const eachCall = 4000 // 1000 x 2 + 200 x 10, in millionths of a dollar

	setCap := func(usd string) error {
		s, err := in.Store(person.ID)
		if err != nil {
			return err
		}
		defer s.Close()
		return tenantsettings.Set(ctx, s, reg, tenantsettings.Admin, turn.KeyMonthlyCap, usd)
	}
	err = func() error {
		s, err := in.Store(person.ID)
		if err != nil {
			return err
		}
		defer s.Close()
		o, err := signin.Defaults()
		if err != nil {
			return err
		}
		if err := signin.SetPassword(s, spendCapPassword, o); err != nil {
			return err
		}
		lim, err := adapters.Limits(reg)
		if err != nil {
			return err
		}
		return models.Add(ctx, s, lim, paid)
	}()
	if err != nil {
		return nil, err
	}
	if err := setCap("1"); err != nil {
		return nil, err
	}

	// The test tenant is beside it, to show its calls are recorded too.
	fx, err := testtenant.Shipped()
	if err != nil {
		return nil, err
	}
	o, err := testtenant.DefaultOptions()
	if err != nil {
		return nil, err
	}
	res, err := testtenant.Load(in, fx, o)
	if err != nil {
		return nil, err
	}
	loaded := res.Tenant

	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	c.Adapters = adapters.Set{models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) {
		return tallied{scripted, &calls}, nil
	}}
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	defer stop()

	// send signs nobody in: it sends one message in a new chat and returns
	// the stream and the request id.
	send := func(token string) (stream, requestID string, err error) {
		rec := w.request("POST", api.Prefix+"chats", token, "", nil)
		var made turn.ChatReply
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil || made.Chat.ID == "" {
			return "", "", fmt.Errorf("a chat cannot be made through the API: status %d", rec.Code)
		}
		body, _ := json.Marshal(turn.SendBody{Text: "Suggest three names for a small bakery by the harbour."})
		rec = w.request("POST", api.Prefix+"chats/"+made.Chat.ID+"/turns", token, string(body), nil)
		stream = rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(stream, "event: "+turn.EventEnd+"\n") {
			return "", "", fmt.Errorf("a turn did not stream to its end: status %d, %d bytes", rec.Code, len(stream))
		}
		return stream, rec.Header().Get("X-Request-Id"), nil
	}
	token, rec := w.signIn(person.ID, spendCapPassword)
	if token == "" {
		return nil, fmt.Errorf("the capped tenant cannot sign in through the API: status %d", rec.Code)
	}

	// 1. Under the cap, the call runs and is recorded with its cost.
	stream, under, err := send(token)
	if err != nil {
		return nil, err
	}
	ran := int(calls.Load())
	if !strings.Contains(stream, `"status":"done"`) || ran == 0 {
		problems = append(problems, fmt.Sprintf("with a cap of one dollar and nothing spent, the turn did not run: the provider was called %d times", ran))
	}

	// 2. Over the cap, the call is stopped before it is sent.
	if err := setCap("0.01"); err != nil {
		return nil, err
	}
	stream, over, err := send(token)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(stream, `"status":"failed"`) || !strings.Contains(stream, `"code":"`+string(seam.ErrSpendCap)+`"`) {
		problems = append(problems, "with the month's spend at the cap, the turn did not fail with the code "+string(seam.ErrSpendCap))
	}
	if n := int(calls.Load()); n != ran {
		problems = append(problems, fmt.Sprintf("a call over the cap reached the provider: %d calls before it and %d after", ran, n))
	}

	s, err := in.Store(person.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	records, err := usage.List(ctx, s, usage.Filter{RequestID: under})
	if err != nil {
		return nil, err
	}
	if len(records) != ran {
		problems = append(problems, fmt.Sprintf("the turn under the cap made %d model calls and left %d usage records", ran, len(records)))
	}
	for _, r := range records {
		if r.Outcome != usage.OutcomeOK || r.CostMicros != eachCall || r.Model != paid.Name || r.Class != models.ClassHosted || r.InputTokens != 1000 || r.OutputTokens != 200 {
			problems = append(problems, fmt.Sprintf("a call under the cap is recorded as %s on %s (%s), %d in and %d out, at a cost of %d", r.Outcome, r.Model, r.Class, r.InputTokens, r.OutputTokens, r.CostMicros))
		}
	}
	records, err = usage.List(ctx, s, usage.Filter{RequestID: over})
	if err != nil {
		return nil, err
	}
	if len(records) != 1 || records[0].Outcome != usage.OutcomeRefused || records[0].CostMicros != 0 {
		problems = append(problems, fmt.Sprintf("the call over the cap left %d usage records, want one that is refused and cost nothing", len(records)))
	}
	if len(records) > 0 {
		spent, err := usage.Spent(ctx, s, usage.MonthOf(records[0].At))
		if err != nil {
			return nil, err
		}
		if want := int64(ran) * eachCall; spent != want {
			problems = append(problems, fmt.Sprintf("the month's spend is %d millionths of a dollar, and the calls that ran cost %d", spent, want))
		}
	}

	// 3. Every call that ended has a usage record, for both tenants.
	p, err := unrecordedCalls(ctx, s, "the capped tenant")
	if err != nil {
		return nil, err
	}
	problems = append(problems, p...)

	testToken, rec := w.signIn(loaded.ID, fx.Password)
	if testToken == "" {
		return nil, fmt.Errorf("the test tenant cannot sign in through the API: status %d", rec.Code)
	}
	if _, _, err := send(testToken); err != nil {
		return nil, err
	}
	ts, err := in.Store(loaded.ID)
	if err != nil {
		return nil, err
	}
	defer ts.Close()
	p, err = unrecordedCalls(ctx, ts, "the test tenant")
	if err != nil {
		return nil, err
	}
	return append(problems, p...), nil
}

// unrecordedCalls compares a tenant's audit log with its usage records:
// for each request id, the model calls that ended must be as many as the
// usage records, and there must be at least one call to compare.
func unrecordedCalls(ctx context.Context, s *tenant.Store, who string) ([]string, error) {
	entries, err := audit.List(ctx, s, audit.Filter{Kind: audit.KindModel})
	if err != nil {
		return nil, err
	}
	ended := map[string]int{}
	total := 0
	for _, e := range entries {
		if e.Outcome != audit.OutcomeStarted {
			ended[e.RequestID]++
			total++
		}
	}
	if total == 0 {
		return []string{who + " made no model call, so there is nothing to compare"}, nil
	}
	var problems []string
	for id, n := range ended {
		records, err := usage.List(ctx, s, usage.Filter{RequestID: id})
		if err != nil {
			return nil, err
		}
		if len(records) != n {
			problems = append(problems, fmt.Sprintf("%s: request %s ended %d model calls and has %d usage records", who, id, n, len(records)))
		}
	}
	all, err := usage.List(ctx, s, usage.Filter{})
	if err != nil {
		return nil, err
	}
	if len(all) != total {
		problems = append(problems, fmt.Sprintf("%s has %d usage records and %d model calls that ended", who, len(all), total))
	}
	return problems, nil
}
