package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jonathanmkosar/keystone/internal/imported"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The plan: what a workspace's turns and routines become here. It is
// worked out from what was read and writes nothing, so the dry run and the
// import see the same thing.

// BriefID is the id New Keystone gives its built-in morning brief: the
// first 32 hex characters of the SHA-256 of "keystone:morning-brief"
// (routines.py). It tells the brief from the person's own routines without
// reading a word of any.
var BriefID = func() string {
	sum := sha256.Sum256([]byte("keystone:morning-brief"))
	return hex.EncodeToString(sum[:])[:32]
}()

// The values of chats.import_hold.
const (
	// HoldNone holds no imported chat: New Keystone marked nothing as
	// sensitive.
	HoldNone = "none"
	// HoldWiki holds the chats in which New Keystone read the person's
	// Waymarks wiki.
	HoldWiki = "wiki"
	// HoldAll holds every imported chat.
	HoldAll = "all"
)

// Why something of the source is not brought in. Each is a line of the
// report, with a count and never a word of what was left.
const (
	LeftInFlight = "turns still under way when the copy was made (a later run brings them)"
	LeftEmpty    = "turns with no message of the person's"
	LeftManual   = "routines that only run when asked (Keystone has no schedule that is never due; make one here and use Run now)"
	LeftProposed = "routines New Keystone proposed and the person never scheduled"
	LeftDeleted  = "routines deleted in New Keystone"
	LeftPast     = "one-time routines whose time has passed"
	LeftCadence  = "routines whose times Keystone cannot read"
	LeftRefused  = "routines Keystone would not take as a schedule"
)

// inFlight are the statuses of a turn New Keystone is still working on.
// Every other turn is brought as it stands.
var inFlight = map[string]bool{"queued": true, "running": true, "stopping": true}

// bookkeeping are the events that say a turn was queued, run and ended, and
// what its call was built from. They are not steps a person would look for.
func bookkeeping(kind string) bool {
	switch {
	case strings.HasPrefix(kind, "job."), strings.HasPrefix(kind, "model."), strings.HasPrefix(kind, "trial."),
		strings.HasPrefix(kind, "access."):
		return true
	}
	switch kind {
	case "context.assembled", "search.started", "request.rejected", "settings.updated":
		return true
	}
	return false
}

// stepWords names New Keystone's events for a person, one and many.
var stepWords = []struct {
	kind, one, many string
}{
	{"search.completed", "web search", "web searches"},
	{"search.failed", "web search that failed", "web searches that failed"},
	{"search.review_requested", "search held for review", "searches held for review"},
	{"wiki.lookup", "wiki lookup", "wiki lookups"},
	{"wiki.saved", "note saved to the wiki", "notes saved to the wiki"},
	{"memory.remembered", "note saved to the notebook", "notes saved to the notebook"},
	{"memory.suggested", "note suggested for the notebook", "notes suggested for the notebook"},
	{"calendar.draft", "calendar draft", "calendar drafts"},
	{"workspace.status_read", "look at the workspace's tasks", "looks at the workspace's tasks"},
	{"chat.attachment_saved", "attached file", "attached files"},
	{"solution.proposed", "build proposal", "build proposals"},
	{"coding.model_call", "coding model call", "coding model calls"},
	{"coding.tool_completed", "coding tool call", "coding tool calls"},
	{"coding.browser_check", "browser check", "browser checks"},
}

// Options are the settings a plan is made with.
type Options struct {
	// Gap cuts the continuing chat into chats: chats.import_chat_gap.
	Gap time.Duration
	// TitleChars is the most characters of a first message a title takes:
	// chats.import_title_max_chars.
	TitleChars int
	// TitleLength is the most characters any title may have:
	// chats.title_max_length.
	TitleLength int
	// Zone is the zone New Keystone kept its routines' times in:
	// chats.import_time_zone.
	Zone *time.Location
	// Hold is chats.import_hold: none, wiki or all.
	Hold string
	// PromptMaxChars is the longest prompt a schedule takes:
	// agents.schedule_prompt_max_chars.
	PromptMaxChars int
	// Now is when the plan is made: a one-time routine before it has passed.
	Now time.Time
}

// Message is one message of a planned chat.
type Message struct {
	Role seam.Role
	// Texts are its text blocks, in order.
	Texts []string
	At    time.Time
	// TurnID is the source's turn it belongs to, and Model the model the
	// source named for a reply.
	TurnID, Model string
}

