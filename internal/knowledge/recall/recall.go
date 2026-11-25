// Package recall brings what a person's knowledge store holds into a chat.
//
// It does two things. Find searches the store with the person's newest
// message and keeps the hits that are relevant enough, each read and cut to
// a size. Lookups are three plain tools a model can call to go deeper:
// search the store, read a page, read a source.
//
// The package enforces fixed rule 3: content from a sensitive space reaches
// only models the registry marks local or trusted. Found.For and Lookups
// take the plain answer of Model.MayReadSensitive, and there is no other
// way to the content. A label that is missing reads as sensitive. The
// package reads no setting, so no value could switch the rule off: its
// sizes and its threshold come in as arguments. tools/verify lists it as a
// guard and fails if it ever imports the settings.
//
// docs/architecture/knowledge-recall.md describes it.
package recall

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
)

// The settings a caller reads to fill Options and LookupOptions, in
// settings/knowledge.json. The package itself reads none of them.
const (
	SettingOn              = "knowledge.recall"
	SettingTimeout         = "knowledge.recall_timeout"
	SettingSearchResults   = "knowledge.recall_search_max_results"
	SettingMaxItems        = "knowledge.recall_max_items"
	SettingItemMaxChars    = "knowledge.recall_item_max_chars"
	SettingQueryMaxChars   = "knowledge.recall_query_max_chars"
	SettingIgnoreWords     = "knowledge.recall_ignore_words"
	SettingSearchFirstText = "knowledge.search_first_text"
	SettingLookupResults   = "knowledge.lookup_max_results"
	SettingLookupMaxChars  = "knowledge.lookup_max_chars"

	// settingMinScorePrefix starts the key of each adapter's threshold.
	settingMinScorePrefix = "knowledge.recall_min_score_"
)

// SettingMinScore returns the key of the relevance threshold for one
// adapter: knowledge.recall_min_score_ and the adapter's name, with each
// hyphen as an underscore. A score is on the store's own scale, so each
// adapter has its own threshold.
func SettingMinScore(adapter string) string {
	return settingMinScorePrefix + strings.ReplaceAll(adapter, "-", "_")
}

// ErrOptions means the options are out of range.
var ErrOptions = errors.New("recall: options out of range")

// Options are the limits of one automatic recall.
type Options struct {
	// MinScore is the relevance threshold: a hit that scores less is left
	// out. It is on the scale of the store that is searched.
	MinScore float64
	// SearchResults is the most hits the store is asked for.
	SearchResults int
	// MaxItems is the most items one call is given.
	MaxItems int
	// ItemMaxChars is the most characters of one page or source an item
	// holds.
	ItemMaxChars int
	// QueryMaxChars is the most characters the store is searched with.
	QueryMaxChars int
	// IgnoreWords are words left out of the search, such as "the" and
	// "what": words that say nothing of what a message is about. A message
	// made of them alone searches for nothing. Letter case does not matter.
	IgnoreWords []string
}

// CheckOptions refuses options nothing can be recalled with.
func CheckOptions(o Options) error {
	switch {
	case o.MinScore < 0:
		return fmt.Errorf("%w: the relevance threshold cannot be negative", ErrOptions)
	case o.SearchResults < 1 || o.SearchResults > 100:
		return fmt.Errorf("%w: %s must be from 1 to 100", ErrOptions, SettingSearchResults)
	case o.MaxItems < 0 || o.MaxItems > 50:
		return fmt.Errorf("%w: %s must be from 0 to 50", ErrOptions, SettingMaxItems)
	case o.ItemMaxChars < 100 || o.ItemMaxChars > 100000:
		return fmt.Errorf("%w: %s must be from 100 to 100000", ErrOptions, SettingItemMaxChars)
	case o.QueryMaxChars < 1 || o.QueryMaxChars > 10000:
		return fmt.Errorf("%w: %s must be from 1 to 10000", ErrOptions, SettingQueryMaxChars)
	}
	return nil
}

