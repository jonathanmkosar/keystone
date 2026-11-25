package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// The "sensitive content" check (P6-05), the plan's standing check of the
// same name: content from a sensitive space never appears in a call to a
// model that may not read it, by recall, by a lookup or by a fallback.
//
// It runs the real server on the same throwaway folder as the recall
// check: the test tenant, and the fixture's knowledge in the built-in store
// and in a fake Waymarks on this machine. The test tenant's one model is
// local (decision 0144), so beside it is an invented tenant with the same
// knowledge and three models the fake answers for: one hosted, one local
// and one trusted. Every request each of them is sent is kept. For each
// store:
//
//  1. The test tenant's local model asked about the sensitive space is
//     given it. So is the trusted model below. The check would prove
//     nothing if recall dropped everything.
//  2. The hosted model asked about the sensitive space is given none of
//     it, and the turn's record says the search found it and held it back.
//  3. The local model, failing, with the hosted and then the trusted model
//     behind it: the trusted model answers with the sensitive content, and
//     the hosted model is not called at all.
//  4. In that chat, which was sent sensitive content, the hosted model is
//     picked for the next message, and is still not called.
//  5. The local model, failing, with the hosted model behind it, asked
//     about something plain: the hosted model answers, and its call holds
//     nothing from the sensitive space, not even the space's name in the
//     map.
//  6. The three lookups, run for the hosted model, return none of it, and
//     run for the trusted model they do.
//
// Last, every request the hosted model was sent, and every call to it the
// audit log holds, is searched for the fixture's markers: words that are in
// the sensitive space and nowhere else.

// The invented tenant's models. The fake answers for each, so nothing
// leaves this machine.
var (
	sensitiveHosted = models.Model{
		Name: "hosted-example", Label: "Hosted Example", Adapter: models.AdapterAnthropic, Endpoint: "https://models.example/v1",
		ProviderModel: "example-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassHosted,
	}
	sensitiveLocal = models.Model{
		Name: "local-example", Label: "Local Example", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8080/v1",
		ProviderModel: "small-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal,
	}
	sensitiveTrusted = models.Model{
		Name: "trusted-example", Label: "Trusted Example", Adapter: models.AdapterResponses, Endpoint: "https://trusted.example/v1",
		ProviderModel: "trusted-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassTrusted,
	}
)

// sensitivePassword is invented, and guards a folder that is removed.
const sensitivePassword = "an invented phrase for the sensitive check"

// ears keeps every request each model is sent, by the model's name. A
// model marked down fails before its reply begins, as an endpoint that
// cannot be reached does.
type ears struct {
	mu   sync.Mutex
	sent map[string][]string
	down map[string]bool
}

func (e *ears) builder(m models.Model, _ string) (seam.Provider, error) { return heard{e, m.Name}, nil }

// heard returns the requests a model has been sent since it had n.
func (e *ears) heard(name string, n int) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n > len(e.sent[name]) {
		return nil
	}
	return append([]string(nil), e.sent[name][n:]...)
}

func (e *ears) count(name string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.sent[name])
}

func (e *ears) setDown(name string, down bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.down[name] = down
}

type heard struct {
	e    *ears
	name string
}

func (h heard) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	raw, err := seam.EncodeRequest(req)
	if err != nil {
		return err
	}
	h.e.mu.Lock()
	h.e.sent[h.name] = append(h.e.sent[h.name], string(raw))
	down := h.e.down[h.name]
	h.e.mu.Unlock()
	if down {
		failure := &seam.Error{Kind: seam.ErrUnavailable, Message: "the endpoint could not be reached", Retryable: true}
		if err := emit(seam.Event{Type: seam.EventError, Error: failure}); err != nil {
			return err
		}
		return failure
	}
	events, err := seam.Events(seam.Response{
		Model:      req.Model,
		Message:    seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: "A reply from " + h.name + "."}}},
		StopReason: seam.StopEnd,
		Usage:      seam.Usage{InputTokens: 100, OutputTokens: 10},
	})
	if err != nil {
		return err
	}
	for _, e := range events {
		if err := emit(e); err != nil {
			return err
		}
	}
	return nil
}

