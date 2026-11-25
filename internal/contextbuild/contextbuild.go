// Package contextbuild builds what one model call carries. Given the model,
// the chat's current branch and the parts a call may hold, Build returns a
// seam request that fits the model's context window, and a record of what
// went in, what was left out and how many tokens each part took.
//
// The parts are packed in a fixed order of priority. The parts a call
// cannot do without go in first: the standing start (rules, profile, person
// map, project, style), the tools and the current turn. Then attachments,
// then earlier turns from the newest back, then recalled knowledge, then
// the summary of older history. A turn is kept whole or left out whole.
//
// The package holds no state, opens no connection and reads no store. Its
// tunables are settings, read by FromSettings and passed in as Options.
// docs/architecture/context.md describes it.
package contextbuild

import (
	"context"
	"errors"
	"math"
	"slices"

	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Part names one part of a call.
type Part string

// The parts, in the order the record lists them. The first six are the
// standing start of a call, which is the same from one call to the next.
const (
	// PartRules is the standing instructions: Options.SystemText.
	PartRules Part = "rules"
	// PartTools is the tools offered: Input.Tools. The tool loop (P7-01)
	// fills it.
	PartTools Part = "tools"
	// PartProfile is what Keystone knows of the person: Input.Profile.
	// P6-04 fills it.
	PartProfile Part = "profile"
	// PartPersonMap is the map of the person's world: Input.PersonMap.
	// P6-04 fills it.
	PartPersonMap Part = "person_map"
	// PartProject is the project's instructions: Input.Project. P9-01
	// fills it.
	PartProject Part = "project"
	// PartStyle is the style the person chose: Input.Style. P9-05 fills it.
	PartStyle Part = "style"
	// PartCurrent is the current turn: the person's newest message and, in
	// a turn that used tools, the calls and results after it.
	PartCurrent Part = "current"
	// PartAttached is attachments and attached chats: Input.Attached.
	// P5-06 fills it.
	PartAttached Part = "attached"
	// PartRecent is the turns before the current one.
	PartRecent Part = "recent"
	// PartRecall is recalled knowledge: Input.Recall. P6-05 fills it.
	PartRecall Part = "recall"
	// PartSummary is older history as a summary: Input.Summary. P5-02
	// fills it. When it is sent, the turns it covers are not.
	PartSummary Part = "summary"
	// PartPinned is the messages of the chat the person pinned whose turns
	// were not sent: Input.Pinned (P5-02). It is never left out.
	PartPinned Part = "pinned"
	// PartNote is one sentence that tells the model earlier turns were
	// left out: Options.DroppedNote. It is sent only when some were.
	PartNote Part = "note"
)

// Parts is every part, in the order a Record lists them.
var Parts = []Part{
	PartRules, PartTools, PartProfile, PartPersonMap, PartProject, PartStyle,
	PartCurrent, PartAttached, PartRecent, PartRecall, PartSummary, PartPinned, PartNote,
}

// Where a part is put in the request.
const (
	PlaceSystem   = "system"
	PlaceTools    = "tools"
	PlaceMessages = "messages"
)

// What became of a part.
const (
	// StateEmpty: there was nothing to send.
	StateEmpty = "empty"
	// StateKept: all of it was sent.
	StateKept = "kept"
	// StatePartly: some of it was sent.
	StatePartly = "partly"
	// StateDropped: there was something, and none of it was sent.
	StateDropped = "dropped"
)

// Item is one piece of a part that holds several: an attached chat's
// summary, a recalled piece of knowledge.
type Item struct {
	// Label is what the inspector calls it. It is not sent.
	Label string
	// Text is what is sent.
	Text string
	// Pinned says the item is never left out.
	Pinned bool
	// Ref is the turn inspector's name for the item (P5-04), such as
	// page:7, source:3 or message:42. It is not sent.
	Ref string
	// Removed says the person took the item out of this call from the turn
	// inspector (P5-04). It is not sent and takes no room, even when it is
	// pinned, and the record lists it as removed.
	Removed bool
}

// Pin is a message of the chat the person pinned (P5-02). It is never left
// out: when the turn it is in is sent, it is there already, and otherwise
// its item is sent in the pinned part. Room for it is held as for every
// part that is never left out.
type Pin struct {
	// Message is the index in Input.Messages of the pinned message. A pin
	// whose message is not among them, -1, is always sent as its item.
	Message int
	// Item is what is sent when the message's turn is not: a label for the
	// inspector and the text.
	Item
}

// Input is everything one call is built from. Only Model, Budget, Options
// and Messages are needed. A part that is left empty is listed in the
// record as empty and takes no room.
type Input struct {
	// Model is the registry entry the call goes to.
	Model models.Model
	// Budget is how tokens are estimated.
	Budget budget.Options
	// Options are this package's settings.
	Options Options
	// MaxOutputTokens is the room held back for the reply. Zero means the
	// model's output limit.
	MaxOutputTokens int
	// ThinkingTokens is passed to the request as it is.
	ThinkingTokens int
	// Provider, when it can count tokens (budget.Counter), is asked to
	// count the packed request, and its count is believed over the
	// estimate. It may be nil. Nothing is sent to the model that answers.
	Provider seam.Provider

	// Messages is the chat's current branch, oldest first, as a provider
	// takes it: sides alternate, and every tool result follows its call.
	// The last turn in it is the current one.
	Messages []seam.Message

	// Tools are the tools offered (P7-01).
	Tools []seam.Tool
	// Profile is what Keystone knows of the person (P6-04).
	Profile string
	// PersonMap is the map of the person's world (P6-04).
	PersonMap string
	// Project is the project's instructions (P9-01).
	Project string
	// Style is the style the person chose (P9-05).
	Style string
	// Attached is attachments and attached chats, the most wanted first
	// (P5-06).
	Attached []Item
	// Recall is recalled knowledge, the most relevant first (P6-05).
	Recall []Item
	// Summary is the summary of older history (P5-02).
	Summary string
	// SummaryCovers is how many of Messages, from the first, Summary
	// stands for (P5-02). The turns that lie wholly inside them are not
	// sent when the summary is. When it does not fit its share, they are
	// packed as any other earlier turn.
	SummaryCovers int
	// SummaryRef is the turn inspector's name for the summary (P5-04),
	// such as summary:12. The record carries it on the summary part.
	SummaryRef string
	// Pinned is the messages of the chat the person pinned (P5-02).
	Pinned []Pin
}

// Record is what one call was built from. The turn inspector (P5-04)
// shows it.
type Record struct {
	Model string `json:"model"`
	// Window is the model's context window.
	Window int `json:"window"`
	// Output is the room held back for the reply.
	Output int `json:"output"`
	// Room is the input tokens the window leaves: budget.InputRoom.
	Room int `json:"room"`
	// Limit is what the builder packed under: Room, less the share the
	// fill setting keeps free.
	Limit int `json:"limit"`
	// Tokens is the request's input tokens as budget counts them: the
	// provider's own count, or the estimate with its margin.
	Tokens int `json:"tokens"`
	// Margin is the part of Tokens that is the estimate's safety margin.
	Margin int `json:"margin"`
	// Exact says the provider counted.
	Exact bool `json:"exact"`
	// Over is how many tokens the request is over Limit. It is more than
	// zero only when the parts that are never left out do not fit.
	Over int `json:"over"`
	// Recounts is how many times the request was packed again because the
	// provider counted more than the estimate.
	Recounts int `json:"recounts,omitempty"`
	// TurnRoom is the room the earlier turns were packed in, in estimated
	// tokens with no margin: what was left once the parts that are never
	// left out and attachments were in, and recall and the summary were
	// held their shares. Compaction (P5-02) measures against it.
	TurnRoom int `json:"turn_room"`
	// SummaryRoom is the most the summary could take in this call: its
	// share of the open room, in estimated tokens with no margin.
	SummaryRoom int `json:"summary_room"`
	// Parts lists every part, in the order of Parts.
	Parts []PartRecord `json:"parts"`
	// Turns lists every turn of the branch, oldest first.
	Turns []TurnRecord `json:"turns"`
}

// PartRecord is one part of a call.
type PartRecord struct {
	Part Part `json:"part"`
	// Place is where the part goes in the request.
	Place string `json:"place"`
	// State is what became of it.
	State string `json:"state"`
	// Tokens is the estimate of what was sent, with no margin.
	Tokens int `json:"tokens"`
	// Kept and Dropped count its pieces: text blocks, tools, turns, items.
	Kept    int `json:"kept"`
	Dropped int `json:"dropped"`
	// DroppedTokens is the estimate of what was left out.
	DroppedTokens int `json:"dropped_tokens"`
	// Covers is, for the summary, how many earlier turns it was sent in
	// place of.
	Covers int `json:"covers,omitempty"`
	// Removed counts the pieces the person took out of the call from the
	// turn inspector (P5-04). They are counted neither kept nor dropped.
	Removed int `json:"removed,omitempty"`
	// Ref names what the part was made from, for the turn inspector: on
	// the summary, Input.SummaryRef (P5-04).
	Ref string `json:"ref,omitempty"`
	// Items lists the pieces of a part that holds several, in the order
	// they were given.
	Items []ItemRecord `json:"items,omitempty"`
}

// ItemRecord is one piece of a part.
type ItemRecord struct {
	Label string `json:"label,omitempty"`
	// Ref is the item's Ref (P5-04).
	Ref    string `json:"ref,omitempty"`
	Tokens int    `json:"tokens"`
	Kept   bool   `json:"kept"`
	Pinned bool   `json:"pinned,omitempty"`
	// InTurn says a pinned message was not sent as an item because the
	// turn it is in was sent.
	InTurn bool `json:"in_turn,omitempty"`
	// Removed says the person took the item out of the call (P5-04). Its
	// Tokens are what it would have taken.
	Removed bool `json:"removed,omitempty"`
}

// TurnRecord is one turn of the branch: a message of the person's and
// everything up to their next.
type TurnRecord struct {
	// First is the index in Input.Messages of the turn's first message.
	First int `json:"first"`
	// Messages is how many messages the turn has.
	Messages int `json:"messages"`
	// Tokens is the estimate of the turn, with no margin.
	Tokens int `json:"tokens"`
	// Part is current for the last turn and recent for the others.
	Part Part `json:"part"`
	// Kept says the turn was sent.
	Kept bool `json:"kept"`
	// Summarised says the turn was not sent because the summary was, in
	// its place.
	Summarised bool `json:"summarised,omitempty"`
}

// Part returns the record of one part.
func (r Record) Part(p Part) PartRecord {
	for _, x := range r.Parts {
		if x.Part == p {
			return x
		}
	}
	return PartRecord{Part: p, State: StateEmpty}
}

// ErrNoMessages is returned for a branch with no message in it.
var ErrNoMessages = errors.New("contextbuild: the branch has no messages")

// Build packs one call. The request it returns always holds the standing
// start, the tools, the pinned items and the current turn. When those
// alone are more than the window allows, the request is returned as it is
// with Record.Over saying by how much, and budget.Guard refuses it as any
// call that cannot fit is refused.
//
// The only errors are a branch with no messages, options that are not
// valid, and a cancelled ctx.
func Build(ctx context.Context, in Input) (seam.Request, Record, error) {
	if err := budget.CheckOptions(in.Budget); err != nil {
		return seam.Request{}, Record{}, err
	}
	if err := CheckOptions(in.Options); err != nil {
		return seam.Request{}, Record{}, err
	}
	if len(in.Messages) == 0 {
		return seam.Request{}, Record{}, ErrNoMessages
	}
	room := budget.InputRoom(in.Model, in.MaxOutputTokens)
	limit := room * in.Options.FillPercent / 100
	output := in.Model.ContextWindow - room

	// Every piece is measured once, with no margin.
	w := weigh(in)

	// The parts are packed against a number of unmargined tokens whose
	// margined count is within the limit.
	target := underMargin(limit, in.Budget.MarginPercent)
	var req seam.Request
	var rec Record
	recounts := 0
	for {
		req, rec = pack(in, w, target)
		count, err := budget.Tokens(ctx, in.Provider, req, in.Budget)
		if err != nil {
			return seam.Request{}, Record{}, err
		}
		rec.Model, rec.Window, rec.Output, rec.Room, rec.Limit = in.Model.Name, in.Model.ContextWindow, output, room, limit
		rec.Tokens, rec.Margin, rec.Exact, rec.Recounts = count.Tokens, count.Margin, count.Exact, recounts
		rec.Over = max(count.Tokens-limit, 0)
		// A provider that counts more than the estimate: pack again, under
		// a target made smaller by as much as the provider counted more.
		if rec.Over == 0 || !count.Exact || recounts >= in.Options.RecountMax || !rec.canShrink() {
			return req, rec, nil
		}
		recounts++
		sent := 0
		for _, p := range rec.Parts {
			sent += p.Tokens
		}
		target = min(int(float64(sent)*float64(limit)/float64(count.Tokens)), sent-1)
	}
}

// canShrink says whether packing under a smaller target could leave
// anything more out.
func (r Record) canShrink() bool {
	for _, t := range r.Turns {
		if t.Kept && t.Part == PartRecent {
			return true
		}
	}
	for _, p := range r.Parts {
		for _, it := range p.Items {
			if it.Kept && !it.Pinned {
				return true
			}
		}
		if p.Part == PartSummary && p.Kept > 0 {
			return true
		}
	}
	return false
}

// underMargin is the largest unmargined count whose margined count, as
// budget.Estimated makes it, is no more than limit.
func underMargin(limit, marginPercent int) int {
	t := limit * 100 / (100 + marginPercent)
	for t > 0 && t+int(math.Ceil(float64(t)*float64(marginPercent)/100)) > limit {
		t--
	}
	return max(t, 0)
}

// weights is the size of every piece of an input, in estimated tokens
// with no margin.
type weights struct {
	rules, profile, personMap, project, style int
	tools                                     int
	summary, note                             int
	attached, recall, pinned                  []int
	turns                                     []span
}

// span is one turn: messages[first:end].
type span struct {
	first, end int
	tokens     int
}

func weigh(in Input) weights {
	text := func(s string) int {
		if s == "" {
			return 0
		}
		return budget.Estimate(seam.Request{System: []seam.Block{{Type: seam.BlockText, Text: s}}}, in.Budget)
	}
	items := func(list []Item) []int {
		out := make([]int, len(list))
		for i, it := range list {
			out[i] = text(it.Text)
		}
		return out
	}
	w := weights{
		rules: text(in.Options.SystemText), profile: text(in.Profile), personMap: text(in.PersonMap),
		project: text(in.Project), style: text(in.Style),
		summary: text(in.Summary), note: text(in.Options.DroppedNote),
		attached: items(in.Attached), recall: items(in.Recall),
	}
	for _, p := range in.Pinned {
		w.pinned = append(w.pinned, text(p.Text))
	}
	if len(in.Tools) > 0 {
		w.tools = budget.Estimate(seam.Request{Tools: in.Tools}, in.Budget)
	}
	for _, s := range Turns(in.Messages) {
		w.turns = append(w.turns, span{first: s[0], end: s[1],
			tokens: budget.Estimate(seam.Request{Messages: in.Messages[s[0]:s[1]]}, in.Budget)})
	}
	return w
}

// Turns cuts a branch into turns and returns each as the indexes
// [first, end) of its messages. A turn starts at a message of the person's
// that holds no tool result, and only where no tool call is waiting for
// its result. So a tool call and its result are always in one turn, and
// leaving a turn out never parts them.
func Turns(messages []seam.Message) [][2]int {
	var out [][2]int
	open := map[string]bool{}
	start := 0
	for i, m := range messages {
		results := false
		for _, b := range m.Blocks {
			if b.Type == seam.BlockToolResult {
				results = true
			}
		}
		if i > start && m.Role == seam.RoleUser && !results && len(open) == 0 {
			out = append(out, [2]int{start, i})
			start = i
		}
		for _, b := range m.Blocks {
			switch b.Type {
			case seam.BlockToolCall:
				open[b.ID] = true
			case seam.BlockToolResult:
				delete(open, b.ID)
			}
		}
	}
	if len(messages) > start {
		out = append(out, [2]int{start, len(messages)})
	}
	return out
}

// pack chooses what goes into the request under target, which is in
// estimated tokens with no margin, and writes the request and the record.
//
// A summary that stands for earlier turns is packed in their place. When
// it could not be sent after all, which only shares that add up to more
// than the open room can bring about, the call is packed again as if the
// summary covered nothing, so no turn is ever lost without the note.
func pack(in Input, w weights, target int) (seam.Request, Record) {
	if req, rec, ok := packWith(in, w, target, true); ok {
		return req, rec
	}
	req, rec, _ := packWith(in, w, target, false)
	return req, rec
}

// coveredTurns is how many of turns, from the first, lie wholly inside the
// first covers messages.
func coveredTurns(turns []span, covers int) int {
	n := 0
	for n < len(turns) && turns[n].end <= covers {
		n++
	}
	return n
}

// turnOf is the index of the turn message i is in, or -1.
func turnOf(turns []span, i int) int {
	for t, s := range turns {
		if i >= s.first && i < s.end {
			return t
		}
	}
	return -1
}

// packWith is pack, with useSummary saying whether the summary may stand
// for the turns it covers. ok is false when it was meant to and was not
// sent.
func packWith(in Input, w weights, target int, useSummary bool) (seam.Request, Record, bool) {
	o := in.Options
	cur := len(w.turns) - 1

	// What is never left out.
	left := target - w.rules - w.tools - w.profile - w.personMap - w.project - w.style - w.turns[cur].tokens
	keepAttached, keepRecall := make([]bool, len(in.Attached)), make([]bool, len(in.Recall))
	for i, it := range in.Attached {
		if it.Pinned && it.Text != "" && !it.Removed {
			keepAttached[i] = true
			left -= w.attached[i]
		}
	}
	for i, it := range in.Recall {
		if it.Pinned && it.Text != "" && !it.Removed {
			keepRecall[i] = true
			left -= w.recall[i]
		}
	}
	// A pinned message outside the current turn is held its room here.
	// When its turn is sent below, the room is given back and the message
	// is not sent a second time. One the person removed is held nothing.
	pinTurn, heldPin := make([]int, len(in.Pinned)), make([]bool, len(in.Pinned))
	for i, p := range in.Pinned {
		pinTurn[i] = turnOf(w.turns, p.Message)
		if p.Text != "" && !p.Removed && pinTurn[i] != cur {
			heldPin[i] = true
			left -= w.pinned[i]
		}
	}
	// open is the room the other parts share. Each share below is a part
	// of it.
	open := max(left, 0)
	share := func(percent int) int { return open * percent / 100 }

	// fill keeps the items that fit in room, in the order given, and
	// returns what they took. An item too big for what is left is passed
	// over, and a smaller one after it may still fit.
	fill := func(items []Item, sizes []int, keep []bool, room int) int {
		used := 0
		for i, it := range items {
			if keep[i] || it.Text == "" || it.Removed || used+sizes[i] > room {
				continue
			}
			keep[i] = true
			used += sizes[i]
		}
		return used
	}
	need := func(items []Item, sizes []int, keep []bool) int {
		n := 0
		for i, it := range items {
			if !keep[i] && it.Text != "" && !it.Removed {
				n += sizes[i]
			}
		}
		return n
	}

	// Attachments come before earlier turns.
	left -= fill(in.Attached, w.attached, keepAttached, min(share(o.AttachedPercent), left))

	// Recall and the summary come after earlier turns, and each is held a
	// share of the room, so that a long chat does not crowd both out.
	heldRecall := min(need(in.Recall, w.recall, keepRecall), share(o.RecallPercent))
	heldSummary := min(w.summary, share(o.SummaryPercent))

	// The turns the summary stands for. When it fits its share they are
	// not sent, because it is, in their place: floor is the oldest turn
	// that may be sent. When it does not fit they are packed as any other.
	floor := 0
	if useSummary && w.summary > 0 && w.summary <= share(o.SummaryPercent) {
		floor = coveredTurns(w.turns[:cur], in.SummaryCovers)
	}

	// Earlier turns, from the newest back. The first one that does not fit
	// ends the packing: no older turn is sent past a gap.
	recent := func(room int) (from, used int) {
		from = cur
		for i := cur - 1; i >= floor; i-- {
			if o.RecentTurnsMax > 0 && cur-i > o.RecentTurnsMax {
				break
			}
			if used+w.turns[i].tokens > room {
				break
			}
			from, used = i, used+w.turns[i].tokens
		}
		return from, used
	}
	recentRoom := left - heldRecall - heldSummary
	from, used := recent(recentRoom)
	note := false
	if from > floor && w.note > 0 {
		// Turns were left out, so the note is sent, and has to fit too.
		note = true
		if used+w.note > recentRoom {
			from, used = recent(recentRoom - w.note)
		}
		if w.note > left-used {
			note = false
		} else {
			left -= w.note
		}
	}
	left -= used

	// A pinned message whose turn was sent is there already.
	sendPin := make([]bool, len(in.Pinned))
	for i := range in.Pinned {
		switch {
		case !heldPin[i]:
		case pinTurn[i] >= from && pinTurn[i] < cur:
			left += w.pinned[i]
		default:
			sendPin[i] = true
		}
	}

	left -= fill(in.Recall, w.recall, keepRecall, min(share(o.RecallPercent), left))
	keepSummary := w.summary > 0 && w.summary <= min(share(o.SummaryPercent), left)
	if floor > 0 && !keepSummary {
		return seam.Request{}, Record{}, false
	}

	// The request. The standing start is first and never varies with the
	// chat's length. What varies comes after it.
	req := seam.Request{
		Model: in.Model.Name, Tools: in.Tools,
		MaxOutputTokens: in.MaxOutputTokens, ThinkingTokens: in.ThinkingTokens,
		Messages: slices.Clone(in.Messages[w.turns[from].first:]),
	}
	system := func(text string) {
		req.System = append(req.System, seam.Block{Type: seam.BlockText, Text: text})
	}
	var turns []TurnRecord
	one := func(p Part, text string, tokens int, kept bool) PartRecord {
		pr := PartRecord{Part: p, Place: PlaceSystem, State: StateEmpty}
		switch {
		case text == "":
		case kept:
			system(text)
			pr.State, pr.Tokens, pr.Kept = StateKept, tokens, 1
		default:
			pr.State, pr.Dropped, pr.DroppedTokens = StateDropped, 1, tokens
		}
		return pr
	}
	many := func(p Part, items []Item, sizes []int, keep []bool) PartRecord {
		pr := PartRecord{Part: p, Place: PlaceSystem}
		for i, it := range items {
			if it.Text == "" {
				continue
			}
			pr.Items = append(pr.Items, ItemRecord{Label: it.Label, Ref: it.Ref, Tokens: sizes[i], Kept: keep[i], Pinned: it.Pinned, Removed: it.Removed})
			switch {
			case keep[i]:
				system(it.Text)
				pr.Kept, pr.Tokens = pr.Kept+1, pr.Tokens+sizes[i]
			case it.Removed:
				pr.Removed++
			default:
				pr.Dropped, pr.DroppedTokens = pr.Dropped+1, pr.DroppedTokens+sizes[i]
			}
		}
		pr.State = state(pr.Kept, pr.Dropped+pr.Removed)
		return pr
	}
	turnsOf := func(p Part, lo, hi int) PartRecord {
		pr := PartRecord{Part: p, Place: PlaceMessages}
		for i := lo; i < hi; i++ {
			kept, summarised := i >= from, i < floor
			turns = append(turns, TurnRecord{
				First: w.turns[i].first, Messages: w.turns[i].end - w.turns[i].first,
				Tokens: w.turns[i].tokens, Part: p, Kept: kept, Summarised: summarised,
			})
			switch {
			case kept:
				pr.Kept, pr.Tokens = pr.Kept+1, pr.Tokens+w.turns[i].tokens
			case !summarised:
				pr.Dropped, pr.DroppedTokens = pr.Dropped+1, pr.DroppedTokens+w.turns[i].tokens
			}
		}
		pr.State = state(pr.Kept, pr.Dropped)
		return pr
	}
	pinned := func() PartRecord {
		pr := PartRecord{Part: PartPinned, Place: PlaceSystem}
		gone := 0 // removed, and not in a turn that was sent
		for i, p := range in.Pinned {
			if p.Text == "" {
				continue
			}
			// A removed pin whose turn is sent is in the call all the same,
			// in its turn.
			inTurn := !sendPin[i]
			if p.Removed {
				inTurn = pinTurn[i] == cur || (pinTurn[i] >= from && pinTurn[i] < cur)
				pr.Removed++
				if !inTurn {
					gone++
				}
			}
			pr.Items = append(pr.Items, ItemRecord{Label: p.Label, Ref: p.Ref, Tokens: w.pinned[i], Kept: sendPin[i], Pinned: true, InTurn: inTurn, Removed: p.Removed})
			if sendPin[i] {
				system(p.Text)
				pr.Kept, pr.Tokens = pr.Kept+1, pr.Tokens+w.pinned[i]
			}
		}
		pr.State = state(pr.Kept, gone)
		return pr
	}

	// The turns are listed in the order of the branch, so the earlier ones
	// are written before the current one.
	earlier, current := turnsOf(PartRecent, 0, cur), turnsOf(PartCurrent, cur, cur+1)
	noteText := ""
	if from > floor {
		noteText = o.DroppedNote
	}
	summary := func() PartRecord {
		pr := one(PartSummary, in.Summary, w.summary, keepSummary)
		if keepSummary {
			pr.Covers = floor
		}
		if in.Summary != "" {
			pr.Ref = in.SummaryRef
		}
		return pr
	}
	// In the order of Parts. A system block is appended as its part is
	// written, so the standing start comes first in the request too.
	parts := []PartRecord{
		one(PartRules, o.SystemText, w.rules, true),
		{Part: PartTools, Place: PlaceTools, State: state(len(in.Tools), 0), Tokens: w.tools, Kept: len(in.Tools)},
		one(PartProfile, in.Profile, w.profile, true),
		one(PartPersonMap, in.PersonMap, w.personMap, true),
		one(PartProject, in.Project, w.project, true),
		one(PartStyle, in.Style, w.style, true),
		current,
		many(PartAttached, in.Attached, w.attached, keepAttached),
		earlier,
		many(PartRecall, in.Recall, w.recall, keepRecall),
		summary(),
		pinned(),
		one(PartNote, noteText, w.note, note),
	}
	return req, Record{Parts: parts, Turns: turns, TurnRoom: max(recentRoom, 0), SummaryRoom: share(o.SummaryPercent)}, true
}

func state(kept, dropped int) string {
	switch {
	case kept == 0 && dropped == 0:
		return StateEmpty
	case dropped == 0:
		return StateKept
	case kept == 0:
		return StateDropped
	}
	return StatePartly
}