// Why a hit was left out of a call.
const (
	// ReasonBelowThreshold: the hit scored under the relevance threshold.
	ReasonBelowThreshold = "below_threshold"
	// ReasonSensitive: the hit is from a sensitive space, and the call's
	// model may not read sensitive content.
	ReasonSensitive = "sensitive"
	// ReasonOverLimit: enough better hits were kept already.
	ReasonOverLimit = "over_limit"
	// ReasonUnreadable: the page or source could not be read.
	ReasonUnreadable = "unreadable"
)

// Item is one recalled piece of knowledge, shaped for the context
// builder's recall slot.
type Item struct {
	// Label says where the item came from, for the turn inspector.
	Label string
	// Text is what the model is sent. It starts with a line that names the
	// page or source and its id, so the model can cite it and read the rest.
	Text string
}

// HitRecord is what became of one hit of the search.
type HitRecord struct {
	// Type is knowledge.HitPage or knowledge.HitSource.
	Type  string  `json:"type"`
	ID    string  `json:"id"`
	Space string  `json:"space"`
	Title string  `json:"title"`
	Score float64 `json:"score"`
	// Sensitive says the hit is from a sensitive space.
	Sensitive bool `json:"sensitive"`
	// Kept says the hit was handed to the context builder.
	Kept bool `json:"kept"`
	// Reason is why it was not, and empty when it was.
	Reason string `json:"reason,omitempty"`
	// Chars is the length of the item's text, when it was kept.
	Chars int `json:"chars,omitempty"`
}

// Record is what one recall found, kept and dropped, and why. It is for
// the turn inspector, which the person reads. It names sensitive pages, so
// it is never sent to a model.
type Record struct {
	// Adapter is the knowledge store's adapter. The caller sets it.
	Adapter string `json:"adapter,omitempty"`
	// Note says why nothing was searched, when nothing was. The caller
	// sets it.
	Note string `json:"note,omitempty"`
	// Query is what the store was searched with.
	Query    string  `json:"query,omitempty"`
	MinScore float64 `json:"min_score"`
	// Found, Kept and Dropped count the hits.
	Found   int `json:"found"`
	Kept    int `json:"kept"`
	Dropped int `json:"dropped"`
	// Withheld counts the hits left out because the model may not read
	// sensitive content.
	Withheld int `json:"withheld"`
	// Sensitive says a kept item is from a sensitive space, so the call is
	// one only a local or trusted model may be sent.
	Sensitive bool        `json:"sensitive"`
	Hits      []HitRecord `json:"hits,omitempty"`
}

// Outcome is a recall for one model.
type Outcome struct {
	// Items are the recalled items, the most relevant first.
	Items []Item
	// Sensitive says an item is from a sensitive space. The call that
	// carries the items must then be marked sensitive for routing, so no
	// model that may not read it is sent the call, first or as a fallback.
	Sensitive bool
	// Record is what was found, kept and dropped.
	Record Record
}

// candidate is one hit, with its text when it was read.
type candidate struct {
	hit       knowledge.Hit
	sensitive bool
	reason    string // why no view keeps it; empty when it was read
	item      Item
}

// Found is what one search of the store brought back, for every kind of
// model. Nothing in it is exported: For is the only way to its content.
type Found struct {
	query      string
	minScore   float64
	maxItems   int
	candidates []candidate
}

// mayRead is fixed rule 3 for one piece of content: a model reads content
// under a label when the label is normal, or when the model may read
// sensitive content. Every way out of this package asks it.
func mayRead(label knowledge.Label, mayReadSensitive bool) bool {
	return mayReadSensitive || !label.Sensitive()
}

// labelOf is the label a piece of content is held to: sensitive when the
// search or the read said so. A label that is blank reads as sensitive.
func labelOf(labels ...knowledge.Label) knowledge.Label {
	for _, l := range labels {
		if l.Sensitive() {
			return knowledge.LabelSensitive
		}
	}
	return knowledge.LabelNormal
}

