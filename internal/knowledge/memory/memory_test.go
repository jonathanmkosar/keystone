package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Every person, place and fact in these tests is invented.

func options() Options {
	return Options{
		Space: "memory", SensitiveSpace: "health",
		Prompt:     "Keep the long-term memory. Write at most {max} facts as {\"facts\": []}.",
		SourceName: "Facts from the chat \"{title}\", {date}",
		MaxFacts:   3, FactMaxChars: 60, MessageMaxChars: 200, MaxOutputTokens: 500,
	}
}

func exchange() Exchange {
	return Exchange{
		ChatID: "c1", TurnID: "t1", Title: "Bees", At: time.Date(2026, 11, 25, 9, 30, 0, 0, time.UTC),
		Person: "I keep two beehives at the allotment.", Reply: "Check their stores before winter.",
	}
}

func store(t *testing.T) *tenant.Store {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func answer(text string) seam.Response {
	return seam.Response{Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}}, StopReason: seam.StopEnd}
}

func TestOptionsAreChecked(t *testing.T) {
	if err := CheckOptions(options()); err != nil {
		t.Fatalf("good options were refused: %v", err)
	}
	for key, change := range map[string]func(*Options){
		SettingSpace:           func(o *Options) { o.Space = " " },
		SettingPrompt:          func(o *Options) { o.Prompt = "" },
		SettingSourceName:      func(o *Options) { o.SourceName = "" },
		SettingMaxFacts:        func(o *Options) { o.MaxFacts = 0 },
		SettingFactMaxChars:    func(o *Options) { o.FactMaxChars = 5 },
		SettingMessageMaxChars: func(o *Options) { o.MessageMaxChars = 100001 },
		SettingMaxOutputTokens: func(o *Options) { o.MaxOutputTokens = 10 },
	} {
		o := options()
		change(&o)
		if err := CheckOptions(o); !errors.Is(err, ErrOptions) || !strings.Contains(err.Error(), key) {
			t.Errorf("a bad %s gave %v", key, err)
		}
	}
	o := options()
	o.SensitiveSpace = ""
	if err := CheckOptions(o); err != nil {
		t.Errorf("an empty sensitive space is allowed and was refused: %v", err)
	}
	if routing.KindMemory != "memory" || Kind != "memory" {
		t.Errorf("the memory pass's kind is %q, and routing's %q", Kind, routing.KindMemory)
	}
}

// The request is the prompt and one message with the exchange in it, each
// side cut to its limit.
func TestTheRequestHoldsTheExchange(t *testing.T) {
	o := options()
	req := Request(o, exchange())
	if err := seam.ValidateRequest(req); err != nil {
		t.Fatalf("the request is not one a provider takes: %v", err)
	}
	if got := req.System[0].Text; !strings.Contains(got, "at most 3 facts") || strings.Contains(got, "{max}") {
		t.Errorf("the system text is %q", got)
	}
	msg := seam.Text(req.Messages[0])
	for _, want := range []string{`chat "Bees", on 2026-11-25`, "The person wrote:\nI keep two beehives", "The assistant answered:\nCheck their stores"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message does not hold %q:\n%s", want, msg)
		}
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != seam.RoleUser || req.MaxOutputTokens != 500 || len(req.Tools) != 0 {
		t.Errorf("the request is %+v", req)
	}

	x := exchange()
	x.Title, x.Reply = "", ""
	x.Person = strings.Repeat("hive ", 100)
	msg = seam.Text(Request(o, x).Messages[0])
	if !strings.Contains(msg, `chat "untitled"`) || !strings.Contains(msg, "[The rest is cut.]") || !strings.Contains(msg, "The assistant answered:\n(nothing)") {
		t.Errorf("a long message with no title and no reply reads:\n%s", msg)
	}
	if strings.Count(msg, "hive") > 41 {
		t.Errorf("the person's message was not cut to 200 characters:\n%s", msg)
	}
	if !(Exchange{Person: " ", Reply: "\n"}).Empty() || exchange().Empty() {
		t.Error("Empty does not tell an exchange with words from one with none")
	}
}