// marker returns the first of the fixture's markers a text holds, in any
// letter case, or "".
func marker(text string, markers []string) string {
	lower := strings.ToLower(text)
	for _, m := range markers {
		if strings.Contains(lower, strings.ToLower(m)) {
			return m
		}
	}
	return ""
}

func checkSensitive(root string) error {
	e := &ears{sent: map[string][]string{}, down: map[string]bool{}}
	set := adapters.Set{}
	for _, m := range []models.Model{sensitiveHosted, sensitiveLocal, sensitiveTrusted} {
		set[m.Adapter] = e.builder
	}
	g, err := openKnowledgeRig(root, "keystone-sensitive-", set)
	if err != nil {
		return err
	}
	defer g.done()
	problems, err := sensitiveProblems(g, e)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func sensitiveProblems(g *knowledgeRig, e *ears) ([]string, error) {
	ctx := context.Background()
	fx := g.fx
	markers := fx.Sensitive.Markers
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	for _, ask := range []string{fx.Sensitive.Ask, fx.Sensitive.Plain} {
		if m := marker(ask, markers); m != "" {
			return nil, fmt.Errorf("the fixture's question %q holds its own marker %q, so a call could not be searched for it", ask, m)
		}
	}

	// The invented tenant.
	person, err := g.in.Create("Sensitive Check Person")
	if err != nil {
		return nil, err
	}
	err = g.with(person.ID, func(s *tenant.Store) error {
		o, err := signin.Defaults()
		if err != nil {
			return err
		}
		if err := signin.SetPassword(s, sensitivePassword, o); err != nil {
			return err
		}
		lim, err := adapters.Limits(g.reg)
		if err != nil {
			return err
		}
		for _, m := range []models.Model{sensitiveHosted, sensitiveLocal, sensitiveTrusted} {
			if err := models.Add(ctx, s, lim, m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := g.seed(person.ID); err != nil {
		return nil, err
	}
	token, rec := g.w.signIn(person.ID, sensitivePassword)
	if token == "" {
		return nil, fmt.Errorf("the invented tenant cannot sign in through the API: status %d", rec.Code)
	}
	testToken, rec := g.w.signIn(g.test.ID, g.testPassword)
	if testToken == "" {
		return nil, fmt.Errorf("the test tenant cannot sign in through the API: status %d", rec.Code)
	}
	ended := func(t sent, model string) {
		if !strings.Contains(t.stream, `"status":"done"`) {
			say("a turn on %s did not end as done", model)
		}
	}

	for _, store := range knowledgeStores {
		for _, id := range []string{g.test.ID, person.ID} {
			if err := g.use(id, store); err != nil {
				return nil, err
			}
		}

		// 1. A model that may read the sensitive space is given it.
		t, err := g.send(testToken, "", fx.Sensitive.Ask, "")
		if err != nil {
			return nil, err
		}
		if call, _, err := g.calls(g.test.ID, t.requestID); err != nil {
			return nil, err
		} else if marker(call, markers) == "" {
			say("%s: the test tenant's local model asked about the sensitive space was given none of it, so the check proves nothing", store)
		}

		// 2. Recall to the hosted model.
		if err := g.set(person.ID, turn.KeyFallbackModels, ""); err != nil {
			return nil, err
		}
		before := e.count(sensitiveHosted.Name)
		if t, err = g.send(token, "", fx.Sensitive.Ask, sensitiveHosted.Name); err != nil {
			return nil, err
		}
		ended(t, sensitiveHosted.Name)
		if len(e.heard(sensitiveHosted.Name, before)) == 0 {
			say("%s: the hosted model was not called with a question about the sensitive space", store)
		}
		if found, ok, err := g.record(person.ID, t.turnID); err != nil {
			return nil, err
		} else if !ok || found.Withheld == 0 || found.Sensitive {
			say("%s: the hosted model's turn should record sensitive hits held back and none kept: %+v", store, found)
		}

		// 3. The fallback chain.
		if err := g.set(person.ID, turn.KeyFallbackModels, sensitiveHosted.Name+", "+sensitiveTrusted.Name); err != nil {
			return nil, err
		}
		e.setDown(sensitiveLocal.Name, true)
		before, trusted := e.count(sensitiveHosted.Name), e.count(sensitiveTrusted.Name)
		held, err := g.send(token, "", fx.Sensitive.Ask, sensitiveLocal.Name)
		if err != nil {
			return nil, err
		}
		ended(held, sensitiveLocal.Name)
		if n := len(e.heard(sensitiveHosted.Name, before)); n != 0 {
			say("%s: with the local model down, the hosted model behind it was called %d times with a turn that recalled sensitive content", store, n)
		}
		given := strings.Join(e.heard(sensitiveTrusted.Name, trusted), "\n")
		if marker(given, markers) == "" {
			say("%s: with the local model down, the trusted model behind it was not given the sensitive content", store)
		}
		err = g.with(person.ID, func(s *tenant.Store) error {
			h, err := recall.Held(ctx, s, held.chatID)
			if err == nil && !h {
				say("%s: a chat that was sent sensitive content is not held", store)
			}
			return err
		})
		if err != nil {
			return nil, err
		}

		// 4. The held chat, on the hosted model.
		before = e.count(sensitiveHosted.Name)
		if t, err = g.send(token, held.chatID, "Thank you. Can you say that again more briefly?", sensitiveHosted.Name); err != nil {
			return nil, err
		}
		if n := len(e.heard(sensitiveHosted.Name, before)); n != 0 {
			say("%s: the hosted model was picked in a chat that was sent sensitive content, and was called %d times", store, n)
		}

		// 5. A plain question, falling back to the hosted model.
		if err := g.set(person.ID, turn.KeyFallbackModels, sensitiveHosted.Name); err != nil {
			return nil, err
		}
		before = e.count(sensitiveHosted.Name)
		if t, err = g.send(token, "", fx.Sensitive.Plain, sensitiveLocal.Name); err != nil {
			return nil, err
		}
		ended(t, sensitiveLocal.Name)
		got := e.heard(sensitiveHosted.Name, before)
		if len(got) == 0 {
			say("%s: with the local model down and a plain question, the hosted model behind it was not called", store)
		} else if !strings.Contains(strings.Join(got, "\n"), recalledMark) {
			say("%s: the plain question recalled nothing for the hosted model behind the local one", store)
		}
		e.setDown(sensitiveLocal.Name, false)

		// 6. The lookups.
		ids := g.ids(person.ID, store)
		words, _ := json.Marshal(map[string]string{"query": fx.Sensitive.Ask})
		for _, may := range []bool{false, true} {
			var all strings.Builder
			err := g.with(person.ID, func(s *tenant.Store) error {
				ks, err := knowledge.Open(ctx, store, s)
				if err != nil {
					return err
				}
				defer ks.Close()
				list, err := recall.Lookups(may, recall.LookupOptions{MaxResults: 10, MaxChars: 6000})
				if err != nil {
					return err
				}
				inputs := map[string]string{
					recall.LookupSearch:     string(words),
					recall.LookupReadPage:   `{"id":"` + ids[0] + `"}`,
					recall.LookupReadSource: `{"id":"` + ids[1] + `"}`,
				}
				for _, l := range list {
					res, err := l.Run(ctx, s, ks, json.RawMessage(inputs[l.Name]))
					if err != nil {
						return fmt.Errorf("the lookup %s: %w", l.Name, err)
					}
					if m := marker(res.Text, markers); !may && m != "" {
						say("%s: the lookup %s gave a model that may not read sensitive content %q", store, l.Name, m)
					}
					all.WriteString(res.Text + "\n")
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			if may && marker(all.String(), markers) == "" {
				say("%s: the lookups gave a model that may read sensitive content none of it, so the check proves nothing", store)
			}
		}
	}

	// Everything the hosted model was ever sent.
	for i, req := range e.heard(sensitiveHosted.Name, 0) {
		if m := marker(req, markers); m != "" {
			say("request %d to the hosted model holds %q, from a sensitive space", i+1, m)
		}
	}
	err = g.with(person.ID, func(s *tenant.Store) error {
		entries, err := audit.List(ctx, s, audit.Filter{Kind: audit.KindModel})
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Action == sensitiveHosted.Name {
				if m := marker(entry.Sent, markers); m != "" {
					say("the audit log holds a call to the hosted model with %q, from a sensitive space", m)
				}
			}
		}
		return nil
	})
	return problems, err
}
