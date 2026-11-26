package importer_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/imported"
	"github.com/jonathanmkosar/keystone/internal/importer"
	"github.com/jonathanmkosar/keystone/internal/importer/importertest"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/recap"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// Everything in these tests is invented, and lives in throwaway folders.

var now = time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)

type rig struct {
	t       *testing.T
	install *tenant.Install
	one     *tenant.Store
	two     *tenant.Store
	dir     string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	in, err := tenant.OpenWithKeeper(filepath.Join(dir, "data"), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	g := &rig{t: t, install: in, dir: dir}
	for _, into := range []**tenant.Store{&g.one, &g.two} {
		made, err := in.Create("Invented Person")
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		*into = s
	}
	return g
}

// source builds a database of the fixtures and opens it as a source.
func (g *rig) source(name string, fixtures ...importertest.Fixture) *importer.Source {
	g.t.Helper()
	file := filepath.Join(g.dir, name)
	if err := importertest.Build(file, fixtures...); err != nil {
		g.t.Fatal(err)
	}
	src, err := importer.OpenSource(file)
	if err != nil {
		g.t.Fatal(err)
	}
	g.t.Cleanup(func() { src.Close() })
	return src
}

func (g *rig) set(s *tenant.Store, key, value string) {
	g.t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		g.t.Fatal(err)
	}
	if err := tenantsettings.Set(context.Background(), s, reg, tenantsettings.Admin, key, value); err != nil {
		g.t.Fatal(err)
	}
}

