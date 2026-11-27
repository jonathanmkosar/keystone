// Package loadtest is the load test (P13-03): years of invented chats for
// one person and a smaller load for several more, the times a person waits
// on measured against the real server, each held to a budget, and the
// figures Operations reports compared with the test's own counts.
//
// Run makes a throwaway data folder, starts the real server on it as
// internal/app puts it together, in memory, with the fake provider on the
// model seam, fills the people's folders, and measures: context packing,
// past-chat search, the chat list and a long chat's page, the time to the
// first streamed words and to stream a reply for one person and for every
// person at once, the usage report and the Operations report, and the
// sizes the load leaves on disk.
//
// A real turn's rows are written by a fast path (fast.go), because the real
// paths take no date and a load dated today would sit in one day. A test
// proves the fast path leaves the rows a real turn leaves (agree.go). The
// rare things, uploads, artifacts, a project and recaps, go through the
// real store functions (extras.go).
//
// The profile and the budgets are settings, in settings/loadtest.json.
// go run ./tools/loadtest is the full run; the standing check "load test"
// in tools/verify runs the small profile. docs/architecture/load-test.md
// has the last full run's table.
package loadtest

import (
	"fmt"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/settings"
)

// Profile is how much load a run makes and how often it measures.
type Profile struct {
	// Name is "full" or "small", for the report.
	Name string
	// Seed makes the invented words and the plan: the same seed gives the
	// same chats.
	Seed uint64
	// Days is how far back the chats reach: the full profile's years, or
	// the small profile's days.
	Days int
	// Chats is how many chats the first person has, and TurnsPerChat the
	// mean number of turns in one: a chat has from 1 to twice the mean
	// less one.
	Chats, TurnsPerChat int
	// WordsPerTurn is about how many words a turn holds, the person's
	// message and the reply together: a fifth are the person's.
	WordsPerTurn int
	// LongChatTurns is the turns of the first person's one long chat.
	LongChatTurns int
	// Tenants is how many more people there are, each with TenantChats
	// chats of the same shape.
	Tenants, TenantChats int
	// Vocabulary is how many different invented words there are.
	Vocabulary int
	// UploadEvery and ArtifactEvery say one chat in that many has an
	// upload, or an artifact; zero means none. UploadKB and ArtifactKB are
	// their mean sizes.
	UploadEvery, UploadKB     int
	ArtifactEvery, ArtifactKB int
	// Runs is how many times each measurement is made.
	Runs int
	// ReplyChars is the length of the reply the fake provider streams in a
	// measured turn.
	ReplyChars int
	// BudgetPercent is the share of each budget this profile is held to,
	// and OnMedian holds the median to it, not the 95th percentile.
	BudgetPercent int
	OnMedian      bool
}

// The measurements, by the name their budget's setting ends with.
const (
	MeasureContextLong     = "context_long"
	MeasureContextProject  = "context_project"
	MeasureSearchTool      = "search_tool"
	MeasureSearchRoute     = "search_route"
	MeasureChatList        = "chat_list"
	MeasureChatPage        = "chat_page_long"
	MeasureFirstWordsOne   = "first_words_one"
	MeasureFirstWordsMany  = "first_words_several"
	MeasureStreamOne       = "stream_one"
	MeasureStreamMany      = "stream_several"
	MeasureUsageReport     = "usage_report"
	MeasureOperations      = "operations_report"
	MeasureOperationsWatch = "operations_watch"
)

// Measures is every measurement, in the order the table lists them, with
// what each one times.
var Measures = []struct{ Name, What string }{
	{MeasureContextLong, "Context packing, the long chat: a message sent to its model call opening"},
	{MeasureContextProject, "Context packing, a chat with attached chats, an upload and a project"},
	{MeasureSearchTool, "Past-chat search, the model's `search_chats`: the tool call to its result"},
	{MeasureSearchRoute, "Past-chat search, `GET /api/v1/chats/search`"},
	{MeasureChatList, "The chat list's first page, `GET /api/v1/chats`"},
	{MeasureChatPage, "The long chat's page, `GET /api/v1/chats/{id}`"},
	{MeasureFirstWordsOne, "First streamed words, one person: a message sent to the first words"},
	{MeasureFirstWordsMany, "First streamed words, every person sending at once"},
	{MeasureStreamOne, "Streaming a reply, one person: the first words to the last"},
	{MeasureStreamMany, "Streaming a reply, every person sending at once"},
	{MeasureUsageReport, "The usage report, `GET /api/v1/usage`"},
	{MeasureOperations, "The Operations report, `GET /api/v1/operations`"},
	{MeasureOperationsWatch, "The home screen's notice, `GET /api/v1/operations/watch`"},
}

