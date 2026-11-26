package importer

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/imported"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// The import's settings, in settings/chats.json. Each is the admin's to
// set for one person: "tools/tenant setting set <tenant-id> <key> <value>".
const (
	KeyGap        = "chats.import_chat_gap"
	KeyTitleChars = "chats.import_title_max_chars"
	KeyZone       = "chats.import_time_zone"
	KeyHold       = "chats.import_hold"
)

// What the import is called in the audit log.
const (
	// Actor is who an import runs as: whoever has a shell on the machine.
	Actor = "admin"
	// Action names the import among the log's tool entries.
	Action = "import_new_keystone"
	// HoldActor is the actor of the turn that marks an imported chat as
	// held, which calls no model.
	HoldActor = "import"
)

// Settings are the values the import runs with.
type Settings struct {
	// Gap is chats.import_chat_gap.
	Gap time.Duration
	// TitleChars is chats.import_title_max_chars.
	TitleChars int
	// ZoneName is chats.import_time_zone, and Zone the zone it names.
	ZoneName string
	Zone     *time.Location
	// Hold is chats.import_hold.
	Hold string
}

// ReadSettings builds the settings from wherever their values are kept,
// and refuses a value no import can run with, naming the setting.
func ReadSettings(get func(key string) (string, error)) (Settings, error) {
	var s Settings
	gap, err := get(KeyGap)
	if err != nil {
		return Settings{}, err
	}
	if s.Gap, err = time.ParseDuration(strings.TrimSpace(gap)); err != nil || s.Gap < time.Minute || s.Gap > 720*time.Hour {
		return Settings{}, fmt.Errorf("%s must be a length of time from 1m to 720h", KeyGap)
	}
	chars, err := get(KeyTitleChars)
	if err != nil {
		return Settings{}, err
	}
	if s.TitleChars, err = strconv.Atoi(strings.TrimSpace(chars)); err != nil || s.TitleChars < 10 || s.TitleChars > 150 {
		return Settings{}, fmt.Errorf("%s must be from 10 to 150", KeyTitleChars)
	}
	zone, err := get(KeyZone)
	if err != nil {
		return Settings{}, err
	}
	s.ZoneName = strings.TrimSpace(zone)
	if s.ZoneName == "" {
		return Settings{}, fmt.Errorf("%s must name a time zone, such as Europe/Lisbon", KeyZone)
	}
	if s.Zone, err = time.LoadLocation(s.ZoneName); err != nil {
		return Settings{}, fmt.Errorf("%s: %q is not a time zone Keystone knows", KeyZone, s.ZoneName)
	}
	hold, err := get(KeyHold)
	if err != nil {
		return Settings{}, err
	}
	switch s.Hold = strings.TrimSpace(hold); s.Hold {
	case HoldNone, HoldWiki, HoldAll:
	default:
		return Settings{}, fmt.Errorf("%s must be none, wiki or all", KeyHold)
	}
	return s, nil
}

// SettingsFor reads the import's settings as they stand for one tenant.
func SettingsFor(ctx context.Context, s *tenant.Store) (Settings, error) {
	reg, err := settings.Registry()
	if err != nil {
		return Settings{}, err
	}
	return ReadSettings(func(key string) (string, error) { return tenantsettings.Get(ctx, s, reg, key) })
}

// Line is one line of a report: what, and how many.
type Line struct {
	What  string `json:"what"`
	Count int    `json:"count"`
}

