package router

import (
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
)

// Facts are what the turn knows about one message's call before it is
// routed. The router reads nothing else about the chat.
type Facts struct {
	// Text is the newest message's text. Only the decision model sees it,
	// cut to router.verdict_max_chars, and it is never recorded here.
	Text string
	// Tokens is the call's estimated input, as the budget counts it.
	Tokens int
	// ReplyTokens is the room the call asks for the reply. Zero or more
	// than a model writes keeps that model's output limit, as the budget
	// does.
	ReplyTokens int
	// Images, Files and Tools say what the call holds: an image asks for
	// vision, a file for files, and offered tools (how many) for tools.
	Images bool
	Files  bool
	Tools  int
	// PreviousStep is the registry name of the model the chat's previous
	// message used, or empty for a first message.
	PreviousStep string
	// Turns is how many turns the chat has had before this one.
	Turns int
	// Held says the chat is held for sensitive content (decision 0211):
	// fixed rule 3 holds it to local and trusted steps, and its verdict to
	// a decision model that may read it.
	Held bool
	// LocalFailedAt is when a call to a local endpoint, a decision call
	// included, last failed with unavailable. Zero means none has.
	LocalFailedAt time.Time
	// Now is the time the message is routed. Zero means the clock's.
	Now time.Time
}

// Needs are the capabilities the call cannot do without (rule 2).
func (f Facts) Needs() []models.Capability {
	var needs []models.Capability
	if f.Images {
		needs = append(needs, models.CapVision)
	}
	if f.Tools > 0 {
		needs = append(needs, models.CapTools)
	}
	if f.Files {
		needs = append(needs, models.CapFiles)
	}
	return needs
}

// localDown reports whether a local endpoint failed with unavailable
// within router.local_down_for (rule 4).
func (f Facts) localDown(s Settings) bool {
	if f.LocalFailedAt.IsZero() || s.LocalDownFor <= 0 {
		return false
	}
	now := f.Now
	if now.IsZero() {
		now = time.Now()
	}
	return now.Sub(f.LocalFailedAt) < s.LocalDownFor
}

// Why a step of the ladder is not a candidate.
const (
	// SkipUnknown: the name is not a usable model in the person's registry.
	SkipUnknown = routing.WithheldUnknown
	// SkipSensitive: the chat is held and the model may not read sensitive
	// content (rule 1).
	SkipSensitive = routing.WithheldSensitive
	// SkipCapability: the model lacks something the call needs (rule 2),
	// or takes no chat calls at all, as an embedding or decision model.
	SkipCapability = routing.WithheldCapability
	// SkipSize: the call's estimate is over the step's size limit (rule 3).
	SkipSize = "size"
	// SkipWindow: the call and the room for its reply do not fit the
	// model's window (rule 3).
	SkipWindow = "window"
	// SkipLocalDown: the step is local and the local machine is not
	// answering (rule 4).
	SkipLocalDown = "local_down"
)

// Skip is one step of the ladder that is not a candidate, and why.
type Skip struct {
	Name string `json:"name"`
	// Why is one of the Skip constants.
	Why string `json:"why"`
}

// Candidate is a step the rules leave for this call.
type Candidate struct {
	Model models.Model
	// Step is the step's place on the ladder, from 1, smallest first.
	Step int
	// Limit is the step's size limit, or zero.
	Limit int
}

// Candidates applies the four rules to the ladder, in order: a held chat
// keeps only steps that may read sensitive content, the call's needs, the
// size limits and the window, and the local machine. What is left keeps
// the ladder's order, smallest first.
//
// Each step is asked of routing.Plan on its own, as a chat call to that
// model with the held mark as sensitive and the needs, so a step is kept
// only when the plan would send the call to it. The router narrows and
// orders what the plan allows and can add nothing to it.
func Candidates(s Settings, f Facts, usable []models.Model) ([]Candidate, []Skip) {
	var cands []Candidate
	var skipped []Skip
	down := f.localDown(s)
	needs := f.Needs()
	for i, r := range s.Ladder {
		skip := func(why string) { skipped = append(skipped, Skip{Name: r.Name, Why: why}) }
		route, err := routing.Plan(routing.Rules{}, routing.Ask{Kind: routing.KindChat, Model: r.Name, Sensitive: f.Held, Needs: needs}, usable)
		if err != nil || len(route.Models) == 0 || route.Models[0].Name != r.Name {
			why := SkipUnknown
			if len(route.Withheld) > 0 {
				why = route.Withheld[0].Why
			}
			skip(why)
			continue
		}
		m := route.Models[0]
		switch {
		case f.Held && !m.MayReadSensitive():
			// routing.Plan never keeps one; this says so a second time.
			skip(SkipSensitive)
		case r.Limit > 0 && f.Tokens > r.Limit:
			skip(SkipSize)
		case !fitsWindow(m, f):
			skip(SkipWindow)
		case down && m.Class == models.ClassLocal:
			skip(SkipLocalDown)
		default:
			cands = append(cands, Candidate{Model: m, Step: i + 1, Limit: r.Limit})
		}
	}
	return cands, skipped
}

// fitsWindow is the budget's check (budget.Measure) on the facts: the
// estimated input and the room kept for the reply fit the model's window.
// The room is what the call asks for, or the model's output limit when it
// asks for nothing or for more, as budget.Reply works it out. The router
// cannot import the budget, which reads settings.
func fitsWindow(m models.Model, f Facts) bool {
	reply := m.OutputLimit
	if f.ReplyTokens > 0 && f.ReplyTokens < m.OutputLimit {
		reply = f.ReplyTokens
	}
	return f.Tokens+reply <= m.ContextWindow
}

// refusal is why no candidate is left, from what was skipped.
func refusal(s Settings, skipped []Skip) *Refusal {
	if len(s.Ladder) == 0 {
		return &Refusal{Code: CodeNoLadder, Message: "There is no ladder for this person, so Auto is not offered. Pick a model for the chat."}
	}
	switch {
	case hasSkip(skipped, SkipLocalDown):
		return &Refusal{Code: CodeLocalUnavailable, Skipped: skipped, Message: "The local machine is not answering, and no other step of the ladder may take this message. Try again when the machine is back."}
	case hasSkip(skipped, SkipSensitive):
		return &Refusal{Code: CodeSensitive, Skipped: skipped, Message: "This chat holds content from a sensitive space, and no step of the ladder that may take it is local or trusted, so it was not sent."}
	}
	return &Refusal{Code: CodeNoModel, Skipped: skipped, Message: "No step of the ladder can take this message."}
}