// Find searches the store with the person's message, by words, and reads
// the hits at or above the relevance threshold.
//
// A nil store, an empty message and a limit of no items find nothing. An
// error is the store's own, or the context's: the caller logs it and the
// chat carries on with nothing recalled.
func Find(ctx context.Context, ks knowledge.Store, message string, o Options) (Found, error) {
	if err := CheckOptions(o); err != nil {
		return Found{}, err
	}
	f := Found{minScore: o.MinScore, maxItems: o.MaxItems}
	if err := ctx.Err(); err != nil {
		return f, err
	}
	f.query = Query(message, o)
	if ks == nil || f.query == "" || o.MaxItems == 0 {
		return f, nil
	}
	hits, err := ks.Search(ctx, knowledge.Query{Text: f.query, Limit: o.SearchResults})
	if err != nil {
		return f, err
	}
	// read counts the hits that were read, and open the ones of them a
	// model that may not read sensitive content can be given. A hit is
	// read while either kind of model still has room for it.
	read, open := 0, 0
	for _, h := range hits {
		c := candidate{hit: h, sensitive: h.Label.Sensitive()}
		switch {
		case h.Score < o.MinScore:
			c.reason = ReasonBelowThreshold
		case read >= o.MaxItems && (c.sensitive || open >= o.MaxItems):
			c.reason = ReasonOverLimit
		default:
			item, label, err := readHit(ctx, ks, h, o.ItemMaxChars)
			if err != nil {
				if cerr := ctx.Err(); cerr != nil {
					return Found{minScore: o.MinScore, maxItems: o.MaxItems, query: f.query}, cerr
				}
				c.reason = ReasonUnreadable
				break
			}
			c.item = item
			c.sensitive = labelOf(h.Label, label).Sensitive()
			read++
			if !c.sensitive {
				open++
			}
		}
		f.candidates = append(f.candidates, c)
	}
	return f, nil
}

// Query returns the words a message is searched with: its words in the
// order they come, each once, in lower case, without the words to ignore
// and without single characters, cut to the longest query. A store matches
// a page on any one word, so a word such as "the" would find every page.
func Query(message string, o Options) string {
	ignore := map[string]bool{}
	for _, w := range o.IgnoreWords {
		ignore[strings.ToLower(strings.TrimSpace(w))] = true
	}
	seen := map[string]bool{}
	var words []string
	for _, w := range strings.FieldsFunc(strings.ToLower(message), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if utf8.RuneCountInString(w) < 2 || ignore[w] || seen[w] {
			continue
		}
		seen[w] = true
		words = append(words, w)
	}
	q := strings.Join(words, " ")
	if utf8.RuneCountInString(q) > o.QueryMaxChars {
		// Cut at a word's end, so no half word is searched for.
		r := []rune(q)[:o.QueryMaxChars+1]
		end := len(r) - 1
		for end > 0 && r[end] != ' ' {
			end--
		}
		if end == 0 {
			end = o.QueryMaxChars // one word longer than the whole query
		}
		q = strings.TrimSpace(string(r[:end]))
	}
	return q
}