func count(t *testing.T, s *tenant.Store, table string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func said(m chat.Message) string { return seam.Text(seam.Message{Role: m.Role, Blocks: m.Blocks}) }

// chats returns the tenant's chats, oldest first by their own times.
func chats(t *testing.T, s *tenant.Store) []chat.Chat {
	t.Helper()
	list, err := chat.List(s, chat.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list
}

// The item's proof, on the real server: see importertest.Proof.
func TestTheProof(t *testing.T) {
	problems, err := importertest.Proof(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// The standard fixture becomes five chats and three paused schedules, each
// as its source had it, and everything else is counted and left.
func TestAWorkspaceBecomesChatsAndPausedSchedules(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	src := g.source("workspace.sqlite", importertest.Standard())
	fx := importertest.Standard()

	report, err := importer.Run(ctx, g.one, src, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Chats != 5 || report.ChatsByKind[imported.ChatStretch] != 3 || report.ChatsByKind[imported.ChatRoutine] != 1 || report.ChatsByKind[imported.ChatBuild] != 1 {
		t.Fatalf("the import made %d chats: %v", report.Chats, report.ChatsByKind)
	}
	if report.Turns != 7 || report.Messages != 12 || report.FromPerson != 7 || report.Replies != 5 || report.Unanswered != 2 || report.Held != 0 {
		t.Errorf("the report counts %+v", report)
	}
	if report.Models[importertest.LocalModel] != 2 || report.Models[importertest.PickedModel] != 1 || report.Models[""] != 2 {
		t.Errorf("replies by model: %v", report.Models)
	}
	left := map[string]int{}
	for _, l := range report.Left {
		left[l.What] = l.Count
	}
	for _, reason := range []string{importer.LeftInFlight, importer.LeftManual, importer.LeftProposed, importer.LeftDeleted, importer.LeftPast} {
		if left[reason] != 1 {
			t.Errorf("left out for %q: %d, want 1", reason, left[reason])
		}
	}
	if report.Memories["active"] != 2 || report.Memories["waymarks"] != 3 || report.Memories["forgotten"] != 1 {
		t.Errorf("notebook entries by state: %v", report.Memories)
	}
	tables := map[string]int{}
	for _, l := range report.Tables {
		name, _, _ := strings.Cut(l.What, ":")
		tables[name] = l.Count
	}
	for name, want := range map[string]int{"memories": 6, "decisions": 1, "calendar_drafts": 2, "email_drafts": 1, "proposals": 1, "artifacts": 1, "routine_runs": 1, "chat_attachments": 1} {
		if tables[name] != want {
			t.Errorf("the report counts %d rows of %s, want %d", tables[name], name, want)
		}
	}

	list := chats(t, g.one)
	if len(list) != 5 {
		t.Fatalf("the tenant has %d chats", len(list))
	}
	// Oldest first: beans, pintle, the brief's run, the build, the two
	// turns that got no reply.
	beans, pintle, run, build, unanswered := list[0], list[1], list[2], list[3], list[4]
	if beans.Title != "Which quillfeather beans should go in the north bed this…" {
		t.Errorf("the bean chat is titled %q", beans.Title)
	}
	if !beans.Created.Equal(fx.Turns[0].At) || !beans.Active.Equal(fx.Turns[1].At.Add(fx.Turns[1].Took)) {
		t.Errorf("the bean chat's times are %s and %s", beans.Created, beans.Active)
	}
	path, err := chat.Path(g.one, beans.ID)
	if err != nil || len(path) != 4 {
		t.Fatalf("the bean chat's branch has %d messages: %v", len(path), err)
	}
	for i, want := range []struct {
		role seam.Role
		at   time.Time
	}{
		{seam.RoleUser, fx.Turns[0].At}, {seam.RoleAssistant, fx.Turns[0].At.Add(4 * time.Second)},
		{seam.RoleUser, fx.Turns[1].At}, {seam.RoleAssistant, fx.Turns[1].At.Add(3 * time.Second)},
	} {
		if path[i].Role != want.role || !path[i].Created.Equal(want.at) {
			t.Errorf("message %d is %s at %s, want %s at %s", i+1, path[i].Role, path[i].Created, want.role, want.at)
		}
	}
	if got := said(path[1]); got != importertest.BeanReply+"\n\nSources:\n\n- [1] [Sowing beans by a north fence](https://gardening.example/beans-north-fence)" {
		t.Errorf("the first reply reads %q", got)
	}
	if len(path[2].Blocks) != 2 || path[2].Blocks[1].Text != "Attached file: "+importertest.BeanFileName+"\n\n"+importertest.BeanFileText {
		t.Errorf("the second message's blocks are %+v", path[2].Blocks)
	}
	models, err := imported.Models(ctx, g.one, beans.ID)
	if err != nil || len(models) != 2 || models[path[1].ID] != importertest.LocalModel || models[path[3].ID] != importertest.LocalModel {
		t.Errorf("the bean chat's replies name %v: %v", models, err)
	}
	marks, err := imported.Marks(ctx, g.one, []string{beans.ID, pintle.ID, run.ID, build.ID, unanswered.ID, "no such chat"})
	if err != nil || len(marks) != 5 {
		t.Fatalf("%d marks: %v", len(marks), err)
	}
	if m := marks[beans.ID]; m.From != "New Keystone" || m.Kind != imported.ChatStretch || m.Turns != 2 || m.Unanswered != 0 || !m.At.Equal(now) ||
		!m.First.Equal(fx.Turns[0].At) || m.Steps["search.completed"] != 1 || m.Steps["chat.attachment_saved"] != 1 || len(m.Steps) != 2 ||
		m.Note != "New Keystone recorded 1 web search and 1 attached file here. It kept no record of what each one asked or found, so they are counted and not shown as steps." {
		t.Errorf("the bean chat's mark is %+v", m)
	}
	if m := marks[pintle.ID]; len(m.Models) != 1 || m.Models[0] != importertest.PickedModel || m.Steps["wiki.lookup"] != 1 || !strings.Contains(m.Note, "1 wiki lookup and 1 note saved to the wiki") {
		t.Errorf("the pintle chat's mark is %+v", m)
	}
	if run.Title != importertest.BriefName+", Wed 11 Nov 2026" || marks[run.ID].Kind != imported.ChatRoutine {
		t.Errorf("the brief's run is titled %q, kind %s", run.Title, marks[run.ID].Kind)
	}
	if m := marks[build.ID]; m.Kind != imported.ChatBuild || !strings.Contains(m.Note, "This build made an app (3 files, published in New Keystone): "+importertest.BuildSummary+".") || !strings.Contains(m.Note, "1 coding model call") || !strings.Contains(m.Note, "2 other events") {
		t.Errorf("the build's mark is %+v", m)
	}
	if m := marks[unanswered.ID]; m.Turns != 2 || m.Unanswered != 2 || len(m.Models) != 0 || m.Note != "" {
		t.Errorf("the unanswered turns' mark is %+v", m)
	}
	if path, _ := chat.Path(g.one, unanswered.ID); len(path) != 2 || path[0].Role != seam.RoleUser || path[1].Role != seam.RoleUser || said(path[1]) != importertest.CancelledAsk {
		t.Errorf("the unanswered turns' chat is %+v", path)
	}
	// The turn still queued is nowhere.
	lim, _ := chat.DefaultLimits()
	if hits, err := chat.Search(g.one, lim, "water butt"); err != nil || len(hits) != 0 {
		t.Errorf("the queued turn was brought: %v %v", hits, err)
	}
	if hits, err := chat.Search(g.one, lim, importertest.PintleWord); err != nil || len(hits) == 0 || hits[0].ChatID != pintle.ID {
		t.Errorf("the chat search for the pintle chat: %v %v", hits, err)
	}
	// No chat is held, and none has a turn.
	for _, c := range list {
		if held, err := recall.Held(ctx, g.one, c.ID); err != nil || held {
			t.Errorf("chat %s is held (%v)", c.ID, err)
		}
	}
	if n := count(t, g.one, "turns"); n != 0 {
		t.Errorf("the import wrote %d turns", n)
	}

	// The routines.
	sched := schedules.New(schedules.Options{Limits: lim, Now: func() time.Time { return now }})
	got, err := sched.List(ctx, g.one)
	if err != nil || len(got.Schedules) != 3 {
		t.Fatalf("%d schedules: %v", len(got.Schedules), err)
	}
	if got.Zone.Name != "America/Chicago" || report.ZoneSet != "America/Chicago" {
		t.Errorf("the person's zone is %q, and the report says %q was set", got.Zone.Name, report.ZoneSet)
	}
	says := map[string]string{}
	for _, sc := range got.Schedules {
		if sc.Enabled || sc.Next != nil {
			t.Errorf("the schedule %q is switched on", sc.Name)
		}
		says[sc.Name] = sc.Says + " | " + sc.Prompt
	}
	for name, want := range map[string]string{
		importertest.BriefName:  "Every day at 07:00 | " + importertest.BriefPrompt,
		importertest.WeeklyName: "Sundays at 18:30 | " + importertest.WeeklyPrompt,
		"Hourly rain gauge":     "Every hour, on the hour | Read the rain gauge.",
	} {
		if says[name] != want {
			t.Errorf("the schedule %q says %q, want %q", name, says[name], want)
		}
	}

	// In the audit log, and nowhere a model was called.
	entries, err := audit.List(ctx, g.one, audit.Filter{})
	if err != nil || len(entries) != 2 || entries[0].Outcome != audit.OutcomeStarted || entries[1].Outcome != audit.OutcomeOK || entries[1].RequestID != "import-"+report.ImportID {
		t.Fatalf("the audit log holds %+v: %v", entries, err)
	}
	for _, e := range entries {
		if e.Kind != audit.KindTool || e.Action != importer.Action || e.Actor != importer.Actor || strings.Contains(e.Sent+e.Received, importertest.BeanWord) {
			t.Errorf("the audit entry is %+v", e)
		}
	}

	// The other tenant has nothing of it.
	for _, table := range []string{"chats", "messages", "schedules", "audit_log", "imports", "import_sources", "imported_chats", "imported_messages", "tenant_settings"} {
		if n := count(t, g.two, table); n != 0 {
			t.Errorf("the other tenant's %s holds %d rows", table, n)
		}
	}

	// Twice is once, and a chat the person deleted stays deleted.
	if err := chat.Delete(g.one, pintle.ID); err != nil {
		t.Fatal(err)
	}
	again, err := importer.Run(ctx, g.one, src, "", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if again.Chats != 0 || again.Schedules != 0 || again.AlreadyTurns != 7 || again.AlreadyRoutines != 3 || again.ImportID != "" {
		t.Errorf("a second run reports %+v", again)
	}
	if n := len(chats(t, g.one)); n != 4 {
		t.Errorf("after a second run the tenant has %d chats, want 4", n)
	}
	if count(t, g.one, "imports") != 1 || count(t, g.one, "schedules") != 3 || count(t, g.one, "import_sources") != 10 || count(t, g.one, "imported_chats") != 4 {
		t.Errorf("after a second run: %d imports, %d schedules, %d source ids, %d marks", count(t, g.one, "imports"), count(t, g.one, "schedules"), count(t, g.one, "import_sources"), count(t, g.one, "imported_chats"))
	}
	if n := count(t, g.one, "audit_log"); n != 4 {
		t.Errorf("two runs left %d audit entries, want 4", n)
	}
}

// The dry run writes nothing, counts what a run then brings, and prints no
// word of the source.
func TestTheDryRunWritesNothingAndPrintsNoWords(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	src := g.source("workspace.sqlite", importertest.Standard())
	would, err := importer.Check(ctx, g.one, src, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if !would.Check || would.Chats != 5 || would.Messages != 12 || would.Schedules != 3 || !would.Brief || would.ZoneSet != "America/Chicago" || would.ImportID != "" {
		t.Errorf("the dry run reports %+v", would)
	}
	for _, table := range []string{"chats", "messages", "schedules", "audit_log", "imports", "import_sources", "tenant_settings", "turns"} {
		if n := count(t, g.one, table); n != 0 {
			t.Errorf("the dry run wrote %d rows of %s", n, table)
		}
	}
	var out bytes.Buffer
	would.Write(&out)
	did, err := importer.Run(ctx, g.one, src, "", now)
	if err != nil {
		t.Fatal(err)
	}
	did.Write(&out)
	fx := importertest.Standard()
	words := []string{importertest.Workspace, importertest.BuildSummary, "greenhouse", "Tide check", "rain gauge"}
	for _, turn := range fx.Turns {
		words = append(words, turn.Prompt)
		if turn.Reply != "" {
			words = append(words, turn.Reply)
		}
	}
	for _, r := range fx.Routines {
		words = append(words, r.Name)
	}
	for _, c := range chats(t, g.one) {
		words = append(words, c.Title)
	}
	for _, word := range words {
		if strings.Contains(out.String(), word) {
			t.Errorf("the reports print %q", word)
		}
	}
	for _, want := range []string{"Would import:", "Imported:", "5", "the morning brief among them", importertest.LocalModel, "(none named)", "notebook entries", "active 2", "Nothing was written", "No model was called"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the reports do not say %q:\n%s", want, out.String())
		}
	}
}

// A later copy of the same database brings only what is new, in chats of
// its own.
func TestALaterCopyBringsOnlyWhatIsNew(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	first := importertest.Standard()
	if _, err := importer.Run(ctx, g.one, g.source("first.sqlite", first), "", now); err != nil {
		t.Fatal(err)
	}
	later := importertest.Standard()
	// The queued turn has ended, and one more was sent the next day.
	later.Turns[7].Status, later.Turns[7].Reply, later.Turns[7].Took = "", "The water butt is full.", 3*time.Second
	later.Turns = append(later.Turns, importertest.Turn{Prompt: "When should the leeks be lifted?", Reply: "Lift the leeks after the first hard frost.",
		At: later.Turns[7].At.Add(26 * time.Hour), Took: 2 * time.Second})
	report, err := importer.Run(ctx, g.one, g.source("later.sqlite", later), "", now.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if report.Chats != 2 || report.Turns != 2 || report.Messages != 4 || report.Schedules != 0 || report.AlreadyTurns != 7 || report.AlreadyRoutines != 3 {
		t.Errorf("the later copy brought %+v", report)
	}
	if n := len(chats(t, g.one)); n != 7 {
		t.Errorf("the tenant has %d chats, want 7", n)
	}
	if count(t, g.one, "imports") != 2 || count(t, g.one, "schedules") != 3 {
		t.Errorf("%d import runs and %d schedules", count(t, g.one, "imports"), count(t, g.one, "schedules"))
	}
}

// chats.import_hold holds the chats New Keystone read the wiki in, or all
// of them, in the transaction that writes each.
func TestImportedChatsCanBeHeld(t *testing.T) {
	ctx := context.Background()
	for hold, want := range map[string]int{importer.HoldWiki: 1, importer.HoldAll: 5} {
		g := newRig(t)
		g.set(g.one, importer.KeyHold, hold)
		report, err := importer.Run(ctx, g.one, g.source("workspace.sqlite", importertest.Standard()), "", now)
		if err != nil {
			t.Fatal(err)
		}
		held := 0
		for _, c := range chats(t, g.one) {
			is, err := recall.Held(ctx, g.one, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !is {
				continue
			}
			held++
			if hold == importer.HoldWiki && !strings.Contains(c.Title, importertest.PintleWord) {
				t.Errorf("held for the wiki: %q", c.Title)
			}
			if sensitive, err := recap.Sensitive(ctx, g.one, c.ID); err != nil || !sensitive || recap.MayShow(sensitive, false) {
				t.Errorf("a held imported chat may be shown to a hosted model (%v)", err)
			}
		}
		if held != want || report.Held != want || count(t, g.one, "turns") != want {
			t.Errorf("%s: %d chats are held, the report says %d, with %d turns; want %d", hold, held, report.Held, count(t, g.one, "turns"), want)
		}
		var actor, status, model string
		if err := g.one.DB().QueryRow("SELECT actor, status, model FROM turns LIMIT 1").Scan(&actor, &status, &model); err != nil || actor != importer.HoldActor || status != "done" || model != "" {
			t.Errorf("the turn that carries a hold is %s, %s, %q: %v", actor, status, model, err)
		}
	}
}

// The gap decides where the continuing chat is cut, and a bad setting is
// refused by name.
func TestTheGapCutsTheContinuingChat(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	src := g.source("workspace.sqlite", importertest.Standard())
	g.set(g.one, importer.KeyGap, "5m")
	would, err := importer.Check(ctx, g.one, src, "", now)
	if err != nil || would.Chats != 6 || would.ChatsByKind[imported.ChatStretch] != 4 {
		t.Errorf("with a gap of 5m: %d chats, %v: %v", would.Chats, would.ChatsByKind, err)
	}
	g.set(g.one, importer.KeyGap, "48h")
	would, err = importer.Check(ctx, g.one, src, "", now)
	if err != nil || would.Chats != 3 || would.ChatsByKind[imported.ChatStretch] != 1 {
		t.Errorf("with a gap of 48h: %d chats, %v: %v", would.Chats, would.ChatsByKind, err)
	}
	for key, bad := range map[string]string{importer.KeyGap: "10s", importer.KeyTitleChars: "3", importer.KeyZone: "Mars/Olympus"} {
		h := newRig(t)
		h.set(h.one, key, bad)
		if _, err := importer.Check(ctx, h.one, h.source("workspace.sqlite", importertest.Standard()), "", now); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s = %s was taken: %v", key, bad, err)
		}
	}
}

// A person with a zone of their own keeps it, and is told the two differ.
func TestAPersonsOwnZoneIsKept(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	reg, _ := settings.Registry()
	if err := tenantsettings.Set(ctx, g.one, reg, tenantsettings.Person, schedules.KeyTimeZone, "Europe/Lisbon"); err != nil {
		t.Fatal(err)
	}
	report, err := importer.Run(ctx, g.one, g.source("workspace.sqlite", importertest.Standard()), "", now)
	if err != nil {
		t.Fatal(err)
	}
	if report.ZoneSet != "" || report.ZoneKept != "Europe/Lisbon" || report.ZoneWas != "America/Chicago" {
		t.Errorf("the report says %q set, %q kept, %q was", report.ZoneSet, report.ZoneKept, report.ZoneWas)
	}
	if v, _ := tenantsettings.Get(ctx, g.one, reg, schedules.KeyTimeZone); v != "Europe/Lisbon" {
		t.Errorf("the person's zone is now %q", v)
	}
}

// A file that is not a whole copy of a New Keystone database is refused,
// and a source with several workspaces needs one named.
func TestWhatIsNotAWholeCopyIsRefused(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	refused := func(name string, err error, words string) {
		t.Helper()
		if !errors.Is(err, importer.ErrSource) || !strings.Contains(err.Error(), words) {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, err := importer.OpenSource(filepath.Join(g.dir, "nothing.sqlite"))
	refused("a file that is not there", err, "nothing.sqlite")

	text := filepath.Join(g.dir, "notes.sqlite")
	os.WriteFile(text, []byte("These are invented notes, not a database."), 0o600)
	_, err = importer.OpenSource(text)
	refused("a text file", err, "does not open as a SQLite database")

	other := filepath.Join(g.dir, "other.sqlite")
	db, err := importertest.Open(other, "CREATE TABLE jobs(id TEXT); CREATE TABLE notes(body TEXT);")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, err = importer.OpenSource(other)
	refused("another app's database", err, "is not a New Keystone database")

	// The live database: its newest turns are in the log beside it.
	live := filepath.Join(g.dir, "live.sqlite")
	db, err = importertest.Open(live, importertest.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := importertest.Write(db, importertest.Standard()); err != nil {
		t.Fatal(err)
	}
	_, err = importer.OpenSource(live)
	refused("a database in use", err, "import-copy")

	// Copy makes a whole copy of it while it is in use, and the copy holds
	// what the log held.
	whole := filepath.Join(g.dir, "copy.sqlite")
	if err := importer.Copy(live, whole); err != nil {
		t.Fatal(err)
	}
	src, err := importer.OpenSource(whole)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if would, err := importer.Check(ctx, g.one, src, "", now); err != nil || would.SourceTurns != 8 || would.Chats != 5 {
		t.Errorf("the copy holds %d turns and would make %d chats: %v", would.SourceTurns, would.Chats, err)
	}
	if _, err := os.Stat(whole + "-wal"); err == nil {
		t.Error("reading the copy left a log beside it")
	}
	if err := importer.Copy(live, whole); err == nil || !strings.Contains(err.Error(), "already there") {
		t.Errorf("a copy replaced a file: %v", err)
	}
	// A database at rest is copied without a file being made beside it.
	rest := filepath.Join(g.dir, "rest", "workspace.sqlite")
	os.MkdirAll(filepath.Dir(rest), 0o700)
	if err := importertest.Build(rest, importertest.Standard()); err != nil {
		t.Fatal(err)
	}
	if err := importer.Copy(rest, filepath.Join(g.dir, "rest-copy.sqlite")); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(filepath.Dir(rest)); len(left) != 1 {
		t.Errorf("copying a database at rest left %d files in its folder, want the one", len(left))
	}

	// Two workspaces: one must be named, and only it is brought.
	second := importertest.Fixture{Workspace: "Marlow", Turns: []importertest.Turn{{ID: strings.Repeat("d", 32), Prompt: "Where is the trowel?", Reply: "In the potting shed.", At: now.Add(-time.Hour), Took: time.Second}}}
	both := g.source("both.sqlite", importertest.Standard(), second)
	if _, err := importer.Check(ctx, g.one, both, "", now); !errors.Is(err, importer.ErrWorkspace) || !strings.Contains(err.Error(), "--workspace") {
		t.Errorf("two workspaces and none named: %v", err)
	}
	if _, err := importer.Check(ctx, g.one, both, "Nobody", now); !errors.Is(err, importer.ErrWorkspace) {
		t.Errorf("a workspace that is not there: %v", err)
	}
	report, err := importer.Run(ctx, g.two, both, "Marlow", now)
	if err != nil || report.Chats != 1 || report.Messages != 2 || report.Schedules != 0 {
		t.Errorf("the second workspace brought %+v: %v", report, err)
	}
	if list := chats(t, g.two); len(list) != 1 || list[0].Title != "Where is the trowel?" {
		t.Errorf("the second workspace's chats: %+v", list)
	}
}

// A database only New Keystone's first build opened has no kinds, models,
// sources, events or routines, and is read all the same.
func TestAFirstBuildDatabaseIsRead(t *testing.T) {
	g := newRig(t)
	file := filepath.Join(g.dir, "first-build.sqlite")
	db, err := importertest.Open(file, importertest.FirstBuildSchema)
	if err != nil {
		t.Fatal(err)
	}
	old := importertest.Fixture{Workspace: importertest.Workspace, Turns: importertest.Standard().Turns[:2]}
	if err := importertest.Write(db, old); err != nil {
		t.Fatal(err)
	}
	db.Close()
	src, err := importer.OpenSource(file)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	report, err := importer.Run(context.Background(), g.one, src, "", now)
	if err != nil || report.Chats != 1 || report.Messages != 4 || report.Schedules != 0 || report.Models[""] != 2 {
		t.Errorf("a first-build database brought %+v: %v", report, err)
	}
}

// What a plan makes of odd turns and routines.
func TestThePlan(t *testing.T) {
	zone, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	o := importer.Options{Gap: time.Hour, TitleChars: 20, TitleLength: 150, Zone: zone, Hold: importer.HoldNone, PromptMaxChars: 8000, Now: now}
	at := time.Date(2026, 11, 12, 3, 30, 0, 0, time.UTC) // 21:30 on the 11th in Chicago
	h, wd := 9, 2
	d := &importer.Data{
		Turns: []importer.Turn{
			{ID: "a", Status: "succeeded", Prompt: "\n\n  Supercalifragilistic   words that go on and on\nsecond line", Reply: "From the job's own copy.", Created: at, Updated: at.Add(time.Minute),
				Events: map[string]int{"job.queued": 1, "model.usage": 1, "search.completed": 2, "something.new": 1}},
			{ID: "b", Status: "waiting_decision", Prompt: "Still waiting on a decision.", Created: at.Add(2 * time.Minute), Updated: at.Add(2 * time.Minute)},
			{ID: "c", Status: "succeeded", Prompt: "   ", Created: at.Add(3 * time.Minute)},
			{ID: "d", Status: "succeeded", Prompt: "A run of a routine that is gone.", Reply: "Done.", Created: at, Updated: at, Routine: "gone",
				Sources: []importer.Link{{Number: "", Title: "A [bracketed] title", URL: "javascript:alert(1)"}, {Number: "7", URL: "https://tides.example/table"}}},
			{ID: "e", Status: "running", Prompt: "Under way.", Created: at},
			{ID: "seen", Status: "succeeded", Prompt: "Here already.", Reply: "Yes.", Created: at},
		},
		Routines: []importer.Routine{
			{ID: "r1", Name: "Wednesday  check", Prompt: "Check the nets.", Cadence: "weekly", Hour: &h, Minute: 5, Weekday: &wd, Status: "active"},
			{ID: "r2", Name: "No hour", Prompt: "x", Cadence: "daily", Status: "active"},
			{ID: "r3", Name: "Later", Prompt: "Once, later.", Cadence: "once", Hour: &h, RunOn: "2026-12-24", Status: "paused"},
			{ID: "r4", Name: "Odd", Prompt: "x", Cadence: "fortnightly", Hour: &h, Status: "active"},
			{ID: "r5", Name: "Here", Prompt: "x", Cadence: "daily", Hour: &h, Status: "active"},
		},
	}
	p := importer.MakePlan(d, map[string]string{"seen": "chat"}, map[string]string{"r5": "schedule"}, o)
	if len(p.Chats) != 2 || p.SeenTurns != 1 || p.SeenRoutines != 1 || p.Left[importer.LeftInFlight] != 1 || p.Left[importer.LeftEmpty] != 1 {
		t.Fatalf("the plan: %d chats, %+v", len(p.Chats), p)
	}
	stretch, run := p.Chats[0], p.Chats[1]
	if stretch.Title != "Supercalifragilisti…" || len(stretch.TurnIDs) != 2 || stretch.Unanswered != 1 || len(stretch.Messages) != 3 {
		t.Errorf("the stretch: %q, %+v", stretch.Title, stretch)
	}
	if stretch.Messages[1].Texts[0] != "From the job's own copy." || !stretch.Messages[1].At.Equal(at.Add(time.Minute)) {
		t.Errorf("a reply read from the job's own copy: %+v", stretch.Messages[1])
	}
	if stretch.Note != "New Keystone recorded 2 web searches and 1 other event here. It kept no record of what each one asked or found, so they are counted and not shown as steps." {
		t.Errorf("the stretch's note: %q", stretch.Note)
	}
	if run.Kind != imported.ChatRoutine || run.Title != "Routine run, Wed 11 Nov 2026" {
		t.Errorf("the run of a routine that is gone: %q", run.Title)
	}
	if got := run.Messages[1].Texts[0]; got != "Done.\n\nSources:\n\n- [1] A (bracketed) title\n- [7] <https://tides.example/table>" {
		t.Errorf("sources that are not links: %q", got)
	}
	if len(p.Schedules) != 2 || p.Left[importer.LeftCadence] != 2 {
		t.Fatalf("the plan's schedules: %+v, left %v", p.Schedules, p.Left)
	}
	if b := p.Schedules[0].Body; b.Name != "Wednesday check" || b.Recurrence.Kind != schedules.KindWeekly || b.Recurrence.Time != "09:05" || len(b.Recurrence.Days) != 1 || b.Recurrence.Days[0] != "wed" || b.Enabled == nil || *b.Enabled {
		t.Errorf("a weekly routine: %+v", b)
	}
	if b := p.Schedules[1].Body; b.Recurrence.Kind != schedules.KindOnce || b.Recurrence.Date != "2026-12-24" || p.Schedules[1].Brief {
		t.Errorf("a one-time routine still to come: %+v", b)
	}
	if importer.BriefID != importertest.BriefID || len(importer.BriefID) != 32 {
		t.Errorf("the brief's id is %q", importer.BriefID)
	}
}
