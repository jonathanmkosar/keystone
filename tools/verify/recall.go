package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/knowledge/personmap"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/knowledge/waymarks"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/settings"
)

// The "recall" check (P6-05): a message about something in the person's
// knowledge store brings it into the model call, and a message about
// nothing in the store brings nothing.
//
// It runs the real server, with the shipped settings, on a throwaway data
// folder with the test tenant in it. The invented knowledge in
// fixtures/test-tenant/knowledge is put into the test tenant's store twice
// over: into the built-in store, and into a fake Waymarks on this machine
// that the Waymarks adapter calls. For each store:
//
//   - Each question of the fixture is sent as the one message of a new
//     chat, and the call the model was sent, as the audit log holds it,
//     must hold the answer. The answers sit in pages the person map does
//     not name, and in a source, which the map never names, and the check
//     proves the map does not name them. The share of questions answered
//     from the store may not fall below the fixture's floor.
//   - Each message about nothing in the store is sent, and its call holds
//     nothing recalled.
//   - Each call holds the search-first rule.
//
// With the none adapter nothing is searched, nothing is recorded, and the
// rule is not sent.
//
// The test tenant's one model is local, so it may read sensitive content.
// The sensitive content check, in sensitive.go, is the other half.

// recalledMark starts every recalled item, so a call that holds one says so.
const recalledMark = "From the person's knowledge store"

// knowledgeFixture is fixtures/test-tenant/knowledge/knowledge.json.
type knowledgeFixture struct {
	TestData bool   `json:"test_data"`
	About    string `json:"about"`
	Spaces   []struct {
		Name  string `json:"name"`
		Label string `json:"label"`
	} `json:"spaces"`
	Pages []struct {
		Space string `json:"space"`
		Path  string `json:"path"`
		Title string `json:"title"`
		Kind  string `json:"kind"`
		Body  string `json:"body"`
	} `json:"pages"`
	Sources []struct {
		Space  string `json:"space"`
		Name   string `json:"name"`
		Origin string `json:"origin"`
		Dated  string `json:"dated"`
		Text   string `json:"text"`
	} `json:"sources"`
	Recall struct {
		Floor     float64 `json:"floor"`
		Questions []struct {
			Ask     string `json:"ask"`
			Answer  string `json:"answer"`
			Unnamed string `json:"unnamed"`
		} `json:"questions"`
		Nothing []string `json:"nothing"`
	} `json:"recall"`
	Sensitive struct {
		Ask     string   `json:"ask"`
		Plain   string   `json:"plain"`
		Markers []string `json:"markers"`
	} `json:"sensitive"`
}

func readKnowledgeFixture(root string) (knowledgeFixture, error) {
	var fx knowledgeFixture
	path := filepath.Join(root, "fixtures", "test-tenant", "knowledge", "knowledge.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fx, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fx); err != nil {
		return fx, fmt.Errorf("%s does not read: %w", path, err)
	}
	sensitive := 0
	for _, sp := range fx.Spaces {
		if knowledge.Label(sp.Label).Sensitive() {
			sensitive++
		}
	}
	switch {
	case !fx.TestData:
		return fx, fmt.Errorf("%s is not marked as test data", path)
	case len(fx.Recall.Questions) == 0 || len(fx.Recall.Nothing) == 0:
		return fx, fmt.Errorf("%s has no questions, or no message about nothing", path)
	case fx.Recall.Floor <= 0 || fx.Recall.Floor > 1:
		return fx, fmt.Errorf("%s: the floor is %v, and it is a share above 0 and up to 1", path, fx.Recall.Floor)
	case sensitive == 0 || len(fx.Sensitive.Markers) == 0 || fx.Sensitive.Ask == "" || fx.Sensitive.Plain == "":
		return fx, fmt.Errorf("%s needs a sensitive space, its markers, a question about it and a plain question", path)
	}
	return fx, nil
}

// knowledgeStores are the two stores the checks run on.
var knowledgeStores = []string{builtin.Name, waymarks.Name}

// knowledgeRig is a throwaway data folder with the test tenant in it, the
// fixture's knowledge in a fake Waymarks, and the real server.
type knowledgeRig struct {
	fx   knowledgeFixture
	reg  []settings.Setting
	in   *tenant.Install
	w    *walker
	fw   *fakeWaymarks
	test tenant.Tenant
	// testPassword is the test tenant's published fixture password.
	testPassword string
	// sensitiveIDs are the ids of the first page and the first source in a
	// sensitive space, by tenant and store.
	sensitiveIDs map[string][2]string
	done         func()
}