// Chat is one chat the import would write.
type Chat struct {
	// Kind is imported.ChatStretch, ChatRoutine or ChatBuild.
	Kind     string
	Title    string
	TurnIDs  []string
	Messages []Message
	// Unanswered counts its turns that have no reply.
	Unanswered int
	Models     []string
	Steps      map[string]int
	Note       string
	// Hold says the chat is to be held to local or trusted models.
	Hold bool
}

// Schedule is one schedule the import would make, paused.
type Schedule struct {
	// RoutineID is the routine's id in the source.
	RoutineID string
	Body      schedules.ScheduleBody
	// Brief says it is New Keystone's built-in morning brief.
	Brief bool
}

// Plan is what an import would do with one workspace.
type Plan struct {
	Chats     []Chat
	Schedules []Schedule
	// Left counts what is not brought, by the reason.
	Left map[string]int
	// SeenTurns and SeenRoutines count what is here already.
	SeenTurns, SeenRoutines int
}

// MakePlan works out what a workspace becomes. seenTurns and seenRoutines
// are the source ids that are here already; they are left alone.
func MakePlan(d *Data, seenTurns, seenRoutines map[string]string, o Options) Plan {
	p := Plan{Left: map[string]int{}}
	names := map[string]string{}
	for _, r := range d.Routines {
		names[r.ID] = r.Name
	}
	// open is the place in p.Chats of the stretch of the continuing chat a
	// next turn may join, and last when that stretch was last active.
	open := -1
	var last time.Time
	for _, t := range d.Turns {
		if _, here := seenTurns[t.ID]; here {
			p.SeenTurns++
			continue
		}
		if inFlight[t.Status] {
			p.Left[LeftInFlight]++
			continue
		}
		msgs := said(t)
		if len(msgs) == 0 {
			p.Left[LeftEmpty]++
			continue
		}
		at := msgs[0].At
		switch {
		case t.Routine != "":
			c := Chat{Kind: imported.ChatRoutine, Title: runTitle(names[t.Routine], msgs[0], o)}
			c.add(t, msgs)
			p.Chats = append(p.Chats, c)
		case t.Kind == "build":
			c := Chat{Kind: imported.ChatBuild, Title: titleOf(msgs[0].Texts[0], o)}
			c.add(t, msgs)
			p.Chats = append(p.Chats, c)
		default:
			if open < 0 || at.Sub(last) > o.Gap {
				p.Chats = append(p.Chats, Chat{Kind: imported.ChatStretch, Title: titleOf(msgs[0].Texts[0], o)})
				open = len(p.Chats) - 1
			}
			p.Chats[open].add(t, msgs)
			for _, m := range msgs {
				if m.At.After(last) {
					last = m.At
				}
			}
			if t.Updated.After(last) {
				last = t.Updated
			}
		}
	}
	for i := range p.Chats {
		c := &p.Chats[i]
		c.Note = noteOf(*c, d)
		c.Hold = o.Hold == HoldAll || (o.Hold == HoldWiki && readWiki(*c, d))
	}
	for _, r := range d.Routines {
		if _, here := seenRoutines[r.ID]; here {
			p.SeenRoutines++
			continue
		}
		body, left := scheduleOf(r, o)
		if left != "" {
			p.Left[left]++
			continue
		}
		p.Schedules = append(p.Schedules, Schedule{RoutineID: r.ID, Body: body, Brief: r.ID == BriefID})
	}
	return p
}

// add puts a turn's messages into a chat, and counts what it recorded.
func (c *Chat) add(t Turn, msgs []Message) {
	c.TurnIDs = append(c.TurnIDs, t.ID)
	c.Messages = append(c.Messages, msgs...)
	if len(msgs) < 2 {
		c.Unanswered++
	}
	for _, m := range msgs {
		if m.Model == "" {
			continue
		}
		known := false
		for _, have := range c.Models {
			known = known || have == m.Model
		}
		if !known {
			c.Models = append(c.Models, m.Model)
		}
	}
	for kind, n := range t.Events {
		if bookkeeping(kind) {
			continue
		}
		if c.Steps == nil {
			c.Steps = map[string]int{}
		}
		c.Steps[kind] += n
	}
}