func TestTheAnswerIsRead(t *testing.T) {
	o := options()
	read := func(text string) (Read, error) { return Facts(answer(text), o) }

	got, err := read(`{"facts": ["The person keeps two beehives.", "The person   looks in on them on Sundays."]}`)
	if err != nil || len(got.Facts) != 2 || got.Facts[1].Text != "The person looks in on them on Sundays." || got.Dropped != 0 {
		t.Errorf("a plain answer read as %+v (%v)", got, err)
	}
	got, err = read("Here they are:\n```json\n{\"facts\": [{\"text\": \"The person keeps bees.\"}, \"The person has an allotment.\"]}\n```")
	if err != nil || len(got.Facts) != 2 || got.Facts[0].Text != "The person keeps bees." {
		t.Errorf("a fenced answer with an object read as %+v (%v)", got, err)
	}
	got, err = read(`{"facts": []}`)
	if err != nil || len(got.Facts) != 0 {
		t.Errorf("an empty list read as %+v (%v)", got, err)
	}
	got, err = read(`{"facts": ["", "The person keeps bees.", "the person keeps bees.", "` + strings.Repeat("long ", 20) + `", "One.", "Two.", "Three."]}`)
	if err != nil || len(got.Facts) != 3 || got.Dropped != 4 || got.Facts[2].Text != "Two." {
		t.Errorf("blank, repeated, long and extra facts read as %+v (%v)", got, err)
	}
	if texts := got.Texts(); len(texts) != 3 || texts[0] != "The person keeps bees." {
		t.Errorf("the texts are %q", texts)
	}
	for _, bad := range []string{
		"The person keeps bees.",
		`{"facts": "The person keeps bees."}`,
		`{"notes": ["The person keeps bees."]}`,
		`{"facts": [42]}`,
		`{"facts": [{"words": "x"}]}`,
		`{"facts": ["unfinished"`,
	} {
		if _, err := read(bad); !errors.Is(err, ErrAnswer) {
			t.Errorf("%q gave %v, want ErrAnswer", bad, err)
		}
	}
}

func TestOriginAndNamePointBackToTheChat(t *testing.T) {
	origin := Origin("c1", "t1")
	if origin != "keystone://chats/c1/turns/t1" {
		t.Errorf("the origin is %q", origin)
	}
	if c, tr, ok := ParseOrigin(origin); !ok || c != "c1" || tr != "t1" {
		t.Errorf("the origin reads back as %q, %q, %v", c, tr, ok)
	}
	for _, other := range []string{"a letter", "keystone://chats/c1", "keystone://chats//turns/t1", "keystone://chats/c1/turns/t1/x", "https://chats/c1/turns/t1"} {
		if _, _, ok := ParseOrigin(other); ok {
			t.Errorf("%q was read as the memory pass's origin", other)
		}
	}
	o := options()
	if got := SourceName(o, exchange()); got != `Facts from the chat "Bees", 2026-11-25` {
		t.Errorf("the source's name is %q", got)
	}
	x := exchange()
	x.Title = strings.Repeat("a long title ", 20)
	if got := SourceName(o, x); len([]rune(got)) > 160 || !strings.Contains(got, "...") {
		t.Errorf("a long title gives the name %q", got)
	}
}