func openKnowledgeRig(root, prefix string, set adapters.Set) (*knowledgeRig, error) {
	fx, err := readKnowledgeFixture(root)
	if err != nil {
		return nil, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return nil, err
	}
	g := &knowledgeRig{fx: fx, reg: reg, sensitiveIDs: map[string][2]string{}}
	var closers []func()
	g.done = func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	closers = append(closers, func() { os.RemoveAll(dir) })
	fail := func(err error) (*knowledgeRig, error) {
		g.done()
		return nil, err
	}
	if g.in, err = tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper()); err != nil {
		return fail(err)
	}
	shipped, err := testtenant.Shipped()
	if err != nil {
		return fail(err)
	}
	o, err := testtenant.DefaultOptions()
	if err != nil {
		return fail(err)
	}
	loaded, err := testtenant.Load(g.in, shipped, o)
	if err != nil {
		return fail(err)
	}
	g.test, g.testPassword = loaded.Tenant, shipped.Password

	g.fw = newFakeWaymarks()
	closers = append(closers, g.fw.close)
	page, source := "", ""
	for _, sp := range fx.Spaces {
		g.fw.addSpace(sp.Name, sp.Label)
	}
	for _, p := range fx.Pages {
		id := strconv.FormatInt(g.fw.addPage(p.Space, p.Path, p.Title, p.Kind, p.Body), 10)
		if page == "" && g.sensitiveSpace(p.Space) {
			page = id
		}
	}
	for _, s := range fx.Sources {
		id := strconv.FormatInt(g.fw.addSource(s.Space, s.Name, s.Origin, s.Dated, s.Text), 10)
		if source == "" && g.sensitiveSpace(s.Space) {
			source = id
		}
	}
	g.sensitiveIDs[waymarks.Name] = [2]string{page, source}
	if err := g.seed(g.test.ID); err != nil {
		return fail(err)
	}

	c, err := app.Defaults()
	if err != nil {
		return fail(err)
	}
	g.w = &walker{log: &bytes.Buffer{}}
	c.Install, c.Log, c.Adapters = g.in, g.w.log, set
	var stop func()
	if g.w.h, stop, err = app.Open(c); err != nil {
		return fail(err)
	}
	closers = append(closers, stop)
	return g, nil
}

func (g *knowledgeRig) sensitiveSpace(name string) bool {
	for _, sp := range g.fx.Spaces {
		if sp.Name == name {
			return knowledge.Label(sp.Label).Sensitive()
		}
	}
	return true
}

// with runs f on a tenant's store.
func (g *knowledgeRig) with(tenantID string, f func(s *tenant.Store) error) error {
	s, err := g.in.Store(tenantID)
	if err != nil {
		return err
	}
	defer s.Close()
	return f(s)
}

// seed puts the fixture's knowledge into a tenant's built-in store, and
// gives the tenant the fake Waymarks' token.
func (g *knowledgeRig) seed(tenantID string) error {
	ctx := context.Background()
	return g.with(tenantID, func(s *tenant.Store) error {
		page, source := "", ""
		for _, p := range g.fx.Pages {
			written, err := builtin.WritePage(ctx, s, builtin.NewPage{Space: p.Space, Path: p.Path, Title: p.Title, Kind: p.Kind, Body: p.Body})
			if err != nil {
				return err
			}
			if page == "" && g.sensitiveSpace(p.Space) {
				page = written.ID
			}
		}
		for _, src := range g.fx.Sources {
			added, err := builtin.AddText(ctx, s, builtin.NewText{Space: src.Space, Name: src.Name, Origin: src.Origin, Dated: src.Dated, Text: src.Text})
			if err != nil {
				return err
			}
			if source == "" && g.sensitiveSpace(src.Space) {
				source = added.ID
			}
		}
		for _, sp := range g.fx.Spaces {
			if knowledge.Label(sp.Label).Sensitive() {
				if err := builtin.SetLabel(ctx, s, sp.Name, knowledge.LabelSensitive); err != nil {
					return err
				}
			}
		}
		g.sensitiveIDs[tenantID] = [2]string{page, source}
		return s.Secrets().Set(waymarks.SecretReader, g.fw.token)
	})
}