// Report says what an import brought, or what a dry run would bring. It
// holds counts, model names, table names and dates, and never a title, a
// name or a word of a message.
type Report struct {
	// Check says nothing was written: this is a dry run's answer.
	Check bool `json:"check"`
	// ImportID names the run in the record and the audit log. Empty for a
	// dry run and for a run that had nothing to bring.
	ImportID string `json:"import_id,omitempty"`
	// FileSHA256 says which copy was read.
	FileSHA256 string `json:"file_sha256"`
	// SourceTurns and SourceRoutines are what the workspace holds.
	SourceTurns    int `json:"source_turns"`
	SourceRoutines int `json:"source_routines"`

	// Chats is how many chats are brought, and ChatsByKind how many of
	// each kind: chat, routine, build.
	Chats       int            `json:"chats"`
	ChatsByKind map[string]int `json:"chats_by_kind"`
	// Turns are the source's turns they hold; Messages their messages,
	// FromPerson the person's and Replies the replies; Unanswered the
	// turns with no reply.
	Turns      int `json:"turns"`
	Messages   int `json:"messages"`
	FromPerson int `json:"from_person"`
	Replies    int `json:"replies"`
	Unanswered int `json:"unanswered"`
	// Held is how many of the chats are held to local or trusted models.
	Held int `json:"held"`
	// Models counts the replies by the model New Keystone named; the empty
	// name counts those it named none for.
	Models map[string]int `json:"models"`
	// First and Last are the times of the oldest and newest message
	// brought.
	First *time.Time `json:"first,omitempty"`
	Last  *time.Time `json:"last,omitempty"`

	// Schedules is how many routines become schedules, all paused, and
	// Brief says New Keystone's morning brief is one of them.
	Schedules int  `json:"schedules"`
	Brief     bool `json:"brief"`
	// ZoneSet is the zone the person's agents.time_zone is set to, when it
	// was empty and a schedule is brought. ZoneKept is the person's own
	// zone when it differs from the one New Keystone's times were kept in,
	// ZoneWas: the times are brought as they read on the clock.
	ZoneSet  string `json:"zone_set,omitempty"`
	ZoneKept string `json:"zone_kept,omitempty"`
	ZoneWas  string `json:"zone_was,omitempty"`

	// AlreadyTurns and AlreadyRoutines are what earlier runs brought.
	AlreadyTurns    int `json:"already_turns"`
	AlreadyRoutines int `json:"already_routines"`
	// Left counts the turns and routines that are not brought, by reason.
	Left []Line `json:"left"`
	// Tables counts the workspace's rows in the source's other tables,
	// with what becomes of each. Memories counts its notebook entries by
	// state.
	Tables   []Line         `json:"tables"`
	Memories map[string]int `json:"memories,omitempty"`
}

// tableNotes says what becomes of each table of New Keystone's database.
var tableNotes = map[string]string{
	"events":             "what happened in each turn, as short words: counted into the notes of the chats, not brought as steps",
	"routine_runs":       "each run's turn is a chat of its own; the list of runs is not brought",
	"chat_attachments":   "text files sent with a message: brought as text after that message",
	"artifacts":          "apps New Keystone built, whose files are not in the database: each one's summary and state are in the note of its build's chat",
	"memories":           "notebook entries: not brought. Those New Keystone sent to Waymarks are in the knowledge store already",
	"memory_reviews":     "New Keystone's own bookkeeping: not brought",
	"decisions":          "decisions on publishing a build: not brought",
	"calendar_drafts":    "drafts of calendar events: not brought",
	"email_drafts":       "drafts of emails: not brought",
	"proposals":          "build proposals: not brought (the chat each was made in is)",
	"release_feedback":   "reports on a built app: not brought",
	"acceptance_trials":  "New Keystone's trial records: not brought",
	"trial_observations": "New Keystone's trial records: not brought",
	"lan_app_origins":    "ports of built apps on the home network: not brought",
	"wiki_article_jobs":  "New Keystone's own wiki pages: not brought",
	"wiki_revisions":     "New Keystone's own wiki pages: not brought",
	"wiki_topics":        "New Keystone's own wiki pages: not brought",
}

// brought are the tables the import reads its chats and schedules from.
var brought = map[string]bool{"jobs": true, "messages": true, "routines": true}

// prepared is a plan with everything a run needs to carry it out.
type prepared struct {
	set       Settings
	workspace string
	plan      Plan
	limits    chat.Limits
	sched     *schedules.Scheduler
	existing  []schedules.Schedule
	most      int
	personal  string // the person's own agents.time_zone
	report    Report
}

