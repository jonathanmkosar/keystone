package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/knowledge/waymarks"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// The "embeddings" check (P6-08): recall by words and meaning answers more
// of the test tenant's questions than words alone, and no sensitive page,
// nor the query of a chat that holds sensitive content, reaches a hosted
// embedding model.
//
// It runs the real server, with the shipped settings, on the recall
// check's throwaway folder: the test tenant, and the fixture's knowledge in
// the built-in store and in a fake Waymarks on this machine that takes page
// vectors. fixtures/test-tenant/knowledge/meaning.json adds questions that
// share no word with the page that answers them, and the word groups the
// fake embedding model knows.
//
//  1. Meaning. The test tenant is given the fake embedding model, local,
//     in its registry. For each store, the person's vectors are refreshed
//     through POST /knowledge/embeddings/refresh, every page gets one, and
//     every question of both fixtures is asked twice: with recall by
//     meaning off, then on. On must answer more of them, every question of
//     the recall fixture still, and a message about nothing still recalls
//     nothing. On the built-in store a changed page is refreshed alone.
//  2. The sensitive rule. An invented tenant has the same knowledge in the
//     built-in store, a local chat model, and a hosted embedding model on
//     the Chat Completions adapter behind a stand-in server on this
//     machine, which keeps every request the adapter sends. The refresh
//     leaves the sensitive page without a vector and says so; no request
//     holds a word of the sensitive space; a message in a chat that was
//     sent sensitive content does not reach the stand-in; and with a local
//     embedding model added, chosen first, every page gets a vector of the
//     new model, the sensitive one too. Every embedding call the audit log
//     says ended has a usage record under its request id.

// meaningFixture is fixtures/test-tenant/knowledge/meaning.json.
type meaningFixture struct {
	TestData  bool       `json:"test_data"`
	About     string     `json:"about"`
	Meanings  [][]string `json:"meanings"`
	Questions []struct {
		Ask    string `json:"ask"`
		Answer string `json:"answer"`
	} `json:"questions"`
	Held struct {
		Ask  string `json:"ask"`
		Word string `json:"word"`
	} `json:"held"`
}

func readMeaningFixture(root string) (meaningFixture, error) {
	var fx meaningFixture
	path := filepath.Join(root, "fixtures", "test-tenant", "knowledge", "meaning.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fx, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fx); err != nil {
		return fx, fmt.Errorf("%s does not read: %w", path, err)
	}
	switch {
	case !fx.TestData:
		return fx, fmt.Errorf("%s is not marked as test data", path)
	case len(fx.Questions) == 0 || len(fx.Meanings) == 0 || fx.Held.Ask == "" || fx.Held.Word == "":
		return fx, fmt.Errorf("%s needs questions, word groups and a held question", path)
	case !strings.Contains(strings.ToLower(fx.Held.Ask), fx.Held.Word):
		return fx, fmt.Errorf("%s: the held question does not hold its word", path)
	}
	return fx, nil
}

// The models of the check. Nothing leaves this machine: the fake answers
// the local ones, and the hosted embedding model's endpoint is the
// stand-in, set when it starts.
var (
	embedCheckFake = models.Model{
		Name: "test-embed", Label: "Test embeddings (test data)", Adapter: models.AdapterFake,
		ContextWindow: 8192, OutputLimit: 1, Capabilities: []models.Capability{models.CapEmbedding}, Class: models.ClassLocal,
	}
	embedCheckChat = models.Model{
		Name: "local-chat-example", Label: "Local Chat Example", Adapter: models.AdapterAnthropic, Endpoint: "http://localhost:8080/v1",
		ProviderModel: "small-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal,
	}
	embedCheckHosted = models.Model{
		Name: "hosted-embed-example", Label: "Hosted Embed Example", Adapter: models.AdapterChatCompletions,
		ProviderModel: "example-embed-small", ContextWindow: 8192, OutputLimit: 1, Capabilities: []models.Capability{models.CapEmbedding},
		Class: models.ClassHosted, Price: models.Price{Input: 0.02},
	}
)

// embedCheckPassword is invented, and guards a folder that is removed.
const embedCheckPassword = "an invented phrase for the embeddings check"

// standIn is an embedding endpoint on this machine that keeps the body of
// every request and answers with the fake's vectors.
type standIn struct {
	srv   *httptest.Server
	maker *fake.Provider
	mu    sync.Mutex
	got   []string
}