// ids returns the ids of a sensitive page and source in one tenant's store.
func (g *knowledgeRig) ids(tenantID, store string) [2]string {
	if store == waymarks.Name {
		return g.sensitiveIDs[waymarks.Name]
	}
	return g.sensitiveIDs[tenantID]
}

// set stores one of a tenant's settings, as the person.
func (g *knowledgeRig) set(tenantID string, values ...string) error {
	return g.with(tenantID, func(s *tenant.Store) error {
		for i := 0; i+1 < len(values); i += 2 {
			if err := tenantsettings.Set(context.Background(), s, g.reg, tenantsettings.Person, values[i], values[i+1]); err != nil {
				return fmt.Errorf("setting %s: %w", values[i], err)
			}
		}
		return nil
	})
}

// use gives a tenant one of the stores, or none.
func (g *knowledgeRig) use(tenantID, store string) error {
	if store == waymarks.Name {
		return g.set(tenantID, knowledge.SettingAdapter, store, waymarks.SettingAddress, g.fw.srv.URL)
	}
	return g.set(tenantID, knowledge.SettingAdapter, store)
}

// recallSent is one turn as the knowledge checks see it.
type recallSent struct {
	chatID, turnID, requestID string
	stream                    string
}

// send sends one message, in the chat named or a new one, on the model
// named or the chat's own, and reads the whole stream.
func (g *knowledgeRig) send(token, chatID, text, model string) (recallSent, error) {
	if chatID == "" {
		rec := g.w.request("POST", api.Prefix+"chats", token, "", nil)
		var made turn.ChatReply
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil || made.Chat.ID == "" {
			return recallSent{}, fmt.Errorf("a chat cannot be made through the API: status %d", rec.Code)
		}
		chatID = made.Chat.ID
	}
	body, _ := json.Marshal(turn.SendBody{Text: text, Model: model})
	rec := g.w.request("POST", api.Prefix+"chats/"+chatID+"/turns", token, string(body), nil)
	out := recallSent{chatID: chatID, turnID: rec.Header().Get("X-Turn-Id"), requestID: rec.Header().Get("X-Request-Id"), stream: rec.Body.String()}
	if rec.Code != http.StatusOK || !strings.Contains(out.stream, "event: "+turn.EventEnd+"\n") {
		return out, fmt.Errorf("a turn did not stream to its end: status %d, %d bytes", rec.Code, len(out.stream))
	}
	return out, nil
}

// calls returns what each model call of a request was sent, as the audit
// log holds it, keyed by the model's name in order.
func (g *knowledgeRig) calls(tenantID, requestID string) (all string, byModel map[string][]string, err error) {
	byModel = map[string][]string{}
	err = g.with(tenantID, func(s *tenant.Store) error {
		entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if err != nil {
			return err
		}
		var b strings.Builder
		for _, e := range entries {
			if e.Outcome == audit.OutcomeStarted {
				byModel[e.Action] = append(byModel[e.Action], e.Sent)
				b.WriteString(e.Sent + "\n")
			}
		}
		all = b.String()
		return nil
	})
	return all, byModel, err
}

// record returns what a turn's recall found.
func (g *knowledgeRig) record(tenantID, turnID string) (recall.Record, bool, error) {
	var rec recall.Record
	var ok bool
	err := g.with(tenantID, func(s *tenant.Store) error {
		var err error
		rec, ok, err = recall.Saved(context.Background(), s, turnID)
		return err
	})
	return rec, ok, err
}

// defaultText is a setting's shipped value.
func (g *knowledgeRig) defaultText(key string) string {
	st, _ := settings.Lookup(g.reg, key)
	return st.Default
}

// mapLimits are the person map's limits as shipped.
func (g *knowledgeRig) mapLimits() (personmap.Limits, error) {
	whole := func(key string) int {
		n, _ := strconv.Atoi(g.defaultText(key))
		return n
	}
	per, err := strconv.ParseFloat(g.defaultText("budget.chars_per_token"), 64)
	if err != nil {
		return personmap.Limits{}, err
	}
	l := personmap.Limits{
		ProfileMaxChars: whole(personmap.SettingProfileMaxChars), MapMaxTokens: whole(personmap.SettingMapMaxTokens),
		TitlesPerKind: whole(personmap.SettingMapTitlesPerKind), TitleMaxChars: whole(personmap.SettingMapTitleMaxChars),
		CharsPerToken: per,
	}
	return l, personmap.CheckLimits(l)
}

