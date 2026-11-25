package recall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The names of the three lookups, as a model calls them.
const (
	LookupSearch     = "knowledge_search"
	LookupReadPage   = "knowledge_read_page"
	LookupReadSource = "knowledge_read_source"
)

var (
	// ErrInput means the model's input to a lookup does not read. Its
	// message is written for the model.
	ErrInput = errors.New("recall: the lookup's input does not read")
	// ErrNoStore means a lookup was run with no knowledge store open.
	ErrNoStore = errors.New("recall: there is no knowledge store to look in")
)

// LookupOptions are the limits of the lookups.
type LookupOptions struct {
	// MaxResults is the most results one search returns.
	MaxResults int
	// MaxChars is the most characters one lookup returns.
	MaxChars int
}

// CheckLookupOptions refuses options no lookup can run with.
func CheckLookupOptions(o LookupOptions) error {
	switch {
	case o.MaxResults < 1 || o.MaxResults > 100:
		return fmt.Errorf("%w: %s must be from 1 to 100", ErrOptions, SettingLookupResults)
	case o.MaxChars < 200 || o.MaxChars > 200000:
		return fmt.Errorf("%w: %s must be from 200 to 200000", ErrOptions, SettingLookupMaxChars)
	}
	return nil
}

// Result is what one lookup returned.
type Result struct {
	// Text is what the model is given.
	Text string
	// Sensitive says the text holds content from a sensitive space. The
	// chat must then be held to models that may read it: see Hold.
	Sensitive bool
	// Withheld counts what was left out because the model may not read
	// sensitive content.
	Withheld int
}

// Lookup is one tool a model can call to look in the person's knowledge
// store. It is plain data and one function, so a tool registry needs only
// a thin wrapper to offer it.
type Lookup struct {
	// Name is what the model calls.
	Name string
	// Description tells the model what the lookup does and when to use it.
	Description string
	// Schema is the JSON Schema of the input.
	Schema json.RawMessage
	// ReadOnly is true for all three: a lookup changes nothing, in
	// Keystone or outside it.
	ReadOnly bool
	// Run does the lookup. ts is the tenant's own handle and ks the
	// tenant's opened knowledge store. input is the model's input as JSON.
	// An error that wraps ErrInput is the model's own mistake, and its
	// text can be given back to the model. Any other error is the store's.
	Run func(ctx context.Context, ts *tenant.Store, ks knowledge.Store, input json.RawMessage) (Result, error)
}

// Lookups returns the three lookups for one model. mayReadSensitive is the
// answer of Model.MayReadSensitive for the model that will read the
// results. With false, no lookup returns anything from a sensitive space:
// a search leaves such hits out and says how many, and a read of such a
// page or source says only that this model may not read it.
//
// This and Found.For are the only ways to the store's content.
func Lookups(mayReadSensitive bool, o LookupOptions) ([]Lookup, error) {
	if err := CheckLookupOptions(o); err != nil {
		return nil, err
	}
	return []Lookup{
		{
			Name:        LookupSearch,
			Description: "Search the person's own knowledge store by words. It returns the pages and sources that match, best first, each with its id. Use it before saying you do not know something about the person's world, then read a page or a source in full by its id.",
			Schema: json.RawMessage(`{"type":"object","properties":{` +
				`"query":{"type":"string","description":"The words to search for."},` +
				`"space":{"type":"string","description":"Search this space only. Leave out to search every space."},` +
				`"limit":{"type":"integer","minimum":1,"description":"The most results wanted."}},` +
				`"required":["query"],"additionalProperties":false}`),
			ReadOnly: true,
			Run: func(ctx context.Context, _ *tenant.Store, ks knowledge.Store, input json.RawMessage) (Result, error) {
				return search(ctx, ks, mayReadSensitive, o, input)
			},
		},
		{
			Name:        LookupReadPage,
			Description: "Read one page of the person's knowledge store by its page id, as a search or a recalled item gave it. A long page is returned in parts: pass from to read on.",
			Schema:      readSchema("page"),
			ReadOnly:    true,
			Run: func(ctx context.Context, _ *tenant.Store, ks knowledge.Store, input json.RawMessage) (Result, error) {
				return readPage(ctx, ks, mayReadSensitive, o, input)
			},
		},
		{
			Name:        LookupReadSource,
			Description: "Read one source of the person's knowledge store by its source id: something that was brought in and kept unchanged, such as a document or the facts noted from a chat. A long source is returned in parts: pass from to read on.",
			Schema:      readSchema("source"),
			ReadOnly:    true,
			Run: func(ctx context.Context, _ *tenant.Store, ks knowledge.Store, input json.RawMessage) (Result, error) {
				return readSource(ctx, ks, mayReadSensitive, o, input)
			},
		},
	}, nil
}

