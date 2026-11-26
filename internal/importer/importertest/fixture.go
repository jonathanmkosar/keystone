package importertest

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// The standard fixture: one invented person's workspace, with every kind
// of thing the import reads. All of it is invented. The words below are
// the ones the tests and the check look for.
const (
	// Workspace is the invented person's workspace.
	Workspace = "Wren"

	// BeanTitle is the title the first stretch of the continuing chat gets:
	// the first words of its first message.
	BeanAsk    = "Which quillfeather beans should go in the north bed this spring?"
	BeanReply  = "Sow the quillfeather beans along the north fence once the frost has passed [1]."
	BeanAsk2   = "And how far apart?"
	BeanReply2 = "A hand's width apart, in two staggered rows."
	// BeanWord is a word found only in that chat.
	BeanWord = "quillfeather"
	// BeanFile is the text file sent with the second message.
	BeanFileName = "bed-plan.txt"
	BeanFileText = "North bed: four metres by one, shaded after three o'clock."

	PintleAsk   = "The dinghy's rudder pintle is loose again. What did I note about it?"
	PintleReply = "Your notes say the lower pintle needs a nylon bush and a split pin."
	PintleWord  = "pintle"

	// BriefPrompt is the morning brief's prompt in the fixture. It is the
	// prompt of a turn the test tenant has a recorded reply to, so the
	// schedule it becomes can run with the fake provider.
	BriefPrompt = "Write the morning harbour brief: today's date, and what my chats say about the lighthouse walk."
	BriefName   = "Morning brief"
	BriefReply  = "Daily brief: the allotment committee meets at six, and no mail is unread."
	// BriefResult is how the recorded reply to BriefPrompt begins.
	BriefResult = "Good morning. The harbour brief:"

	BuildAsk     = "Build a page that shows this week's tide times for the slipway."
	BuildReply   = "The tide page is built and waits for your review."
	BuildSummary = "A page of this week's tide times for the slipway"

	FailedAsk    = "Translate the seed packet's back into plain words."
	CancelledAsk = "Draft a long note about the greenhouse glazing."
	QueuedAsk    = "Is the water butt full after last night's rain?"

	WeeklyName   = "Sunday seed check"
	WeeklyPrompt = "List the seeds to sow this week."

	// The models the fixture's New Keystone named.
	LocalModel  = "harbour-local-12b"
	PickedModel = "tidewater-hosted-3"
)

// BriefID is the id New Keystone gives its built-in morning brief.
var BriefID = func() string {
	sum := sha256.Sum256([]byte("keystone:morning-brief"))
	return hex.EncodeToString(sum[:])[:32]
}()

func hour(h int) *int { return &h }

