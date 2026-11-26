package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// View, correct and forget (P6-07). What the memory pass added is read
// from its own ledger, memory_passes, and nowhere else: a person can see
// and change only the sources Keystone's own intake put in their store,
// never what came from elsewhere, and only in their own tenant's database.
//
// A source is kept unchanged by every store, so a fact is changed by
// replacing its source: the source's other facts, and the corrected one,
// are added back as one new source with the old one's space, name, origin
// and date, and then the old source goes. A store with the RemoveSource
// capability removes it. A store without it, such as Waymarks, keeps it,
// and Keystone withdraws it (knowledge.Withdraw), so it is never read,
// found or cited again by anything Keystone opens. The ledger row follows
// the new source, and keeps no word of the forgotten fact.
//
// Fixed rule 3 holds through it: the new source goes to the old one's
// space, and when the old one was sensitive the new one must land
// sensitive, or it is taken back and the change refused. No model is
// asked anything.

// The errors a change answers with. Test with errors.Is.
var (
	// ErrNotAdded says the source is not one the memory pass added for
	// this person, or the ledger no longer points at it.
	ErrNotAdded = errors.New("memory: that source is not one the memory pass added")
	// ErrNoFact says the source holds no such fact.
	ErrNoFact = errors.New("memory: the source holds no such fact")
	// ErrCannotChange says the store can take no new source, so the facts
	// that are kept could not be put back.
	ErrCannotChange = errors.New("memory: the store cannot take the facts that are kept")
	// ErrGone says the store no longer holds the source. The ledger lets go
	// of it.
	ErrGone = errors.New("memory: the store no longer holds that source")
)

// The reasons a pass's row gives once a person changed what it added.
const (
	ReasonForgotten = "The person asked to forget the facts this pass added."
	ReasonCorrected = "The person corrected or forgot some of the facts this pass added."
	ReasonGone      = "The knowledge store no longer holds the source this pass added."
)