// said is a turn as messages: the person's, and the reply when there is
// one. The rows of the messages table are the truth; a turn whose rows are
// gone is read from the job's own copies.
func said(t Turn) []Message {
	text, at := t.Prompt, t.Created
	if t.User != nil {
		text = t.User.Text
		if !t.User.At.IsZero() {
			at = t.User.At
		}
	}
	if strings.TrimSpace(text) == "" || at.IsZero() {
		return nil
	}
	user := Message{Role: seam.RoleUser, Texts: []string{text}, At: at, TurnID: t.ID}
	if a := t.Attachment; a != nil && strings.TrimSpace(a.Content) != "" {
		name := strings.Join(strings.Fields(a.Name), " ")
		if name == "" {
			name = "a file"
		}
		user.Texts = append(user.Texts, "Attached file: "+name+"\n\n"+a.Content)
	}
	out := []Message{user}
	reply, replied := "", at
	switch {
	case t.Assistant != nil:
		reply = t.Assistant.Text
		if !t.Assistant.At.IsZero() {
			replied = t.Assistant.At
		}
	case t.Status == "succeeded":
		reply = t.Reply
		if !t.Updated.IsZero() {
			replied = t.Updated
		}
	}
	if strings.TrimSpace(reply) == "" {
		return out
	}
	if list := sourceList(t.Sources); list != "" {
		reply = strings.TrimRight(reply, "\n ") + "\n\n" + list
	}
	return append(out, Message{Role: seam.RoleAssistant, Texts: []string{reply}, At: replied, TurnID: t.ID, Model: t.Model})
}

// sourceList writes a reply's web sources as a list at its end, which is
// where New Keystone showed them, so the numbers the reply cites still
// lead somewhere. Only an http or https address becomes a link.
func sourceList(links []Link) string {
	if len(links) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Sources:\n")
	for i, l := range links {
		number := l.Number
		if number == "" {
			number = fmt.Sprint(i + 1)
		}
		title := strings.NewReplacer("[", "(", "]", ")").Replace(l.Title)
		u, err := url.Parse(l.URL)
		linked := err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && !strings.ContainsAny(l.URL, " ()<>")
		switch {
		case linked && title != "":
			fmt.Fprintf(&b, "\n- [%s] [%s](%s)", number, title, l.URL)
		case linked:
			fmt.Fprintf(&b, "\n- [%s] <%s>", number, l.URL)
		case title != "":
			fmt.Fprintf(&b, "\n- [%s] %s", number, title)
		default:
			fmt.Fprintf(&b, "\n- [%s] %s", number, l.URL)
		}
	}
	return b.String()
}

// titleOf is a chat's title: the first words of its first message, cut at
// a word's end.
func titleOf(text string, o Options) string {
	line := ""
	for _, l := range strings.Split(text, "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			line = l
			break
		}
	}
	most := min(o.TitleChars, o.TitleLength)
	r := []rune(line)
	if len(r) > most {
		cut := most - 1
		for i := cut; i > most/2; i-- {
			if unicode.IsSpace(r[i]) {
				cut = i
				break
			}
		}
		line = strings.TrimRightFunc(string(r[:cut]), func(c rune) bool { return unicode.IsSpace(c) || unicode.IsPunct(c) }) + "…"
	}
	if line == "" {
		return "Imported chat"
	}
	return line
}

// runTitle is the title of a routine's run: the routine's name and the
// date in the routines' zone, as a scheduled run's chat is titled here.
func runTitle(name string, first Message, o Options) string {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = "Routine run"
	}
	suffix := []rune(", " + first.At.In(o.Zone).Format("Mon 2 Jan 2006"))
	r := []rune(name)
	if most := o.TitleLength - len(suffix); most < 1 {
		return string([]rune(name + string(suffix))[:max(o.TitleLength, 1)])
	} else if len(r) > most {
		r = append([]rune(strings.TrimSpace(string(r[:most-1]))), '…')
	}
	return string(r) + string(suffix)
}

// readWiki says New Keystone read the person's wiki in one of the chat's
// turns.
func readWiki(c Chat, d *Data) bool {
	in := map[string]bool{}
	for _, id := range c.TurnIDs {
		in[id] = true
	}
	for _, t := range d.Turns {
		if in[t.ID] && t.ReadWiki {
			return true
		}
	}
	return false
}

