package audit

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// twoStores makes an install with two tenants and returns the install and
// an open handle for each. Every name here is invented.
func twoStores(t *testing.T) (in *tenant.Install, a, b *tenant.Store) {
	t.Helper()
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"Test Person One", "Test Person Two"} {
		tn, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(tn.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		if i == 0 {
			a = s
		} else {
			b = s
		}
	}
	return in, a, b
}

// rawLog returns every text column of every row, read without this package.
func rawLog(t *testing.T, s *tenant.Store) string {
	t.Helper()
	var all string
	err := s.DB().QueryRow(`SELECT coalesce(group_concat(
		request_id || char(10) || actor || char(10) || action || char(10) || sent || char(10) || received, char(10)), '')
		FROM audit_log`).Scan(&all)
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func mustAppend(t *testing.T, s *tenant.Store, e Entry, secrets ...string) Entry {
	t.Helper()
	got, err := Append(context.Background(), s, e, secrets...)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustList(t *testing.T, s *tenant.Store, f Filter) []Entry {
	t.Helper()
	got, err := List(context.Background(), s, f)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAppendAndList(t *testing.T) {
	_, a, b := twoStores(t)
	before := time.Now().UTC().Add(-time.Second)
	// The seam's usage converts as it is. If the two shapes ever differ,
	// this file stops compiling.
	used := seam.Usage{InputTokens: 120, OutputTokens: 30, CacheReadTokens: 800, CacheWriteTokens: 10, ThinkingTokens: 12, Estimated: true}

	api := mustAppend(t, a, Entry{Kind: KindAPI, RequestID: "req-one", Actor: "person", Action: "POST /api/chats/c1/turns", Outcome: OutcomeOK, Status: 200, DurationMS: 40})
	model := mustAppend(t, a, Entry{
		// A caller cannot choose the id or place an entry in the past.
		ID: 999, At: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC),
		Kind: KindModel, RequestID: "req-one", Actor: "person", Action: "fake-small", Outcome: OutcomeOK,
		Sent:       `{"messages":[{"role":"user","blocks":[{"type":"text","text":"What is on today?"}]}]}`,
		Received:   `{"message":{"role":"assistant","blocks":[{"type":"text","text":"Two meetings."}]}}`,
		Usage:      Usage(used),
		CostMicros: 4321, DurationMS: 950,
	})
	if seam.Usage(model.Usage) != used {
		t.Errorf("the usage was written as %+v, want %+v", model.Usage, used)
	}
	tool := mustAppend(t, a, Entry{Kind: KindTool, RequestID: "req-two", Actor: "schedule:morning", Action: "web_search", Outcome: OutcomeError,
		Sent: `{"query":"weather"}`, Received: `{"error":"the search service did not answer"}`, DurationMS: 5000})

	if api.ID >= model.ID || model.ID >= tool.ID {
		t.Errorf("ids do not rise: %d, %d, %d", api.ID, model.ID, tool.ID)
	}
	if model.ID == 999 || model.At.Before(before) {
		t.Errorf("the caller's id or time was kept: id %d at %s", model.ID, model.At)
	}

	all := mustList(t, a, Filter{})
	if want := []Entry{api, model, tool}; !reflect.DeepEqual(all, want) {
		t.Fatalf("the log reads back differently:\n got %+v\nwant %+v", all, want)
	}
	if got := mustList(t, a, Filter{Kind: KindModel}); len(got) != 1 || got[0].ID != model.ID {
		t.Errorf("filter by kind: %+v", got)
	}
	if got := mustList(t, a, Filter{RequestID: "req-one"}); len(got) != 2 {
		t.Errorf("filter by request id: %+v", got)
	}
	if got := mustList(t, a, Filter{AfterID: api.ID, Limit: 1}); len(got) != 1 || got[0].ID != model.ID {
		t.Errorf("a page after the first entry: %+v", got)
	}
	if got := mustList(t, b, Filter{}); len(got) != 0 {
		t.Errorf("tenant two's log holds tenant one's entries: %+v", got)
	}
}

func TestUsageIsTheSeamsUsage(t *testing.T) {
	ours, theirs := reflect.TypeOf(Usage{}), reflect.TypeOf(seam.Usage{})
	if ours.NumField() != theirs.NumField() {
		t.Fatalf("audit.Usage has %d fields and seam.Usage has %d", ours.NumField(), theirs.NumField())
	}
	for i := range ours.NumField() {
		a, b := ours.Field(i), theirs.Field(i)
		if a.Name != b.Name || a.Type != b.Type || a.Tag != b.Tag {
			t.Errorf("field %d: audit has %s %s `%s`, seam has %s %s `%s`", i, a.Name, a.Type, a.Tag, b.Name, b.Type, b.Tag)
		}
	}
	// What the seam refuses, the log refuses.
	for _, u := range []seam.Usage{
		{InputTokens: 1, OutputTokens: 2, ThinkingTokens: 2},
		{InputTokens: -1},
		{OutputTokens: 1, ThinkingTokens: 2},
		{CacheWriteTokens: -1},
	} {
		e := Entry{Kind: KindModel, RequestID: "req-one", Actor: "person", Action: "fake-small", Outcome: OutcomeOK, Usage: Usage(u)}
		if (seam.ValidateUsage(u) == nil) != (Validate(e) == nil) {
			t.Errorf("the seam and the log disagree on %+v", u)
		}
	}
}

func TestAnEntryCannotBeChangedDeletedOrReplaced(t *testing.T) {
	_, a, _ := twoStores(t)
	first := mustAppend(t, a, Entry{Kind: KindTool, RequestID: "req-one", Actor: "person", Action: "send_mail", Outcome: OutcomeOK, Sent: `{"to":"Test Person Two"}`})
	mustAppend(t, a, Entry{Kind: KindAPI, RequestID: "req-two", Actor: "person", Action: "GET /api/chats", Outcome: OutcomeOK, Status: 200})
	before := mustList(t, a, Filter{})

	insert := ` INTO audit_log(id, at, kind, request_id, actor, action, outcome)
		VALUES(?, '2026-11-24T00:00:00.000Z', 'tool', 'req-one', 'person', 'nothing_happened', 'ok')`
	attempts := map[string]struct {
		sql  string
		args []any
	}{
		"change one field":       {"UPDATE audit_log SET sent = '' WHERE id = ?", []any{first.ID}},
		"change every row":       {"UPDATE audit_log SET outcome = 'ok'", nil},
		"change the id":          {"UPDATE audit_log SET id = id + 100 WHERE id = ?", []any{first.ID}},
		"delete one row":         {"DELETE FROM audit_log WHERE id = ?", []any{first.ID}},
		"delete every row":       {"DELETE FROM audit_log", nil},
		"insert or replace":      {"INSERT OR REPLACE" + insert, []any{first.ID}},
		"replace":                {"REPLACE" + insert, []any{first.ID}},
		"insert, update if held": {"INSERT" + insert + " ON CONFLICT(id) DO UPDATE SET action = excluded.action", []any{first.ID}},
		"insert over a held id":  {"INSERT" + insert, []any{first.ID}},
		// A row with id -1 would make every later append look like a replace.
		"insert with id -1": {"INSERT" + insert, []any{-1}},
		"insert with id 0":  {"INSERT" + insert, []any{0}},
	}
	for name, at := range attempts {
		if _, err := a.DB().Exec(at.sql, at.args...); err == nil {
			t.Errorf("%s: the database allowed it", name)
		}
		// The same again inside a transaction, which must not commit it.
		tx, err := a.DB().Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(at.sql, at.args...); err == nil {
			t.Errorf("%s, in a transaction: the database allowed it", name)
		}
		tx.Commit()
	}
	if after := mustList(t, a, Filter{}); !reflect.DeepEqual(after, before) {
		t.Fatalf("the log changed:\n got %+v\nwant %+v", after, before)
	}

	// Adding is still allowed.
	next := mustAppend(t, a, Entry{Kind: KindAPI, RequestID: "req-three", Actor: "person", Action: "GET /api/chats", Outcome: OutcomeOK, Status: 200})
	if after := mustList(t, a, Filter{}); len(after) != len(before)+1 || after[len(after)-1].ID != next.ID {
		t.Errorf("an entry could not be added after the refused attempts: %+v", after)
	}
}

func TestAppendRefusesAMalformedEntry(t *testing.T) {
	_, a, _ := twoStores(t)
	good := Entry{Kind: KindModel, RequestID: "req-one", Actor: "person", Action: "fake-small", Outcome: OutcomeOK}
	if err := Validate(good); err != nil {
		t.Fatalf("a good entry was refused: %v", err)
	}
	change := func(f func(*Entry)) Entry { e := good; f(&e); return e }
	bad := map[string]Entry{
		"no kind":            change(func(e *Entry) { e.Kind = "" }),
		"unknown kind":       change(func(e *Entry) { e.Kind = "chat" }),
		"unknown outcome":    change(func(e *Entry) { e.Outcome = "fine" }),
		"no request id":      change(func(e *Entry) { e.RequestID = " " }),
		"no actor":           change(func(e *Entry) { e.Actor = "" }),
		"no action":          change(func(e *Entry) { e.Action = "" }),
		"a status of 42":     change(func(e *Entry) { e.Status = 42 }),
		"a negative cost":    change(func(e *Entry) { e.CostMicros = -1 }),
		"a negative time":    change(func(e *Entry) { e.DurationMS = -1 }),
		"negative tokens":    change(func(e *Entry) { e.Usage.InputTokens = -5 }),
		"too much thinking":  change(func(e *Entry) { e.Usage = Usage{OutputTokens: 1, ThinkingTokens: 2} }),
		"negative thinking":  change(func(e *Entry) { e.Usage.ThinkingTokens = -1 }),
		"negative cache use": change(func(e *Entry) { e.Usage.CacheReadTokens = -1 }),
	}
	for name, e := range bad {
		if _, err := Append(context.Background(), a, e); err == nil {
			t.Errorf("%s: was written", name)
		}
	}
	if got := mustList(t, a, Filter{}); len(got) != 0 {
		t.Errorf("a refused entry is in the log: %+v", got)
	}
}

// madeUpKeys returns strings with the shapes of real keys. None is real, and
// each is put together here so that no file in the repo holds the shape.
func madeUpKeys() map[string]string {
	fill := strings.Repeat("x9", 20)
	return map[string]string{
		"anthropic":   "sk-" + "ant-" + "api03-" + fill,
		"openai":      "sk-" + "proj-" + fill,
		"openrouter":  "sk-" + "or-v1-" + fill,
		"plain sk":    "sk-" + fill,
		"aws":         "AKIA" + strings.Repeat("Q", 16),
		"github":      "gh" + "p_" + fill,
		"github pat":  "github_" + "pat_" + fill,
		"tavily":      "tvly-" + fill,
		"google":      "AIza" + fill,
		"chat app":    "xox" + "b-" + fill,
		"web token":   "eyJ" + fill + "." + fill + "." + fill,
		"private key": "-----BEGIN " + "PRIVATE KEY-----\n" + strings.Repeat("Zk", 30) + "\n" + strings.Repeat("Qm", 30) + "\n-----END " + "PRIVATE KEY-----",
	}
}

// needles returns what must not be found for one made-up key: the key, and
// for a private key each line of its body.
func needles(key string) []string {
	out := []string{key}
	if strings.Contains(key, "PRIVATE KEY") {
		out = append(out, strings.Repeat("Zk", 30), strings.Repeat("Qm", 30))
	}
	return out
}

func TestNoKeyIsEverWritten(t *testing.T) {
	_, a, _ := twoStores(t)
	check := func(what, text string, key string) {
		t.Helper()
		for _, n := range needles(key) {
			if strings.Contains(text, n) {
				t.Errorf("%s: a key reached the log: %q", what, text)
			}
		}
	}
	asJSON := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	for name, key := range madeUpKeys() {
		// By shape alone, in every text field, as plain text and inside JSON.
		got := mustAppend(t, a, Entry{
			Kind: KindTool, RequestID: "req-" + name, Actor: "person " + key, Action: "fetch " + key, Outcome: OutcomeError,
			Sent:     asJSON(map[string]any{"url": "https://example.invalid/?key=" + key, "headers": map[string]string{"X-Note": key}}),
			Received: "the service said: bad key " + key + "\nand nothing else",
		})
		for _, field := range []string{got.Actor, got.Action, got.Sent, got.Received} {
			check(name, field, key)
		}
		if !json.Valid([]byte(got.Sent)) {
			t.Errorf("%s: what was sent is no longer JSON: %s", name, got.Sent)
		}
		if !strings.Contains(got.Received, "the service said: bad key") || !strings.Contains(got.Received, "and nothing else") {
			t.Errorf("%s: more than the key was removed: %q", name, got.Received)
		}
	}

	// A token with no shape of its own is removed when the caller names it,
	// as plain text and as JSON writes it.
	shapeless := `gateway "pass" <42> \ for Test Person One`
	got := mustAppend(t, a, Entry{
		Kind: KindModel, RequestID: "req-named", Actor: "person", Action: "local-gateway", Outcome: OutcomeError,
		Sent:     asJSON(map[string]string{"text": "hello", "echo": shapeless}),
		Received: "the gateway refused " + shapeless,
	}, shapeless, "")
	for _, field := range []string{got.Sent, got.Received} {
		if strings.Contains(field, "pass") || strings.Contains(field, "42") {
			t.Errorf("a named token reached the log: %q", field)
		}
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(got.Sent), &sent); err != nil || sent["text"] != "hello" || sent["echo"] != removed {
		t.Errorf("what was sent did not keep its shape: %s (%v)", got.Sent, err)
	}

	// Fields and headers that carry a credential, whatever the value looks like.
	got = mustAppend(t, a, Entry{
		Kind: KindTool, RequestID: "req-fields", Actor: "person", Action: "call_service", Outcome: OutcomeOK,
		Sent:     `{"api_key": "plain-words-one", "Password":"plain-words-two", "session_token" : "plain-words-three", "query": "kept", "max_tokens": 50}`,
		Received: "Authorization: Basic plain-words-four\r\nCookie: session=plain-words-five\r\nContent-Type: text/plain\r\n\r\nBearer plain-words-six-and-more",
	})
	for _, n := range []string{"plain-words-one", "plain-words-two", "plain-words-three", "plain-words-four", "plain-words-five", "plain-words-six"} {
		if strings.Contains(got.Sent+got.Received, n) {
			t.Errorf("%s reached the log: %q %q", n, got.Sent, got.Received)
		}
	}
	for _, kept := range []string{`"query": "kept"`, `"max_tokens": 50`} {
		if !strings.Contains(got.Sent, kept) {
			t.Errorf("%s was lost from %s", kept, got.Sent)
		}
	}
	if !strings.Contains(got.Received, "Content-Type: text/plain") {
		t.Errorf("an ordinary header was lost: %q", got.Received)
	}

	// The database itself, read without this package.
	raw := rawLog(t, a)
	for name, key := range madeUpKeys() {
		check("the stored rows, "+name, raw, key)
	}
	if strings.Contains(raw, "plain-words") || strings.Contains(raw, `gateway "pass"`) || strings.Contains(raw, `gateway \"pass\"`) {
		t.Errorf("a credential is in the stored rows")
	}
}

func TestScrubLeavesOrdinaryTextAlone(t *testing.T) {
	for _, text := range []string{
		"",
		"A basic question about tokens, keys and passwords in general.",
		`{"input_tokens": 120, "output_tokens": 30, "stop_reason": "end"}`,
		`{"messages":[{"role":"user","blocks":[{"type":"text","text":"Where is the spare key kept?"}]}]}`,
		"GET /api/chats/3f2a9c",
		"sk-short and AKIA-not-a-key",
	} {
		if got := Scrub(text); got != text {
			t.Errorf("Scrub changed ordinary text:\n got %q\nwant %q", got, text)
		}
	}
}