// Remembered returns the passes whose facts are in the store, the newest
// first, at most limit of them (all of them when limit is zero or less),
// and how many there are in all.
func Remembered(ctx context.Context, s *tenant.Store, limit int) ([]Pass, int, error) {
	var total int
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM memory_passes WHERE status = 'done' AND source_id <> ''").Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = -1 // SQLite reads a negative limit as no limit
	}
	rows, err := s.DB().QueryContext(ctx, "SELECT "+passColumns+" FROM memory_passes WHERE status = 'done' AND source_id <> '' ORDER BY seq DESC LIMIT ?", limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Pass{}
	for rows.Next() {
		p, err := scanPass(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// bySource returns the pass whose facts are in a source.
func bySource(ctx context.Context, s *tenant.Store, sourceID string) (Pass, error) {
	p, err := scanPass(s.DB().QueryRowContext(ctx,
		"SELECT "+passColumns+" FROM memory_passes WHERE status = 'done' AND source_id = ? AND source_id <> ''", sourceID))
	if errors.Is(err, sql.ErrNoRows) {
		return Pass{}, fmt.Errorf("%w: %q", ErrNotAdded, sourceID)
	}
	return p, err
}

// Edit is one change a person asks for: a fact of a source the memory pass
// added, and for a correction the fact as it should read.
type Edit struct {
	SourceID string
	// Fact is the fact as the source holds it.
	Fact string
	// Text is the fact as corrected. Empty forgets the fact.
	Text string
}

// Change is what an edit did.
type Change struct {
	TurnID string `json:"turn_id"`
	ChatID string `json:"chat_id"`
	// SourceID is the source that held the fact.
	SourceID string `json:"source_id"`
	// Removed says the store removed that source. When it is false the
	// store keeps it, and Keystone withdrew it.
	Removed bool `json:"removed"`
	// NewSourceID holds the facts that are kept, the corrected one among
	// them. Empty when no fact was kept.
	NewSourceID string `json:"new_source_id,omitempty"`
	// Facts are the facts that are kept.
	Facts     []string `json:"facts"`
	Space     string   `json:"space"`
	Sensitive bool     `json:"sensitive"`
}

// cleanFact makes a fact's spaces single and checks it is one a pass
// could have kept.
func cleanFact(text string, maxChars int) (string, error) {
	text = strings.Join(strings.Fields(text), " ")
	switch {
	case text == "":
		return "", fmt.Errorf("%w: a corrected fact needs some words", knowledge.ErrInvalid)
	case maxChars > 0 && utf8.RuneCountInString(text) > maxChars:
		return "", fmt.Errorf("%w: a fact is at most %d characters (%s)", knowledge.ErrInvalid, maxChars, SettingFactMaxChars)
	}
	return text, nil
}

// Apply corrects or forgets one fact of a source the memory pass added.
// s is the person's own tenant, ks their store as knowledge.Open handed it
// back, and adapter the store's name. factMaxChars is
// knowledge.memory_fact_max_chars, the longest a corrected fact may be.
func Apply(ctx context.Context, s *tenant.Store, ks knowledge.Store, adapter string, e Edit, factMaxChars int) (Change, error) {
	if ks == nil {
		return Change{}, knowledge.ErrNotSupported
	}
	p, err := bySource(ctx, s, e.SourceID)
	if err != nil {
		return Change{}, err
	}
	at := -1
	want := strings.Join(strings.Fields(e.Fact), " ")
	for i, f := range p.Facts {
		if f == want {
			at = i
			break
		}
	}
	if at < 0 {
		return Change{}, fmt.Errorf("%w: %q", ErrNoFact, e.SourceID)
	}
	kept := append([]string{}, p.Facts[:at]...)
	if e.Text != "" {
		text, err := cleanFact(e.Text, factMaxChars)
		if err != nil {
			return Change{}, err
		}
		kept = append(kept, text)
	}
	kept = append(kept, p.Facts[at+1:]...)

	old, err := ks.ReadSource(ctx, p.SourceID)
	if errors.Is(err, knowledge.ErrNotFound) {
		// Removed or withdrawn outside this change: the ledger lets go.
		if lerr := follow(ctx, s, p, "", nil, false, ReasonGone); lerr != nil {
			return Change{}, lerr
		}
		return Change{}, fmt.Errorf("%w: %q", ErrGone, p.SourceID)
	}
	if err != nil {
		return Change{}, err
	}
	caps := ks.Capabilities()
	if len(kept) > 0 && !caps.AddSource {
		return Change{}, ErrCannotChange
	}

	// The facts that are kept go back first, so a failure leaves the old
	// source as it was.
	ch := Change{TurnID: p.TurnID, ChatID: p.ChatID, SourceID: p.SourceID, Facts: kept, Space: old.Space, Sensitive: old.Label.Sensitive()}
	var added knowledge.Source
	if len(kept) > 0 {
		facts := make([]knowledge.Fact, len(kept))
		for i, f := range kept {
			facts[i] = knowledge.Fact{Text: f}
		}
		added, err = ks.AddSource(ctx, knowledge.NewSource{Space: old.Space, Name: old.Name, Origin: old.Origin, Dated: old.Dated, Facts: facts})
		if err != nil {
			return Change{}, err
		}
		// Fixed rule 3: a sensitive source's facts never move to a space
		// that is not sensitive.
		if old.Label.Sensitive() && !added.Label.Sensitive() {
			let := takeBack(ctx, s, ks, adapter, added.ID)
			return Change{}, errors.Join(fmt.Errorf("%w: source %s in the space %q", ErrLanded, added.ID, added.Space), let)
		}
		ch.NewSourceID, ch.Sensitive = added.ID, old.Label.Sensitive() || added.Label.Sensitive()
	}

	if caps.RemoveSource {
		err = ks.RemoveSource(ctx, old.ID)
		ch.Removed = err == nil
	} else {
		err = knowledge.Withdraw(ctx, s, adapter, old.ID)
	}
	if err != nil {
		if added.ID != "" {
			err = errors.Join(err, takeBack(ctx, s, ks, adapter, added.ID))
		}
		return Change{}, err
	}
	reason := ReasonCorrected
	if len(kept) == 0 {
		reason = ReasonForgotten
	}
	if err := follow(ctx, s, p, ch.NewSourceID, kept, ch.Sensitive, reason); err != nil {
		return Change{}, err
	}
	return ch, nil
}

// takeBack removes or withdraws a source a change added and cannot keep.
func takeBack(ctx context.Context, s *tenant.Store, ks knowledge.Store, adapter, id string) error {
	if ks.Capabilities().RemoveSource {
		return ks.RemoveSource(context.WithoutCancel(ctx), id)
	}
	return knowledge.Withdraw(context.WithoutCancel(ctx), s, adapter, id)
}

// follow points a pass's row at the source that now holds its facts, or at
// none, and keeps the facts that are left. It touches only a row that
// still points at the old source.
func follow(ctx context.Context, s *tenant.Store, p Pass, sourceID string, facts []string, sensitive bool, reason string) error {
	if facts == nil {
		facts = []string{}
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return err
	}
	_, err = s.DB().ExecContext(context.WithoutCancel(ctx), `UPDATE memory_passes SET source_id = ?, facts = ?, sensitive = ?, reason = ?
		WHERE turn_id = ? AND source_id = ?`, sourceID, string(raw), sensitive, reason, p.TurnID, p.SourceID)
	if err != nil {
		return fmt.Errorf("memory: the pass of turn %s: %w", p.TurnID, err)
	}
	return nil
}

// ProfileEdited returns the profile with each line that says the fact
// taken out, or for a correction said as the corrected text, and whether
// anything changed. A line says the fact when, list mark, letter case,
// spaces and a closing full stop aside, it is the fact.
func ProfileEdited(profile string, e Edit) (string, bool) {
	want := plainLine(e.Fact)
	if want == "" {
		return profile, false
	}
	lines := strings.Split(profile, "\n")
	out := make([]string, 0, len(lines))
	changed := false
	for _, line := range lines {
		mark, body := listMark(line)
		if plainLine(body) != want {
			out = append(out, line)
			continue
		}
		changed = true
		if text := strings.Join(strings.Fields(e.Text), " "); text != "" {
			out = append(out, mark+text)
		}
	}
	return strings.Join(out, "\n"), changed
}

// listMark splits a line into its list mark, such as "- ", and the rest.
func listMark(line string) (mark, body string) {
	trimmed := strings.TrimLeft(line, " \t")
	lead := line[:len(line)-len(trimmed)]
	for _, m := range []string{"- ", "* ", "• "} {
		if rest, ok := strings.CutPrefix(trimmed, m); ok {
			return lead + m, rest
		}
	}
	return lead, trimmed
}

func plainLine(text string) string {
	_, body := listMark(text)
	body = strings.ToLower(strings.Join(strings.Fields(body), " "))
	return strings.TrimSuffix(body, ".")
}