// noteOf says, for a person, what New Keystone recorded in a chat that the
// chat record cannot hold: its steps, counted, and the app a build made.
func noteOf(c Chat, d *Data) string {
	var parts []string
	var steps []string
	other := 0
	named := map[string]bool{}
	for _, w := range stepWords {
		named[w.kind] = true
		switch n := c.Steps[w.kind]; {
		case n == 1:
			steps = append(steps, "1 "+w.one)
		case n > 1:
			steps = append(steps, fmt.Sprintf("%d %s", n, w.many))
		}
	}
	var kinds []string
	for kind := range c.Steps {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		if !named[kind] {
			other += c.Steps[kind]
		}
	}
	switch {
	case other == 1:
		steps = append(steps, "1 other event")
	case other > 1:
		steps = append(steps, fmt.Sprintf("%d other events", other))
	}
	if len(steps) > 0 {
		parts = append(parts, "New Keystone recorded "+list(steps)+" here. It kept no record of what each one asked or found, so they are counted and not shown as steps.")
	}
	in := map[string]bool{}
	for _, id := range c.TurnIDs {
		in[id] = true
	}
	for _, t := range d.Turns {
		if r := t.Release; in[t.ID] && r != nil {
			summary := strings.Join(strings.Fields(r.Summary), " ")
			files := fmt.Sprintf("%d files", r.Files)
			if r.Files == 1 {
				files = "1 file"
			}
			parts = append(parts, fmt.Sprintf("This build made an app (%s, %s in New Keystone): %s The app's files stay in New Keystone.", files, r.Status, sentence(summary)))
		}
	}
	return strings.Join(parts, " ")
}

func sentence(s string) string {
	if s == "" {
		return ""
	}
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

// list joins words as "a, b and c".
func list(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// dayNames are New Keystone's weekdays, counted from Monday as 0, as
// Keystone's schedules name them.
var dayNames = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// scheduleOf is a routine as a paused schedule, or the reason it is left
// out.
func scheduleOf(r Routine, o Options) (schedules.ScheduleBody, string) {
	switch r.Status {
	case "active", "paused":
	case "pending":
		return schedules.ScheduleBody{}, LeftProposed
	case "deleted":
		return schedules.ScheduleBody{}, LeftDeleted
	default:
		return schedules.ScheduleBody{}, LeftRefused
	}
	off := false
	body := schedules.ScheduleBody{Name: strings.Join(strings.Fields(r.Name), " "), Prompt: strings.TrimSpace(r.Prompt), Enabled: &off}
	if n := []rune(body.Name); len(n) > o.TitleLength {
		body.Name = strings.TrimSpace(string(n[:o.TitleLength-1])) + "…"
	}
	if body.Name == "" || body.Prompt == "" || len([]rune(body.Prompt)) > o.PromptMaxChars {
		return schedules.ScheduleBody{}, LeftRefused
	}
	if r.Minute < 0 || r.Minute > 59 {
		return schedules.ScheduleBody{}, LeftCadence
	}
	clock := ""
	if r.Hour != nil {
		if *r.Hour < 0 || *r.Hour > 23 {
			return schedules.ScheduleBody{}, LeftCadence
		}
		clock = fmt.Sprintf("%02d:%02d", *r.Hour, r.Minute)
	}
	switch r.Cadence {
	case "manual":
		return schedules.ScheduleBody{}, LeftManual
	case "hourly":
		body.Recurrence = schedules.Recurrence{Kind: schedules.KindHourly, Minute: r.Minute}
		return body, ""
	}
	if clock == "" {
		return schedules.ScheduleBody{}, LeftCadence
	}
	switch r.Cadence {
	case "daily":
		body.Recurrence = schedules.Recurrence{Kind: schedules.KindDaily, Time: clock}
	case "weekdays":
		body.Recurrence = schedules.Recurrence{Kind: schedules.KindWeekdays, Time: clock}
	case "weekly":
		if r.Weekday == nil || *r.Weekday < 0 || *r.Weekday > 6 {
			return schedules.ScheduleBody{}, LeftCadence
		}
		body.Recurrence = schedules.Recurrence{Kind: schedules.KindWeekly, Time: clock, Days: []string{dayNames[*r.Weekday]}}
	case "once":
		if _, err := time.Parse("2006-01-02", r.RunOn); err != nil {
			return schedules.ScheduleBody{}, LeftCadence
		}
		body.Recurrence = schedules.Recurrence{Kind: schedules.KindOnce, Time: clock, Date: r.RunOn}
		if schedules.Next(body.Recurrence, o.Now, o.Zone).IsZero() {
			return schedules.ScheduleBody{}, LeftPast
		}
	default:
		return schedules.ScheduleBody{}, LeftCadence
	}
	return body, ""
}