// For returns the recall for one model. mayReadSensitive is the answer of
// Model.MayReadSensitive for the model the call goes to. With false, every
// hit from a sensitive space is left out, and the record says how many.
//
// This is the only way to what Find read.
func (f Found) For(mayReadSensitive bool) Outcome {
	out := Outcome{Record: Record{Query: f.query, MinScore: f.minScore, Found: len(f.candidates)}}
	for _, c := range f.candidates {
		label := knowledge.LabelNormal
		if c.sensitive {
			label = knowledge.LabelSensitive
		}
		hr := HitRecord{Type: c.hit.Type, ID: c.hit.ID, Space: oneLine(c.hit.Space), Title: oneLine(c.hit.Title), Score: c.hit.Score, Sensitive: c.sensitive}
		switch {
		case c.reason == ReasonBelowThreshold || c.reason == ReasonUnreadable:
			hr.Reason = c.reason
		case !mayRead(label, mayReadSensitive):
			hr.Reason = ReasonSensitive
			out.Record.Withheld++
		case c.reason != "":
			hr.Reason = c.reason
		case len(out.Items) >= f.maxItems:
			hr.Reason = ReasonOverLimit
		default:
			hr.Kept, hr.Chars = true, utf8.RuneCountInString(c.item.Text)
			out.Items = append(out.Items, c.item)
			out.Sensitive = out.Sensitive || c.sensitive
		}
		if hr.Kept {
			out.Record.Kept++
		} else {
			out.Record.Dropped++
		}
		out.Record.Hits = append(out.Record.Hits, hr)
	}
	out.Record.Sensitive = out.Sensitive
	return out
}

// readHit reads the page or source of one hit and makes its item.
func readHit(ctx context.Context, ks knowledge.Store, h knowledge.Hit, maxChars int) (Item, knowledge.Label, error) {
	switch h.Type {
	case knowledge.HitPage:
		p, err := ks.ReadPage(ctx, h.ID)
		if err != nil {
			return Item{}, "", err
		}
		body, more := window(p.Body, 0, maxChars)
		return Item{
			Label: fmt.Sprintf("%s: %s (page %s)", name(p.Space), name(p.Title), p.ID),
			Text:  "From the person's knowledge store, found by searching for their message. " + pageHeading(p) + "\n" + body + moreNote(more, "page", p.ID),
		}, p.Label, nil
	case knowledge.HitSource:
		s, err := ks.ReadSource(ctx, h.ID)
		if err != nil {
			return Item{}, "", err
		}
		body, more := window(s.Text, 0, maxChars)
		return Item{
			Label: fmt.Sprintf("%s: %s (source %s)", name(s.Space), name(s.Name), s.ID),
			Text:  "From the person's knowledge store, found by searching for their message. " + sourceHeading(s) + "\n" + body + moreNote(more, "source", s.ID),
		}, s.Label, nil
	}
	return Item{}, "", fmt.Errorf("%w: a hit of the type %q", knowledge.ErrInvalid, h.Type)
}

// pageHeading is the line that names a page, so a model can cite it.
func pageHeading(p knowledge.Page) string {
	return fmt.Sprintf("Page %q in the space %q, page id %s:", name(p.Title), name(p.Space), p.ID)
}

// sourceHeading is the line that names a source.
func sourceHeading(s knowledge.Source) string {
	line := fmt.Sprintf("Source %q in the space %q, source id %s", name(s.Name), name(s.Space), s.ID)
	if d := name(s.Dated); d != "" {
		line += ", dated " + d
	}
	return line + ":"
}

// moreNote says a text was cut, and how to read the rest.
func moreNote(more bool, what, id string) string {
	if !more {
		return ""
	}
	return fmt.Sprintf("\n[Cut here. The rest of %s %s can be read with a knowledge lookup.]", what, id)
}

// nameMaxChars is the longest a name from the store is shown in a heading.
// It keeps one long title from filling an item. It is a layout bound and
// not a limit a person tunes.
const nameMaxChars = 200

// name puts a name from the store on one line, cut to a longest length.
func name(s string) string { return cut(oneLine(s), nameMaxChars) }

// oneLine puts a text on one line with single spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// cut returns the first n characters of a text.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}

// window returns n characters of a text from the character at from, and
// whether the text goes on after them.
func window(s string, from, n int) (string, bool) {
	r := []rune(strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n")))
	if from < 0 {
		from = 0
	}
	if from >= len(r) {
		return "", false
	}
	r = r[from:]
	if len(r) <= n {
		return string(r), false
	}
	return string(r[:n]), true
}