// Budgets is the time budget of each measurement, by its name.
type Budgets map[string]time.Duration

// reader reads settings from the registry's defaults, and keeps the first
// error.
type reader struct {
	reg []settings.Setting
	err error
}

func (r *reader) text(key string) string {
	s, ok := settings.Lookup(r.reg, key)
	if !ok && r.err == nil {
		r.err = fmt.Errorf("the %s setting is missing from the registry", key)
	}
	return s.Default
}

func (r *reader) number(key string, least int) int {
	n, err := strconv.Atoi(r.text(key))
	if r.err == nil && (err != nil || n < least) {
		r.err = fmt.Errorf("%s must be a whole number, %d or more", key, least)
	}
	return n
}

// FullProfile reads the full run's profile from the settings registry's
// defaults.
func FullProfile() (Profile, error) {
	reg, err := settings.Registry()
	if err != nil {
		return Profile{}, err
	}
	r := &reader{reg: reg}
	p := Profile{
		Name:          "full",
		Seed:          uint64(r.number("loadtest.seed", 0)),
		Days:          365 * r.number("loadtest.years", 1),
		Chats:         r.number("loadtest.chats", 4),
		TurnsPerChat:  r.number("loadtest.turns_per_chat", 1),
		WordsPerTurn:  r.number("loadtest.words_per_turn", 20),
		LongChatTurns: r.number("loadtest.long_chat_turns", 2),
		Tenants:       r.number("loadtest.tenants", 1),
		TenantChats:   r.number("loadtest.tenant_chats", 4),
		Vocabulary:    r.number("loadtest.vocabulary", 500),
		UploadEvery:   r.number("loadtest.upload_every", 0),
		UploadKB:      r.number("loadtest.upload_kb", 1),
		ArtifactEvery: r.number("loadtest.artifact_every", 0),
		ArtifactKB:    r.number("loadtest.artifact_kb", 1),
		Runs:          r.number("loadtest.runs", 3),
		ReplyChars:    r.number("loadtest.reply_chars", 100),
		BudgetPercent: 100,
	}
	return p, r.err
}

// SmallProfile reads the small profile, the one the standing check runs:
// the full profile's words, sizes and budgets, with fewer chats, people
// and runs, and its median held to a share of each budget.
func SmallProfile() (Profile, error) {
	p, err := FullProfile()
	if err != nil {
		return Profile{}, err
	}
	r := &reader{reg: mustRegistry()}
	p.Name = "small"
	p.Days = r.number("loadtest.small_days", 2)
	p.Chats = r.number("loadtest.small_chats", 4)
	p.TurnsPerChat = r.number("loadtest.small_turns_per_chat", 1)
	p.LongChatTurns = r.number("loadtest.small_long_chat_turns", 2)
	p.Tenants = r.number("loadtest.small_tenants", 1)
	p.TenantChats = r.number("loadtest.small_tenant_chats", 4)
	p.Runs = r.number("loadtest.small_runs", 3)
	p.BudgetPercent = r.number("loadtest.small_budget_percent", 1)
	p.OnMedian = true
	p.UploadEvery, p.ArtifactEvery = min(p.UploadEvery, 8), min(p.ArtifactEvery, 10)
	p.UploadKB, p.ArtifactKB = min(p.UploadKB, 8), min(p.ArtifactKB, 4)
	p.Vocabulary = min(p.Vocabulary, 2000)
	return p, r.err
}

func mustRegistry() []settings.Setting {
	reg, _ := settings.Registry()
	return reg
}

// ReadBudgets reads each measurement's budget from the settings registry's
// defaults: loadtest.budget_<name>.
func ReadBudgets() (Budgets, error) {
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	out := Budgets{}
	for _, m := range Measures {
		key := "loadtest.budget_" + m.Name
		s, ok := settings.Lookup(reg, key)
		if !ok {
			return nil, fmt.Errorf("the %s setting is missing from the registry", key)
		}
		d, err := time.ParseDuration(s.Default)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("%s must be a time longer than nothing", key)
		}
		out[m.Name] = d
	}
	return out, nil
}