// personMap is the whole map of a tenant's store, as a model that may read
// sensitive content is given it.
func (g *knowledgeRig) personMap(tenantID, store string) (string, error) {
	l, err := g.mapLimits()
	if err != nil {
		return "", err
	}
	var text string
	err = g.with(tenantID, func(s *tenant.Store) error {
		ks, err := knowledge.Open(context.Background(), store, s)
		if err != nil {
			return err
		}
		defer ks.Close()
		p, err := personmap.ForContext(context.Background(), s, ks, true, l)
		if err != nil {
			return err
		}
		if p.StoreErr != nil {
			return p.StoreErr
		}
		text = p.Map
		return nil
	})
	return text, err
}

func checkRecall(root string) error {
	g, err := openKnowledgeRig(root, "keystone-recall-", nil)
	if err != nil {
		return err
	}
	defer g.done()
	problems, err := recallProblems(g)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func recallProblems(g *knowledgeRig) ([]string, error) {
	var problems []string
	token, rec := g.w.signIn(g.test.ID, g.testPassword)
	if token == "" {
		return nil, fmt.Errorf("the test tenant cannot sign in through the API: status %d", rec.Code)
	}
	rule := strings.TrimSpace(g.defaultText(recall.SettingSearchFirstText))
	if rule == "" {
		return nil, errors.New(recall.SettingSearchFirstText + " ships empty, so the search-first rule cannot be checked")
	}
	ruleSent := func(call string) bool {
		quoted, _ := json.Marshal(rule)
		return strings.Contains(call, strings.Trim(string(quoted), `"`))
	}

	for _, store := range knowledgeStores {
		if err := g.use(g.test.ID, store); err != nil {
			return nil, err
		}
		whole, err := g.personMap(g.test.ID, store)
		if err != nil {
			return nil, fmt.Errorf("%s: the person map cannot be read: %w", store, err)
		}
		answered := 0
		for _, q := range g.fx.Recall.Questions {
			if q.Unnamed != "" && strings.Contains(whole, q.Unnamed) {
				problems = append(problems, fmt.Sprintf("%s: the person map names %q, so the question %q does not show recall going past the map", store, q.Unnamed, q.Ask))
			}
			t, err := g.send(token, "", q.Ask, "")
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
			switch {
			case call == "":
				problems = append(problems, fmt.Sprintf("%s: %q made no model call the audit log holds", store, q.Ask))
			case strings.Contains(call, q.Answer):
				answered++
			default:
				problems = append(problems, fmt.Sprintf("%s: %q did not bring %q into the call; recall kept %d of %d hits (record kept: %v, note %q)", store, q.Ask, q.Answer, found.Kept, found.Found, ok, found.Note))
			}
			if call != "" && !ruleSent(call) {
				problems = append(problems, fmt.Sprintf("%s: the call for %q does not hold the search-first rule", store, q.Ask))
			}
		}
		share := float64(answered) / float64(len(g.fx.Recall.Questions))
		if share < g.fx.Recall.Floor {
			problems = append(problems, fmt.Sprintf("%s: %d of %d questions were answered from the store, a share of %.2f, under the floor of %.2f in the fixture", store, answered, len(g.fx.Recall.Questions), share, g.fx.Recall.Floor))
		}

		for _, ask := range g.fx.Recall.Nothing {
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
				problems = append(problems, fmt.Sprintf("%s: %q is about nothing in the store and recalled %d items (record kept: %v)", store, ask, found.Kept, ok))
			}
		}
	}

	// With the none adapter nothing is searched, kept or told.
	if err := g.use(g.test.ID, knowledge.None); err != nil {
		return nil, err
	}
	q := g.fx.Recall.Questions[0]
	t, err := g.send(token, "", q.Ask, "")
	if err != nil {
		return nil, err
	}
	call, _, err := g.calls(g.test.ID, t.requestID)
	if err != nil {
		return nil, err
	}
	if _, ok, err := g.record(g.test.ID, t.turnID); err != nil {
		return nil, err
	} else if ok || strings.Contains(call, recalledMark) || ruleSent(call) {
		problems = append(problems, "with the none adapter, a turn searched, recorded a recall or sent the search-first rule")
	}
	return problems, nil
}