// Standard is the standard fixture. Its turns, in order:
//
//	0, 1  the bean chat, ten minutes apart, the first with a web source and
//	      the second with a text file
//	2     the pintle chat, four hours later, in which New Keystone read the
//	      wiki, by the model the person picked
//	3     a run of the morning brief, the next morning
//	4     a build, with the app it made
//	5     a turn that failed, straight after the build
//	6     a turn the person cancelled, a minute later
//	7     a turn still queued when the copy was made
//
// With the shipped gap of one hour that is five chats: beans, pintle, the
// brief's run, the build, and the two unanswered turns together.
func Standard() Fixture {
	day := time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)  // 09:00 in Chicago
	next := time.Date(2026, 11, 11, 13, 0, 0, 0, time.UTC) // 07:00 in Chicago
	named := func(model string) []Event {
		return []Event{{"model.started", "Model: " + model + "."}, {"model.completed", "Model: " + model + "; duration_ms: 900."}}
	}
	return Fixture{
		Workspace: Workspace,
		Turns: []Turn{
			{Prompt: BeanAsk, Reply: BeanReply, At: day, Took: 4 * time.Second,
				Events: append([]Event{
					{"context.assembled", `{"kept":0,"omitted_messages":0,"history":0,"memory":0,"memory_items":0,"attachment_tokens":0,"system":900,"omit":0,"history_limit":6000,"memory_limit":1200}`},
					{"search.started", "Tavily basic search requested."},
					{"search.completed", "Tavily basic · 1 credit · 1 sources · 420 ms."},
				}, named(LocalModel)...),
				Sources: []Link{{ID: 1, Title: "Sowing beans by a north fence", URL: "https://gardening.example/beans-north-fence"}}},
			{Prompt: BeanAsk2, Reply: BeanReply2, At: day.Add(10 * time.Minute), Took: 3 * time.Second,
				Events:         append([]Event{{"chat.attachment_saved", "Saved one text attachment (58 bytes)."}}, named(LocalModel)...),
				AttachmentName: BeanFileName, AttachmentText: BeanFileText},
			{Prompt: PintleAsk, Reply: PintleReply, Picked: PickedModel, At: day.Add(4 * time.Hour), Took: 5 * time.Second,
				Events: []Event{
					{"context.assembled", `{"kept":2,"omitted_messages":0,"history":300,"memory":200,"memory_items":2,"attachment_tokens":0,"system":900,"omit":0,"history_limit":6000,"memory_limit":1200,"waymarks_pages":2}`},
					{"wiki.lookup", `read_page {"page":"boats"}`},
					{"wiki.saved", "Saved to the Boats page, source src-test."},
				}},
			{Prompt: BriefPrompt, Reply: BriefReply, At: next, Took: 2 * time.Second, Routine: BriefID},
			{Kind: "build", Prompt: BuildAsk, Reply: BuildReply, At: next.Add(2 * time.Hour), Took: 20 * time.Minute,
				Events:  []Event{{"coding.started", "Build started."}, {"coding.model_call", "Reserved one coding model call."}, {"coding.tool_completed", "write_file"}, {"artifact.ready", "Build artifact and check evidence saved for review."}},
				Release: &Release{Summary: BuildSummary, Status: "published", Files: []string{"index.html", "tides.js", "style.css"}}},
			{Status: "failed", Prompt: FailedAsk, At: next.Add(2*time.Hour + 30*time.Minute), Took: 6 * time.Second, Events: named(LocalModel)[:1]},
			{Status: "cancelled", Prompt: CancelledAsk, At: next.Add(2*time.Hour + 31*time.Minute), Took: time.Second},
			{Status: "queued", Prompt: QueuedAsk, At: next.Add(2*time.Hour + 32*time.Minute)},
		},
		Routines: []Routine{
			{ID: BriefID, Name: BriefName, Prompt: BriefPrompt, Cadence: "daily", Hour: hour(7), Builtin: true},
			{ID: "00000000000000000000000000000c01", Name: WeeklyName, Prompt: WeeklyPrompt, Cadence: "weekly", Hour: hour(18), Minute: 30, Weekday: hour(6), Status: "paused"},
			{ID: "00000000000000000000000000000c02", Name: "Tide check on request", Prompt: "Say when high water is today.", Cadence: "manual"},
			{ID: "00000000000000000000000000000c03", Name: "Proposed frost watch", Prompt: "Warn me when frost is forecast.", Cadence: "daily", Hour: hour(6), Status: "pending"},
			{ID: "00000000000000000000000000000c04", Name: "Old compost reminder", Prompt: "Remind me to turn the compost.", Cadence: "weekdays", Hour: hour(8), Status: "deleted"},
			{ID: "00000000000000000000000000000c05", Name: "Check at 14:00", Prompt: "Check this again. The seed order.", Cadence: "once", Hour: hour(14), RunOn: "2026-11-09"},
			{ID: "00000000000000000000000000000c06", Name: "Hourly rain gauge", Prompt: "Read the rain gauge.", Cadence: "hourly"},
		},
		Memories:       []string{"active", "active", "waymarks", "waymarks", "waymarks", "forgotten"},
		Decisions:      1,
		CalendarDrafts: 2,
		EmailDrafts:    1,
		Proposals:      1,
	}
}