func prepare(ctx context.Context, s *tenant.Store, src *Source, workspace string, now time.Time) (*prepared, error) {
	set, err := SettingsFor(ctx, s)
	if err != nil {
		return nil, err
	}
	p := &prepared{set: set}
	if p.workspace, err = src.Settle(workspace); err != nil {
		return nil, err
	}
	d, err := src.Read(p.workspace)
	if err != nil {
		return nil, err
	}
	seenTurns, err := imported.Seen(ctx, s, imported.NewKeystone, p.workspace, imported.KindTurn)
	if err != nil {
		return nil, err
	}
	seenRoutines, err := imported.Seen(ctx, s, imported.NewKeystone, p.workspace, imported.KindRoutine)
	if err != nil {
		return nil, err
	}
	if p.limits, err = chat.DefaultLimits(); err != nil {
		return nil, err
	}
	cfg, err := schedules.DefaultConfig()
	if err != nil {
		return nil, err
	}
	p.sched = schedules.New(schedules.Options{Config: cfg, Limits: p.limits, Now: func() time.Time { return now }})
	listed, err := p.sched.List(ctx, s)
	if err != nil {
		return nil, err
	}
	p.existing, p.most, p.personal = listed.Schedules, listed.Max, listed.Zone.Name
	p.plan = MakePlan(d, seenTurns, seenRoutines, Options{
		Gap: set.Gap, TitleChars: set.TitleChars, TitleLength: p.limits.TitleLength, Zone: set.Zone, Hold: set.Hold,
		PromptMaxChars: cfg.PromptMaxChars, Now: now,
	})
	// A person may have only so many schedules: the ones past that are
	// left out, and said.
	room := max(p.most-len(p.existing), 0)
	if over := len(p.plan.Schedules) - room; over > 0 {
		adopt := 0
		for _, ps := range p.plan.Schedules {
			if p.twin(ctx, s, ps) != "" {
				adopt++
			}
		}
		if over -= adopt; over > 0 {
			p.plan.Schedules = p.plan.Schedules[:len(p.plan.Schedules)-over]
			p.plan.Left[LeftRefused] += over
		}
	}
	p.report = reportOf(src, d, p.plan)
	if len(p.plan.Schedules) > 0 {
		switch {
		case p.personal == "":
			p.report.ZoneSet = set.ZoneName
		case p.personal != set.ZoneName:
			p.report.ZoneKept, p.report.ZoneWas = p.personal, set.ZoneName
		}
	}
	return p, nil
}

// twin is the id of a schedule that is already here, paused, with exactly
// a planned schedule's name, prompt and times, and that no routine is noted
// as having become: one a run made just before it stopped, before it could
// note it. It is taken as the routine's, so a run that was cut short does
// not leave two.
func (p *prepared) twin(ctx context.Context, s *tenant.Store, ps Schedule) string {
	for _, have := range p.existing {
		if have.Enabled || have.Name != ps.Body.Name || have.Prompt != ps.Body.Prompt || !sameTimes(have.Recurrence, ps.Body.Recurrence) {
			continue
		}
		var noted bool
		err := s.DB().QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM import_sources WHERE schedule_id = ?)", have.ID).Scan(&noted)
		if err == nil && !noted {
			return have.ID
		}
	}
	return ""
}

func sameTimes(a, b schedules.Recurrence) bool {
	return a.Kind == b.Kind && a.Time == b.Time && a.Minute == b.Minute && a.Date == b.Date && strings.Join(a.Days, ",") == strings.Join(b.Days, ",")
}

// reportOf counts a plan.
func reportOf(src *Source, d *Data, p Plan) Report {
	r := Report{
		FileSHA256: src.SHA256(), SourceTurns: len(d.Turns), SourceRoutines: len(d.Routines),
		Chats: len(p.Chats), ChatsByKind: map[string]int{}, Models: map[string]int{},
		Schedules: len(p.Schedules), AlreadyTurns: p.SeenTurns, AlreadyRoutines: p.SeenRoutines,
		Left: []Line{}, Tables: []Line{},
	}
	for _, c := range p.Chats {
		r.ChatsByKind[c.Kind]++
		r.Turns += len(c.TurnIDs)
		r.Unanswered += c.Unanswered
		if c.Hold {
			r.Held++
		}
		for _, m := range c.Messages {
			r.Messages++
			at := m.At
			if r.First == nil || at.Before(*r.First) {
				r.First = &at
			}
			if r.Last == nil || at.After(*r.Last) {
				r.Last = &at
			}
			if m.Role == seam.RoleUser {
				r.FromPerson++
				continue
			}
			r.Replies++
			r.Models[m.Model]++
		}
	}
	for _, s := range p.Schedules {
		r.Brief = r.Brief || s.Brief
	}
	for _, reason := range []string{LeftInFlight, LeftEmpty, LeftManual, LeftProposed, LeftDeleted, LeftPast, LeftCadence, LeftRefused} {
		if n := p.Left[reason]; n > 0 {
			r.Left = append(r.Left, Line{What: reason, Count: n})
		}
	}
	for _, t := range d.Tables {
		if brought[t.Table] || t.Rows == 0 {
			continue
		}
		note, known := tableNotes[t.Table]
		if !known {
			note = "a table Keystone does not know: not brought"
		}
		r.Tables = append(r.Tables, Line{What: t.Table + ": " + note, Count: t.Rows})
	}
	if len(d.Memories) > 0 {
		r.Memories = d.Memories
	}
	return r
}

