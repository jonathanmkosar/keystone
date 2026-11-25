// Package compact keeps a long chat inside its model's window by
// summarising its oldest turns (P5-02). It decides when a chat's earlier
// turns are summarised and which, writes the request that asks a model for
// the summary and reads the answer, keeps the summaries in the tenant's
// database, and says whether a stored summary may be given to a call.
//
// A summary is extended, never made again from the start: the model is
// given the summary so far and the turns that follow it, and its answer
// replaces the summary so far for that branch.
//
// The package reads no setting and opens no connection. The turn reads the
// settings and passes them in as Options, and makes the model call through
// its route. It decides whether a summary of sensitive content may reach a
// call, so it is a guard of fixed rule 3, and it imports neither the
// settings registry nor the tenant's overrides, itself or through another
// package. docs/architecture/compaction.md describes it.
package compact

import (
	"errors"
	"strings"
)

// Options are compaction's tunables. The caller reads them from the
// settings in settings/chats.json.
type Options struct {
	// TriggerPercent: when the earlier turns that are not yet summarised
	// take more than this share of the room a call had for earlier turns,
	// the oldest are summarised. chats.compact_trigger_percent.
	TriggerPercent int
	// KeepPercent: the oldest are summarised until the rest take no more
	// than this share. It is less than TriggerPercent, so a chat grows by
	// the difference between one summary and the next.
	// chats.compact_keep_percent.
	KeepPercent int
	// SummaryMaxTokens is the most a summary may take, in Keystone's
	// estimate. The summary's share of the call may make it less.
	// chats.compact_summary_max_tokens.
	SummaryMaxTokens int
	// Prompt is the standing instruction the summary model is given. The
	// text {words} in it becomes the most words the summary may have.
	// chats.compact_prompt.
	Prompt string
	// MessageMaxChars is the most of one message the summary model is
	// shown. chats.compact_message_max_chars.
	MessageMaxChars int
	// CallsMax is the most model calls one summary job makes, each taking
	// in as many turns as the summary model's window holds.
	// chats.compact_calls_max.
	CallsMax int
}

// ErrOptions says the options cannot be compacted with.
var ErrOptions = errors.New("compact: the options are refused")

// CheckOptions refuses options nothing can be summarised with, and names
// the setting.
func CheckOptions(o Options) error {
	bad := func(why string) error { return errors.Join(ErrOptions, errors.New(why)) }
	switch {
	case o.TriggerPercent < 1 || o.TriggerPercent > 100:
		return bad("chats.compact_trigger_percent must be from 1 to 100")
	case o.KeepPercent < 0 || o.KeepPercent >= o.TriggerPercent:
		return bad("chats.compact_keep_percent must be from 0 to less than chats.compact_trigger_percent")
	case o.SummaryMaxTokens < 1:
		return bad("chats.compact_summary_max_tokens must be 1 or more")
	case strings.TrimSpace(o.Prompt) == "":
		return bad("chats.compact_prompt cannot be empty")
	case o.MessageMaxChars < 1:
		return bad("chats.compact_message_max_chars must be 1 or more")
	case o.CallsMax < 1:
		return bad("chats.compact_calls_max must be 1 or more")
	}
	return nil
}

// Turn is one earlier turn of a call, as the context builder recorded it
// (contextbuild.TurnRecord).
type Turn struct {
	// Tokens is the turn's estimate, with no margin.
	Tokens int
	// Kept says the turn was sent.
	Kept bool
	// Summarised says the summary was sent in its place.
	Summarised bool
}

// Span is the turns to summarise next: [From, To), as indexes into the
// earlier turns of a call.
type Span struct {
	From, To int
}

// Plan says which earlier turns of a call to summarise next. turns are the
// call's earlier turns, oldest first, without the current one. room is the
// room the call had for them (contextbuild.Record.TurnRoom).
//
// Nothing is summarised while the turns the summary does not cover take no
// more than TriggerPercent of room and none was left out. Past that, the
// oldest of them are summarised until the rest take no more than
// KeepPercent, and at least until every turn that was left out is in the
// summary, so the summary leaves no gap.
func Plan(turns []Turn, room int, o Options) (Span, bool) {
	from := 0
	for from < len(turns) && turns[from].Summarised {
		from++
	}
	if from == len(turns) {
		return Span{}, false
	}
	rest, leftOut, firstKept := 0, false, len(turns)
	for i := from; i < len(turns); i++ {
		rest += turns[i].Tokens
		switch {
		case !turns[i].Kept:
			leftOut = true
		case firstKept == len(turns):
			firstKept = i
		}
	}
	if !leftOut && rest*100 <= room*o.TriggerPercent {
		return Span{}, false
	}
	keep := room * o.KeepPercent / 100
	to := from
	for to < len(turns) && (rest > keep || to < firstKept) {
		rest -= turns[to].Tokens
		to++
	}
	if to == from {
		return Span{}, false
	}
	return Span{From: from, To: to}, true
}