func newStandIn(maker *fake.Provider) *standIn {
	s := &standIn{maker: maker}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.got = append(s.got, string(raw))
		s.mu.Unlock()
		var in struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if r.URL.Path != "/v1/embeddings" || json.Unmarshal(raw, &in) != nil || len(in.Input) == 0 {
			http.Error(w, `{"error":{"message":"not an embedding call"}}`, http.StatusBadRequest)
			return
		}
		resp, err := s.maker.Embed(r.Context(), seam.EmbedRequest{Model: in.Model, Texts: in.Input})
		if err != nil {
			http.Error(w, `{"error":{"message":"the stand-in could not answer"}}`, http.StatusInternalServerError)
			return
		}
		var data []map[string]any
		for i, v := range resp.Vectors {
			data = append(data, map[string]any{"object": "embedding", "index": i, "embedding": v})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "model": in.Model, "data": data, "usage": map[string]int{"prompt_tokens": resp.Usage.InputTokens}})
	}))
	return s
}

// holding returns the requests the stand-in was sent that hold a word, in
// any letter case.
func (s *standIn) holding(words ...string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, body := range s.got {
		if m := marker(body, words); m != "" {
			out = append(out, m)
		}
	}
	return out
}

func (s *standIn) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

func checkEmbeddings(root string) error {
	mfx, err := readMeaningFixture(root)
	if err != nil {
		return err
	}
	reg, err := settings.Registry()
	if err != nil {
		return err
	}
	opts, err := fake.DefaultOptions()
	if err != nil {
		return err
	}
	maker, err := fake.New(nil, fake.Options{EmbeddingSize: opts.EmbeddingSize, Meanings: mfx.Meanings})
	if err != nil {
		return err
	}
	stand := newStandIn(maker)
	defer stand.srv.Close()
	cc, err := adapters.ChatCompletions(reg)
	if err != nil {
		return err
	}
	e := &ears{sent: map[string][]string{}, down: map[string]bool{}}
	set := adapters.Set{
		models.AdapterFake:            func(models.Model, string) (seam.Provider, error) { return maker, nil },
		models.AdapterAnthropic:       e.builder,
		models.AdapterChatCompletions: cc[models.AdapterChatCompletions],
	}
	g, err := openKnowledgeRig(root, "keystone-embeddings-", set)
	if err != nil {
		return err
	}
	defer g.done()
	problems, err := meaningProblems(g, mfx)
	if err != nil {
		return err
	}
	more, err := embeddingRuleProblems(g, mfx, stand)
	if err != nil {
		return err
	}
	problems = append(problems, more...)
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

// refreshNow runs a person's refresh through the API and reads its answer.
func (g *knowledgeRig) refreshNow(token string) (turn.EmbeddingsReply, error) {
	var out turn.EmbeddingsReply
	rec := g.w.request("POST", api.Prefix+"knowledge/embeddings/refresh", token, "", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		return out, fmt.Errorf("POST /knowledge/embeddings/refresh answered %d: %s", rec.Code, rec.Body.String())
	}
	return out, nil
}

func meaningProblems(g *knowledgeRig, mfx meaningFixture) ([]string, error) {
	ctx := context.Background()
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	token, rec := g.w.signIn(g.test.ID, g.testPassword)
	if token == "" {
		return nil, fmt.Errorf("the test tenant cannot sign in through the API: status %d", rec.Code)
	}
	err := g.with(g.test.ID, func(s *tenant.Store) error {
		return models.Add(ctx, s, models.Limits{MaxModels: 50}, embedCheckFake)
	})
	if err != nil {
		return nil, err
	}
	type question struct{ ask, answer string }
	var all []question
	for _, q := range g.fx.Recall.Questions {
		all = append(all, question{q.Ask, q.Answer})
	}
	for _, q := range mfx.Questions {
		all = append(all, question{q.Ask, q.Answer})
	}
	answered := func(store string) (int, []string, error) {
		n := 0
		var missed []string
		for _, q := range all {
			t, err := g.send(token, "", q.ask, "")
			if err != nil {
				return 0, nil, err
			}
			call, _, err := g.calls(g.test.ID, t.requestID)
			if err != nil {
				return 0, nil, err
			}
			if strings.Contains(call, q.answer) {
				n++
			} else {
				missed = append(missed, q.ask)
			}
		}
		return n, missed, nil
	}

	for _, store := range knowledgeStores {
		if err := g.use(g.test.ID, store); err != nil {
			return nil, err
		}
		if store == waymarks.Name {
			if err := g.with(g.test.ID, func(s *tenant.Store) error { return s.Secrets().Set(waymarks.SecretEmbedder, g.fw.token) }); err != nil {
				return nil, err
			}
		}
		done, err := g.refreshNow(token)
		if err != nil {
			return nil, err
		}
		if done.Last == nil || done.Last.Embedded != len(g.fx.Pages) || done.Last.Held != 0 || done.Model == nil || done.Model.Name != embedCheckFake.Name {
			say("%s: the refresh with the local fake embedding model made %v vectors for %d pages: %+v", store, done.Last, len(g.fx.Pages), done)
			continue
		}

		if err := g.set(g.test.ID, recall.SettingMeaning, "false"); err != nil {
			return nil, err
		}
		words, _, err := answered(store)
		if err != nil {
			return nil, err
		}
		if err := g.set(g.test.ID, recall.SettingMeaning, "true"); err != nil {
			return nil, err
		}
		both, missed, err := answered(store)
		if err != nil {
			return nil, err
		}
		if both <= words {
			say("%s: by words and meaning %d of %d questions were answered, and by words alone %d: no more (missed: %s)", store, both, len(all), words, strings.Join(missed, "; "))
		}
		for _, q := range g.fx.Recall.Questions {
			for _, m := range missed {
				if m == q.Ask {
					say("%s: with recall by meaning on, the recall fixture's question %q was not answered", store, q.Ask)
				}
			}
		}
		// Waymarks ranks a search by vector by its place and never says how
		// near a page is, so its nearest pages join every message and a
		// message about nothing is checked on the built-in store alone,
		// whose threshold is a cosine (decision 0411).
		nothing := g.fx.Recall.Nothing
		if store != builtin.Name {
			nothing = nil
		}
		for _, ask := range nothing {
			t, err := g.send(token, "", ask, "")
			if err != nil {
				return nil, err
			}
			call, _, err := g.calls(g.test.ID, t.requestID)
			if err != nil {
				return nil, err
			}
			found, ok, err := g.record(g.test.ID, t.turnID)
			if err != nil {
				return nil, err
			}
			if strings.Contains(call, recalledMark) || !ok || found.Kept != 0 {
				say("%s: with recall by meaning on, %q is about nothing in the store and recalled %d items", store, ask, found.Kept)
			}
		}

		// A changed page, alone, gets a new vector.
		if store == builtin.Name {
			p := g.fx.Pages[0]
			err := g.with(g.test.ID, func(s *tenant.Store) error {
				_, err := builtin.WritePage(ctx, s, builtin.NewPage{Space: p.Space, Path: p.Path, Title: p.Title, Kind: p.Kind, Body: p.Body + " It was checked again this year."})
				return err
			})
			if err != nil {
				return nil, err
			}
			again, err := g.refreshNow(token)
			if err != nil {
				return nil, err
			}
			if again.Last == nil || again.Last.Embedded != 1 || again.Last.Listed != 1 {
				say("%s: after one page changed the refresh made %+v", store, again.Last)
			}
		}
	}
	return problems, nil
}

func embeddingRuleProblems(g *knowledgeRig, mfx meaningFixture, stand *standIn) ([]string, error) {
	ctx := context.Background()
	fx := g.fx
	markers := fx.Sensitive.Markers
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if m := marker(mfx.Held.Ask, markers); m != "" {
		return nil, fmt.Errorf("the held question holds the marker %q", m)
	}
	sensitivePages := 0
	for _, p := range fx.Pages {
		if g.sensitiveSpace(p.Space) {
			sensitivePages++
		}
	}

	person, err := g.in.Create("Embeddings Check Person")
	if err != nil {
		return nil, err
	}
	hosted := embedCheckHosted
	hosted.Endpoint = stand.srv.URL + "/v1"
	err = g.with(person.ID, func(s *tenant.Store) error {
		o, err := signin.Defaults()
		if err != nil {
			return err
		}
		if err := signin.SetPassword(s, embedCheckPassword, o); err != nil {
			return err
		}
		for _, m := range []models.Model{embedCheckChat, hosted} {
			if err := models.Add(ctx, s, models.Limits{MaxModels: 50}, m); err != nil {
				return fmt.Errorf("adding %s: %w", m.Name, err)
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
	if err := g.set(person.ID, knowledge.SettingAdapter, builtin.Name, turn.KeyEmbeddingModel, hosted.Name, turn.KeyTitleAuto, "false"); err != nil {
		return nil, err
	}
	token, rec := g.w.signIn(person.ID, embedCheckPassword)
	if token == "" {
		return nil, fmt.Errorf("the invented tenant cannot sign in through the API: status %d", rec.Code)
	}

	// The refresh with the hosted model.
	done, err := g.refreshNow(token)
	if err != nil {
		return nil, err
	}
	switch {
	case done.Last == nil:
		say("the refresh with the hosted embedding model kept no state: %+v", done)
	case done.Last.Held != sensitivePages || done.Last.Embedded != len(fx.Pages)-sensitivePages:
		say("the refresh with the hosted embedding model embedded %d and held %d pages, want %d and %d", done.Last.Embedded, done.Last.Held, len(fx.Pages)-sensitivePages, sensitivePages)
	case !strings.Contains(done.Note, "sensitive space") || marker(done.Note, markers) != "":
		say("the person is not told that sensitive pages were left without a vector, or is told their names: %q", done.Note)
	}
	if stand.count() == 0 {
		say("the hosted embedding model's stand-in was sent nothing, so the check proves nothing")
	}
	err = g.with(person.ID, func(s *tenant.Store) error {
		ks, err := knowledge.Open(ctx, builtin.Name, s)
		if err != nil {
			return err
		}
		defer ks.Close()
		needs, err := ks.VectorsNeeded(ctx, hosted.ProviderModel, 0)
		if err != nil {
			return err
		}
		for _, n := range needs {
			if !n.Label.Sensitive() {
				say("after the hosted refresh the normal page %q still has no vector", n.Title)
			}
		}
		if len(needs) != sensitivePages {
			say("after the hosted refresh %d pages have no vector, want the %d sensitive ones", len(needs), sensitivePages)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// A chat that was sent sensitive content keeps its next message from
	// the hosted embedding model.
	chat, err := g.send(token, "", fx.Sensitive.Ask, embedCheckChat.Name)
	if err != nil {
		return nil, err
	}
	if found, ok, err := g.record(person.ID, chat.turnID); err != nil {
		return nil, err
	} else if !ok || !found.Sensitive {
		say("the local chat model was not given the sensitive page, so the chat is not held and the check proves nothing: %+v", found)
	}
	held, err := g.send(token, chat.chatID, mfx.Held.Ask, embedCheckChat.Name)
	if err != nil {
		return nil, err
	}
	if got := stand.holding(mfx.Held.Word); len(got) > 0 {
		say("the message of a chat that holds sensitive content reached the hosted embedding model")
	}
	if found, ok, err := g.record(person.ID, held.turnID); err != nil {
		return nil, err
	} else if !ok || !strings.Contains(found.MeaningNote, "sensitive") {
		say("the held turn's record does not say why it searched by words alone: %+v", found)
	}

	// A local embedding model, chosen first: every page gets a vector of
	// the new model, the sensitive one too.
	err = g.with(person.ID, func(s *tenant.Store) error {
		return models.Add(ctx, s, models.Limits{MaxModels: 50}, embedCheckFake)
	})
	if err != nil {
		return nil, err
	}
	if err := g.set(person.ID, turn.KeyEmbeddingModel, ""); err != nil {
		return nil, err
	}
	local, err := g.refreshNow(token)
	if err != nil {
		return nil, err
	}
	if local.Last == nil || local.Model == nil || local.Model.Name != embedCheckFake.Name || local.Last.Embedded != len(fx.Pages) || local.Last.Held != 0 {
		say("with a local embedding model the refresh was %+v, want every page of the new model, the sensitive ones too", local.Last)
	}

	// Last: no request the stand-in was sent holds a word of the sensitive
	// space, and every embedding call that ended has a usage record.
	if got := stand.holding(markers...); len(got) > 0 {
		say("the hosted embedding model was sent the sensitive space's words %v", got)
	}
	err = g.with(person.ID, func(s *tenant.Store) error {
		entries, err := audit.List(ctx, s, audit.Filter{Kind: audit.KindModel})
		if err != nil {
			return err
		}
		records, err := usage.List(ctx, s, usage.Filter{Limit: 1000})
		if err != nil {
			return err
		}
		recorded := map[string]int{}
		for _, r := range records {
			if r.Kind == "embedding" {
				recorded[r.RequestID+"/"+r.Model]++
			}
		}
		ended := map[string]int{}
		for _, e := range entries {
			if e.Outcome != audit.OutcomeStarted && (e.Action == hosted.Name || e.Action == embedCheckFake.Name) {
				ended[e.RequestID+"/"+e.Action]++
				if marker(e.Sent+e.Received, markers) != "" && e.Action == hosted.Name {
					say("the audit log holds a call to the hosted embedding model with the sensitive space's words")
				}
			}
		}
		if len(ended) == 0 {
			say("the audit log holds no embedding call")
		}
		for key, n := range ended {
			if recorded[key] != n {
				say("the embedding calls %s ended %d times and have %d usage records", key, n, recorded[key])
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return problems, nil
}