// seed gives the store a garden page in an ordinary space and a sleep page
// in a sensitive one.
func seed(t *testing.T, s *tenant.Store) knowledge.Store {
	t.Helper()
	ctx := context.Background()
	for _, p := range []builtin.NewPage{
		{Space: "garden", Path: "plants/borage", Title: "Borage", Kind: "plant", Body: "Borage seeds itself along the path."},
		{Space: "health", Path: "notes/sleep", Title: "Sleep notes", Kind: "note", Body: "Sleep was poor in March."},
	} {
		if _, err := builtin.WritePage(ctx, s, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := builtin.SetLabel(ctx, s, "health", knowledge.LabelSensitive); err != nil {
		t.Fatal(err)
	}
	ks, err := knowledge.Open(ctx, builtin.Name, s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ks.Close() })
	return ks
}

// broken is a store whose catalog cannot be read.
type broken struct{ knowledge.Store }

func (broken) Catalog(context.Context) ([]knowledge.Entry, error) {
	return nil, knowledge.ErrUnavailable
}

// Fixed rule 3: facts from an exchange that may hold sensitive content go
// only to a space the store says is sensitive, from a model that may read
// it, or nowhere.
func TestFactsLandOnlyWhereTheyMay(t *testing.T) {
	s := store(t)
	ks := seed(t, s)
	ctx := context.Background()
	o := options()
	facts := []knowledge.Fact{{Text: "The person keeps two beehives."}, {Text: "The person checks them on Sundays."}}

	if space, err := Target(ctx, ks, false, o); err != nil || space != "memory" {
		t.Errorf("an ordinary exchange goes to %q (%v)", space, err)
	}
	if space, err := Target(ctx, ks, true, o); err != nil || space != "health" {
		t.Errorf("a sensitive exchange goes to %q (%v)", space, err)
	}
	for name, change := range map[string]func(*Options){
		"no sensitive space":            func(o *Options) { o.SensitiveSpace = "" },
		"an ordinary space":             func(o *Options) { o.SensitiveSpace = "garden" },
		"a space with no page in it":    func(o *Options) { o.SensitiveSpace = "letters" },
		"the space that facts go to":    func(o *Options) { o.SensitiveSpace = "memory" },
		"a name that differs in spaces": func(o *Options) { o.SensitiveSpace = " health2" },
	} {
		bad := options()
		change(&bad)
		if space, err := Target(ctx, ks, true, bad); !errors.Is(err, ErrNowhere) || space != "" {
			t.Errorf("with %s a sensitive exchange goes to %q (%v)", name, space, err)
		}
	}
	if _, err := Target(ctx, broken{ks}, true, o); err == nil || errors.Is(err, ErrNowhere) {
		t.Errorf("a catalog that cannot be read gave %v", err)
	}
	if _, err := Target(ctx, nil, true, o); !errors.Is(err, ErrNowhere) {
		t.Errorf("no store gave %v", err)
	}

	src, err := Add(ctx, ks, o, exchange(), facts, "memory", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if src.Space != "memory" || src.Label != knowledge.LabelNormal || src.Origin != Origin("c1", "t1") || src.Dated != "2026-11-25" ||
		!strings.Contains(src.Text, "- The person keeps two beehives.") || src.Name != `Facts from the chat "Bees", 2026-11-25` {
		t.Errorf("the source was added as %+v", src)
	}
	back, err := ks.ReadSource(ctx, src.ID)
	if err != nil || back.Origin != src.Origin || !strings.Contains(back.Text, "checks them on Sundays") {
		t.Errorf("the source reads back as %+v (%v)", back, err)
	}

	if _, err := Add(ctx, ks, o, exchange(), facts, "health", true, false); !errors.Is(err, ErrSensitive) {
		t.Errorf("sensitive facts from a model that may not read them gave %v", err)
	}
	hits, err := ks.Search(ctx, knowledge.Query{Text: "beehives", Space: "health"})
	if err != nil || len(hits) != 0 {
		t.Errorf("refused facts were added anyway: %+v (%v)", hits, err)
	}
	src, err = Add(ctx, ks, o, exchange(), facts, "health", true, true)
	if err != nil || src.Space != "health" || !src.Label.Sensitive() {
		t.Errorf("sensitive facts from a local model landed as %+v (%v)", src, err)
	}

	// The space was confirmed and then made ordinary before the facts went in.
	if err := builtin.SetLabel(ctx, s, "health", knowledge.LabelNormal); err != nil {
		t.Fatal(err)
	}
	if src, err := Add(ctx, ks, o, exchange(), facts, "health", true, true); !errors.Is(err, ErrLanded) || src.ID == "" {
		t.Errorf("facts that landed in a space made ordinary gave %+v, %v", src, err)
	}
	if _, err := Target(ctx, ks, true, o); !errors.Is(err, ErrNowhere) {
		t.Errorf("a space made ordinary is still a target for sensitive facts: %v", err)
	}

	none, err := knowledge.Open(ctx, knowledge.None, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(ctx, none, o, exchange(), facts, "memory", false, false); !errors.Is(err, knowledge.ErrNotSupported) {
		t.Errorf("a store with no AddSource gave %v", err)
	}
}

// newTurn writes a chat and a turn row in it, as the turn runner would.
func newTurn(t *testing.T, s *tenant.Store, chatID string) string {
	t.Helper()
	if chatID == "" {
		c, err := chat.Create(s)
		if err != nil {
			t.Fatal(err)
		}
		chatID = c.ID
	}
	id := chatID + "-" + time.Now().Format("150405.000000000")
	if _, err := s.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES(?, ?, 'done', 'req', 'person', '{}', '2026-11-25T09:00:00.000Z')", id, chatID); err != nil {
		t.Fatal(err)
	}
	return id
}

func chatOf(t *testing.T, s *tenant.Store, turnID string) string {
	t.Helper()
	var id string
	if err := s.DB().QueryRow("SELECT chat_id FROM turns WHERE id = ?", turnID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPassesAreQueuedTakenAndEnded(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	first := newTurn(t, s, "")
	chatID := chatOf(t, s, first)
	second := newTurn(t, s, chatID)

	for _, id := range []string{first, second} {
		if ok, err := Queue(ctx, s, id, chatID); err != nil || !ok {
			t.Fatalf("queueing %s: %v, %v", id, ok, err)
		}
	}
	if ok, err := Queue(ctx, s, first, chatID); err != nil || ok {
		t.Errorf("a turn was queued twice: %v, %v", ok, err)
	}
	if waiting, err := Waiting(ctx, s, chatID); err != nil || !waiting {
		t.Errorf("two queued passes do not wait: %v (%v)", waiting, err)
	}
	p, ok, err := Claim(ctx, s, chatID)
	if err != nil || !ok || p.TurnID != first || p.Status != StatusRunning {
		t.Fatalf("the first claim took %+v, %v (%v)", p, ok, err)
	}
	if err := Finish(ctx, s, Pass{TurnID: first, Status: StatusQueued}); err == nil {
		t.Error("a pass was ended as queued")
	}
	p.Status, p.Model, p.Space, p.SourceID, p.Facts = StatusDone, "house-small", "memory", "s7", []string{"The person keeps bees."}
	if err := Finish(ctx, s, p); err != nil {
		t.Fatal(err)
	}
	if err := Finish(ctx, s, p); err == nil {
		t.Error("a pass that had ended was ended again")
	}
	got, ok, err := Get(ctx, s, first)
	if err != nil || !ok || got.Status != StatusDone || got.Model != "house-small" || got.SourceID != "s7" || len(got.Facts) != 1 || got.Ended == nil {
		t.Errorf("the ended pass reads back as %+v (%v, %v)", got, ok, err)
	}

	// The second is taken and left running, as a stop would leave it.
	if p, ok, _ := Claim(ctx, s, chatID); !ok || p.TurnID != second {
		t.Fatalf("the second claim took %+v", p)
	}
	if _, ok, _ := Claim(ctx, s, chatID); ok {
		t.Error("a claim took a pass with none queued")
	}
	cut, err := Interrupt(ctx, s, chatID)
	if err != nil || len(cut) != 1 || cut[0] != second {
		t.Fatalf("interrupting gave %v (%v)", cut, err)
	}
	all, err := Passes(ctx, s, chatID)
	if err != nil || len(all) != 2 || all[0].TurnID != first || all[1].Status != StatusInterrupted || all[1].Reason != ReasonInterrupted || all[1].Facts == nil {
		t.Errorf("the chat's passes are %+v (%v)", all, err)
	}
	if waiting, _ := Waiting(ctx, s, chatID); waiting {
		t.Error("a chat with no queued pass is waiting")
	}
	if _, ok, err := Get(ctx, s, "no-such-turn"); ok || err != nil {
		t.Errorf("a turn with no pass gave %v (%v)", ok, err)
	}

	// The rows go with their chat.
	if err := chat.Delete(s, chatID); err != nil {
		t.Fatal(err)
	}
	if all, err := Passes(ctx, s, chatID); err != nil || len(all) != 0 {
		t.Errorf("a deleted chat still has passes: %+v (%v)", all, err)
	}
}

func TestAChatIsMadeIncognito(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	if on, err := Incognito(ctx, s, c.ID); err != nil || on {
		t.Errorf("a new chat is incognito: %v (%v)", on, err)
	}
	for _, want := range []bool{true, true, false, true} {
		if err := SetIncognito(ctx, s, c.ID, want); err != nil {
			t.Fatal(err)
		}
		if on, err := Incognito(ctx, s, c.ID); err != nil || on != want {
			t.Errorf("after setting %v the flag is %v (%v)", want, on, err)
		}
	}
	if err := SetIncognito(ctx, s, "no-such-chat", true); err == nil {
		t.Error("a chat that is not in the record was made incognito")
	}
	if err := chat.Delete(s, c.ID); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.DB().QueryRow("SELECT count(*) FROM memory_chats").Scan(&rows); err != nil || rows != 0 {
		t.Errorf("a deleted chat left %d flags (%v)", rows, err)
	}
}

// The reserve counts the memory pass's own spend on models that are not
// local, and stops the call that would pass it.
func TestTheReserveCountsTheMemoryPassAlone(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	now := time.Now()
	for _, r := range []usage.Record{
		{Kind: Kind, Model: "hosted-small", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: 300},
		{Kind: Kind, Model: "trusted-small", Class: models.ClassTrusted, Outcome: usage.OutcomeOK, CostMicros: 200},
		{Kind: Kind, Model: "house-small", Class: models.ClassLocal, Outcome: usage.OutcomeOK, CostMicros: 900},
		{Kind: "chat", Model: "hosted-small", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: 5000},
	} {
		r.At = now
		if _, err := usage.Add(ctx, s, r); err != nil {
			t.Fatal(err)
		}
	}
	reserve, err := NewReserve(0.001)
	if err != nil || reserve != 1000 {
		t.Fatalf("a reserve of a tenth of a cent is %d (%v)", reserve, err)
	}
	for _, x := range []struct {
		reserve, next int64
		want          usage.State
	}{
		{1000, 400, usage.StateOK},
		{1000, 500, usage.StateWarning},
		{1000, 501, usage.StateStopped},
		{0, 0, usage.StateStopped},
		{500, 0, usage.StateWarning},
	} {
		st, err := Allow(ctx, s, x.reserve, now, x.next)
		if err != nil || st.State != x.want || st.SpentMicros != 500 {
			t.Errorf("a reserve of %d and a call of %d gave %+v (%v), want %s", x.reserve, x.next, st, err, x.want)
		}
	}
	if st, _ := Allow(ctx, s, 1000, now.AddDate(0, 1, 0), 900); st.State == usage.StateStopped || st.SpentMicros != 0 {
		t.Errorf("next month starts with %+v", st)
	}
	for _, bad := range []float64{-1, 2e9} {
		if _, err := NewReserve(bad); err == nil {
			t.Errorf("a reserve of %v was taken", bad)
		}
	}
	if _, err := Allow(ctx, s, -1, now, 0); err == nil {
		t.Error("a negative reserve was taken")
	}
}