// Check is the dry run: it reads the source and the tenant's record of
// earlier imports, and says what a run would bring. It writes nothing.
func Check(ctx context.Context, s *tenant.Store, src *Source, workspace string, now time.Time) (Report, error) {
	p, err := prepare(ctx, s, src, workspace, now)
	if err != nil {
		return Report{}, err
	}
	p.report.Check = true
	return p.report, nil
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Times in the tables of other packages the import writes a row in.
const timeLayout = "2006-01-02T15:04:05.000Z"

// Run brings one workspace of the source into one tenant: its turns as
// chats, and its routines as paused schedules. What earlier runs brought
// is left alone. It writes to that tenant's store only, and calls no
// model, no knowledge store and no network.
//
// Each chat is written in one transaction with the record of where it came
// from, so a run that is cut short leaves whole chats and a later run goes
// on after them. The run has a started and an end entry in the tenant's
// audit log, under one request id.
func Run(ctx context.Context, s *tenant.Store, src *Source, workspace string, now time.Time) (Report, error) {
	p, err := prepare(ctx, s, src, workspace, now)
	if err != nil {
		return Report{}, err
	}
	id, err := newID()
	if err != nil {
		return Report{}, err
	}
	requestID := "import-" + id
	sent, _ := json.Marshal(map[string]any{
		"source": imported.NewKeystone, "file_sha256": src.SHA256(), "gap": p.set.Gap.String(), "hold": p.set.Hold,
		"zone": p.set.ZoneName, "chats": p.report.Chats, "messages": p.report.Messages, "schedules": p.report.Schedules,
	})
	entry := audit.Entry{Kind: audit.KindTool, RequestID: requestID, Actor: Actor, Action: Action, Outcome: audit.OutcomeStarted, Sent: string(sent)}
	if _, err := audit.Append(ctx, s, entry); err != nil {
		return Report{}, fmt.Errorf("the import could not be recorded, so nothing was brought: %w", err)
	}
	began := time.Now()
	report, err := p.carry(ctx, s, src, id, requestID, now)
	entry.Outcome, entry.DurationMS = audit.OutcomeOK, time.Since(began).Milliseconds()
	if err != nil {
		entry.Outcome = audit.OutcomeError
		failed, _ := json.Marshal(map[string]any{"error": err.Error(), "chats": report.Chats, "messages": report.Messages, "schedules": report.Schedules})
		entry.Received = string(failed)
	} else {
		done, _ := json.Marshal(report)
		entry.Received = string(done)
	}
	if _, aerr := audit.Append(ctx, s, entry); aerr != nil && err == nil {
		err = fmt.Errorf("the import was carried out and its end could not be recorded: %w", aerr)
	}
	return report, err
}

// carry writes a plan. The report it returns counts what was written, also
// when it stops early.
func (p *prepared) carry(ctx context.Context, s *tenant.Store, src *Source, id, requestID string, now time.Time) (Report, error) {
	r := p.report
	r.Chats, r.ChatsByKind, r.Turns, r.Messages, r.FromPerson, r.Replies, r.Unanswered, r.Held = 0, map[string]int{}, 0, 0, 0, 0, 0, 0
	r.Models, r.Schedules, r.Brief, r.First, r.Last = map[string]int{}, 0, false, nil, nil
	wanted := p.report
	if len(p.plan.Chats) == 0 && len(p.plan.Schedules) == 0 {
		return r, nil
	}
	r.ImportID = id
	if err := imported.Begin(ctx, s.DB(), imported.Run{
		ID: id, Source: imported.NewKeystone, Workspace: p.workspace, FileSHA256: src.SHA256(), RequestID: requestID, Started: now,
	}); err != nil {
		return r, err
	}
	for _, pc := range p.plan.Chats {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		in := chat.ImportedChat{Title: pc.Title}
		for _, m := range pc.Messages {
			blocks := make([]seam.Block, 0, len(m.Texts))
			for _, text := range m.Texts {
				blocks = append(blocks, seam.Block{Type: seam.BlockText, Text: text})
			}
			in.Messages = append(in.Messages, chat.ImportedMessage{Role: m.Role, Blocks: blocks, At: m.At})
		}
		first, last := pc.Messages[0].At, pc.Messages[0].At
		for _, m := range pc.Messages {
			if m.At.Before(first) {
				first = m.At
			}
			if m.At.After(last) {
				last = m.At
			}
		}
		_, err := chat.Import(s, p.limits, in, func(tx *sql.Tx, c chat.Chat, ids []int64) error {
			mark := imported.NewMark{Source: imported.NewKeystone, ImportID: id, Mark: imported.Mark{
				Kind: pc.Kind, At: now, Turns: len(pc.TurnIDs), Unanswered: pc.Unanswered, First: first, Last: last,
				Models: pc.Models, Steps: pc.Steps, Note: pc.Note,
			}}
			if err := imported.MarkChat(ctx, tx, c.ID, mark); err != nil {
				return err
			}
			for i, m := range pc.Messages {
				if err := imported.MarkMessage(ctx, tx, ids[i], m.TurnID, m.Model); err != nil {
					return err
				}
			}
			for _, turnID := range pc.TurnIDs {
				if err := imported.Note(ctx, tx, imported.Came{
					Source: imported.NewKeystone, Workspace: p.workspace, Kind: imported.KindTurn, SourceID: turnID,
					ImportID: id, ChatID: c.ID, At: now,
				}); err != nil {
					return err
				}
			}
			if pc.Hold {
				return hold(ctx, tx, c.ID, requestID, now)
			}
			return nil
		})
		if err != nil {
			return r, fmt.Errorf("a chat of %d turns could not be written: %w", len(pc.TurnIDs), err)
		}
		r.Chats++
		r.ChatsByKind[pc.Kind]++
		r.Turns += len(pc.TurnIDs)
		r.Unanswered += pc.Unanswered
		if pc.Hold {
			r.Held++
		}
		for _, m := range pc.Messages {
			r.Messages++
			if m.Role == seam.RoleUser {
				r.FromPerson++
			} else {
				r.Replies++
				r.Models[m.Model]++
			}
		}
	}
	r.First, r.Last = wanted.First, wanted.Last

	if len(p.plan.Schedules) > 0 && p.personal == "" {
		reg, err := settings.Registry()
		if err != nil {
			return r, err
		}
		// New Keystone kept every routine's time in one zone. A person with
		// no zone of their own gets that one, so 07:00 there is 07:00 here.
		if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, schedules.KeyTimeZone, p.set.ZoneName); err != nil {
			return r, fmt.Errorf("%s could not be set to %s: %w", schedules.KeyTimeZone, p.set.ZoneName, err)
		}
	}
	for _, ps := range p.plan.Schedules {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		scheduleID := p.twin(ctx, s, ps)
		if scheduleID == "" {
			made, err := p.sched.Create(ctx, s, ps.Body)
			if errors.Is(err, schedules.ErrInvalid) {
				// Its time passed between the plan and now, or the person's
				// own zone reads it differently.
				r.Left = addLine(r.Left, LeftRefused)
				continue
			}
			if err != nil {
				return r, fmt.Errorf("a routine could not be made a schedule: %w", err)
			}
			scheduleID = made.ID
		}
		if err := imported.Note(ctx, s.DB(), imported.Came{
			Source: imported.NewKeystone, Workspace: p.workspace, Kind: imported.KindRoutine, SourceID: ps.RoutineID,
			ImportID: id, ScheduleID: scheduleID, At: now,
		}); err != nil {
			return r, err
		}
		r.Schedules++
		r.Brief = r.Brief || ps.Brief
	}
	return r, imported.End(ctx, s.DB(), id, r.Chats, r.Messages, r.Schedules, time.Now())
}

func addLine(lines []Line, what string) []Line {
	for i := range lines {
		if lines[i].What == what {
			lines[i].Count++
			return lines
		}
	}
	return append(lines, Line{What: what, Count: 1})
}

// hold marks a chat as holding content from a sensitive space, in the
// transaction that writes the chat, so it is never in the record unheld.
// The mark is the one recall.Hold keeps: a row in turn_recall, which hangs
// on a turn. The turn is one of the chat's that calls no model, as a note's
// is, under the import's request id.
func hold(ctx context.Context, tx *sql.Tx, chatID, requestID string, now time.Time) error {
	turnID, err := newID()
	if err != nil {
		return err
	}
	at := now.UTC().Format(timeLayout)
	if _, err := tx.ExecContext(ctx, `INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at, started_at, ended_at)
		VALUES(?, ?, 'done', ?, ?, '{}', ?, ?, ?)`, turnID, chatID, requestID, HoldActor, at, at, at); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO turn_recall(turn_id, chat_id, sensitive, record) VALUES(?, ?, 1, '')", turnID, chatID)
	return err
}