func readSchema(what string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"id":{"type":"string","description":"The ` + what + ` id."},` +
		`"from":{"type":"integer","minimum":0,"description":"The character to start at, to read on in a long ` + what + `. Leave out to start at the beginning."}},` +
		`"required":["id"],"additionalProperties":false}`)
}

// decode reads a lookup's input. A field the lookup does not know is
// refused, so a model's mistake is said and not guessed at.
func decode(input json.RawMessage, v any) error {
	if len(bytes.TrimSpace(input)) == 0 {
		input = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ErrInput, err)
	}
	return nil
}

func search(ctx context.Context, ks knowledge.Store, mayReadSensitive bool, o LookupOptions, input json.RawMessage) (Result, error) {
	var in struct {
		Query string `json:"query"`
		Space string `json:"space"`
		Limit int    `json:"limit"`
	}
	if err := decode(input, &in); err != nil {
		return Result{}, err
	}
	query := oneLine(in.Query)
	if query == "" {
		return Result{}, fmt.Errorf("%w: query is needed, and it is the words to search for", ErrInput)
	}
	if ks == nil {
		return Result{}, ErrNoStore
	}
	limit := o.MaxResults
	if in.Limit > 0 && in.Limit < limit {
		limit = in.Limit
	}
	hits, err := ks.Search(ctx, knowledge.Query{Text: query, Space: strings.TrimSpace(in.Space), Limit: limit})
	if err != nil {
		return Result{}, err
	}
	var res Result
	var lines []string
	// Each line is held to an even share of the room, so one long snippet
	// cannot push the others out.
	share := max(o.MaxChars/max(limit, 1), 80)
	for _, h := range hits {
		if len(lines) == limit {
			break
		}
		if !mayRead(h.Label, mayReadSensitive) {
			res.Withheld++
			continue
		}
		res.Sensitive = res.Sensitive || h.Label.Sensitive()
		line := fmt.Sprintf("%d. %s %s: %q in the space %q", len(lines)+1, h.Type, h.ID, name(h.Title), name(h.Space))
		if s := oneLine(h.Snippet); s != "" {
			line += ". " + s
		}
		lines = append(lines, cut(line, share))
	}
	var b strings.Builder
	switch len(lines) {
	case 0:
		fmt.Fprintf(&b, "Nothing in the person's knowledge store matches %q.", query)
	case 1:
		fmt.Fprintf(&b, "1 result for %q. Read it in full by its id.\n", query)
	default:
		fmt.Fprintf(&b, "%d results for %q, best first. Read one in full by its id.\n", len(lines), query)
	}
	b.WriteString(strings.Join(lines, "\n"))
	if res.Withheld > 0 {
		fmt.Fprintf(&b, "\n%s", withheldLine(res.Withheld))
	}
	res.Text = cut(b.String(), o.MaxChars)
	return res, nil
}

// withheldLine tells a model that results were left out, and never what
// they were.
func withheldLine(n int) string {
	if n == 1 {
		return "1 more result is from a sensitive space and was left out: this model may not read it. A local or trusted model can."
	}
	return fmt.Sprintf("%d more results are from sensitive spaces and were left out: this model may not read them. A local or trusted model can.", n)
}

// readInput reads the input the two reads share.
func readInput(input json.RawMessage, what string) (id string, from int, err error) {
	var in struct {
		ID   string `json:"id"`
		From int    `json:"from"`
	}
	if err := decode(input, &in); err != nil {
		return "", 0, err
	}
	id = strings.TrimSpace(in.ID)
	if id == "" {
		return "", 0, fmt.Errorf("%w: id is needed, and it is the %s id a search gave", ErrInput, what)
	}
	if in.From < 0 {
		return "", 0, fmt.Errorf("%w: from cannot be negative", ErrInput)
	}
	return id, in.From, nil
}

// part returns the part of a text one read gives, with a line that says
// where it stops when there is more.
func part(text string, from, maxChars int) string {
	total := utf8.RuneCountInString(strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n")))
	if from >= total && total > 0 {
		return fmt.Sprintf("[There is nothing from character %d on. The text is %d characters long.]", from, total)
	}
	body, more := window(text, from, maxChars)
	if more {
		next := from + utf8.RuneCountInString(body)
		body += fmt.Sprintf("\n[Cut at character %d of %d. Call again with from=%d to read on.]", next, total, next)
	}
	return body
}

// refusal is what a model that may not read sensitive content is told when
// it asks for something from a sensitive space. It says nothing of what
// the thing is, not its title and not its space.
func refusal(what, id string) Result {
	return Result{Withheld: 1, Text: fmt.Sprintf("The %s %s is in a sensitive space, and this model may not read it. A local or trusted model can.", what, id)}
}

func readPage(ctx context.Context, ks knowledge.Store, mayReadSensitive bool, o LookupOptions, input json.RawMessage) (Result, error) {
	id, from, err := readInput(input, "page")
	if err != nil {
		return Result{}, err
	}
	if ks == nil {
		return Result{}, ErrNoStore
	}
	p, err := ks.ReadPage(ctx, id)
	if errors.Is(err, knowledge.ErrNotFound) {
		return Result{Text: fmt.Sprintf("There is no page with the id %s. Search for it, and use the id a result gives.", id)}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if !mayRead(p.Label, mayReadSensitive) {
		return refusal("page", id), nil
	}
	head := pageHeading(p)
	if len(p.Sources) > 0 {
		head += "\nIt cites these sources: " + strings.Join(p.Sources, ", ") + "."
	}
	return Result{Sensitive: p.Label.Sensitive(), Text: head + "\n" + part(p.Body, from, o.MaxChars)}, nil
}

func readSource(ctx context.Context, ks knowledge.Store, mayReadSensitive bool, o LookupOptions, input json.RawMessage) (Result, error) {
	id, from, err := readInput(input, "source")
	if err != nil {
		return Result{}, err
	}
	if ks == nil {
		return Result{}, ErrNoStore
	}
	s, err := ks.ReadSource(ctx, id)
	if errors.Is(err, knowledge.ErrNotFound) {
		return Result{Text: fmt.Sprintf("There is no source with the id %s. Search for it, and use the id a result gives.", id)}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if !mayRead(s.Label, mayReadSensitive) {
		return refusal("source", id), nil
	}
	head := sourceHeading(s)
	if origin := name(s.Origin); origin != "" {
		head += "\nIt came from: " + origin + "."
	}
	return Result{Sensitive: s.Label.Sensitive(), Text: head + "\n" + part(s.Text, from, o.MaxChars)}, nil
}
