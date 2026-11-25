// Package memory is the memory pass (P6-06): after a turn ends, a model
// reads the exchange, the person's message and the reply, and the durable
// facts it finds are added to the person's knowledge store as one source
// that points back to the chat.
//
// This package holds the parts that decide: the request that asks for the
// facts, how the answer is read, the source the facts become, where they
// may land, the queue of passes kept in the tenant's database, the
// incognito flag on a chat, and the memory pass's own spend reserve. The
// turn (internal/turn/memory.go) reads the settings, reads the chat record,
// routes and makes the model call, and passes plain values in.
//
// It is a guard of fixed rule 3, content from a sensitive space reaches
// only models the registry marks local or trusted: facts drawn from a chat
// that was sent sensitive content land only in a space the store says is
// sensitive, or nowhere, and only a model that may read such content may
// draw them. So it reads no setting, and it does not import the chat
// record, which does. docs/architecture/memory-pass.md describes it.
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The settings the memory pass reads, in settings/knowledge.json. The
// turn reads them and passes the values in.
const (
	SettingOn              = "knowledge.memory"
	SettingSpace           = "knowledge.memory_space"
	SettingSensitiveSpace  = "knowledge.memory_sensitive_space"
	SettingReserve         = "knowledge.memory_reserve_usd"
	SettingPrompt          = "knowledge.memory_prompt"
	SettingSourceName      = "knowledge.memory_source_name"
	SettingMaxFacts        = "knowledge.memory_max_facts"
	SettingFactMaxChars    = "knowledge.memory_fact_max_chars"
	SettingMessageMaxChars = "knowledge.memory_message_max_chars"
	SettingMaxOutputTokens = "knowledge.memory_max_output_tokens"
	SettingTimeout         = "knowledge.memory_timeout"
)

// Kind is the kind of task a memory pass's model call is recorded under,
// in the usage records, and the task routing plans it for.
const Kind = string(routing.KindMemory)

// The errors the package answers with. Test with errors.Is.
var (
	// ErrOptions says the settings cannot be used. The message names the
	// setting.
	ErrOptions = errors.New("memory: the settings cannot be used")
	// ErrAnswer says the model's answer is not the facts that were asked
	// for: no JSON object, or no list of facts in it.
	ErrAnswer = errors.New("memory: the answer is not the facts asked for")
	// ErrNowhere says no space may take the facts of the exchange: it may
	// hold sensitive content, and no space the store says is sensitive was
	// set for such facts. They are kept nowhere.
	ErrNowhere = errors.New("memory: no space may take these facts")
	// ErrSensitive says facts drawn from sensitive content were read by a
	// model that may not read it. They are kept nowhere.
	ErrSensitive = errors.New("memory: facts from sensitive content were read by a model that may not read it")
	// ErrLanded says the store put facts drawn from sensitive content in a
	// space that is not sensitive. It is a store changing a label under
	// the pass, and is written in the server's log.
	ErrLanded = errors.New("memory: the facts landed in a space that is not sensitive")
)

// Options are the memory pass's settings, as the turn read them.
type Options struct {
	// Space is where facts from an ordinary chat go: knowledge.memory_space.
	// A space the store does not hold yet is made, as a normal space.
	Space string
	// SensitiveSpace is where facts from a chat that was sent sensitive
	// content go: knowledge.memory_sensitive_space. The store must already
	// hold it, with a page in it, and say it is sensitive. Empty means such
	// facts are kept nowhere.
	SensitiveSpace string
	// Prompt is the instruction to the model: knowledge.memory_prompt.
	// {max} becomes MaxFacts.
	Prompt string
	// SourceName names the source: knowledge.memory_source_name. {title}
	// becomes the chat's title and {date} the day of the turn.
	SourceName string
	// MaxFacts is the most facts one pass keeps.
	MaxFacts int
	// FactMaxChars is the longest fact kept. A longer one is left out.
	FactMaxChars int
	// MessageMaxChars is the most of the person's message, and of the
	// reply, the model is shown.
	MessageMaxChars int
	// MaxOutputTokens is the longest answer the model may write.
	MaxOutputTokens int
}

// The bounds CheckOptions holds the numbers to.
const (
	maxFactsMost        = 100
	factMaxCharsLeast   = 20
	factMaxCharsMost    = 2000
	messageCharsLeast   = 200
	messageCharsMost    = 100000
	outputTokensLeast   = 64
	outputTokensMost    = 32000
	sourceTitleMaxChars = 120
)

// CheckOptions refuses settings no pass can run with, with ErrOptions and
// the setting's name.
func CheckOptions(o Options) error {
	bad := func(key, why string) error { return fmt.Errorf("%w: %s %s", ErrOptions, key, why) }
	switch {
	case strings.TrimSpace(o.Space) == "":
		return bad(SettingSpace, "is empty, and facts need a space to go to")
	case strings.TrimSpace(o.Prompt) == "":
		return bad(SettingPrompt, "is empty")
	case strings.TrimSpace(o.SourceName) == "":
		return bad(SettingSourceName, "is empty")
	case o.MaxFacts < 1 || o.MaxFacts > maxFactsMost:
		return bad(SettingMaxFacts, fmt.Sprintf("is %d, and it is from 1 to %d", o.MaxFacts, maxFactsMost))
	case o.FactMaxChars < factMaxCharsLeast || o.FactMaxChars > factMaxCharsMost:
		return bad(SettingFactMaxChars, fmt.Sprintf("is %d, and it is from %d to %d", o.FactMaxChars, factMaxCharsLeast, factMaxCharsMost))
	case o.MessageMaxChars < messageCharsLeast || o.MessageMaxChars > messageCharsMost:
		return bad(SettingMessageMaxChars, fmt.Sprintf("is %d, and it is from %d to %d", o.MessageMaxChars, messageCharsLeast, messageCharsMost))
	case o.MaxOutputTokens < outputTokensLeast || o.MaxOutputTokens > outputTokensMost:
		return bad(SettingMaxOutputTokens, fmt.Sprintf("is %d, and it is from %d to %d", o.MaxOutputTokens, outputTokensLeast, outputTokensMost))
	}
	return nil
}

// Exchange is one turn as the memory pass reads it: the person's message
// and the reply, as plain text.
type Exchange struct {
	ChatID string
	TurnID string
	// Title is the chat's title, empty when it has none.
	Title string
	// At is when the turn was sent.
	At time.Time
	// Person is the text of the person's message.
	Person string
	// Reply is the text of the reply the turn ended with.
	Reply string
}

// Empty reports whether there is nothing to read.
func (x Exchange) Empty() bool {
	return strings.TrimSpace(x.Person) == "" && strings.TrimSpace(x.Reply) == ""
}

func (x Exchange) day() string { return x.At.UTC().Format("2006-01-02") }

func (x Exchange) title() string {
	if t := strings.Join(strings.Fields(x.Title), " "); t != "" {
		return t
	}
	return "untitled"
}

// Request is the model call that asks for the facts of one exchange: the
// prompt as the system text, and one message holding the exchange.
func Request(o Options, x Exchange) seam.Request {
	system := strings.ReplaceAll(o.Prompt, "{max}", strconv.Itoa(o.MaxFacts))
	var b strings.Builder
	b.WriteString("The exchange is from the chat \"" + x.title() + "\", on " + x.day() + ".\n\n")
	b.WriteString("The person wrote:\n" + said(x.Person, o.MessageMaxChars) + "\n\n")
	b.WriteString("The assistant answered:\n" + said(x.Reply, o.MessageMaxChars))
	return seam.Request{
		System:          []seam.Block{{Type: seam.BlockText, Text: system}},
		Messages:        []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: b.String()}}}},
		MaxOutputTokens: o.MaxOutputTokens,
	}
}

// said is one side of the exchange as the model is shown it: cut at a
// word's end to max characters, and saying so.
func said(text string, max int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "(nothing)"
	}
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	r := []rune(text)[:max]
	cut := string(r)
	if i := strings.LastIndexAny(cut, " \n\t"); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut) + " [The rest is cut.]"
}

// Read is what the facts of an answer came to.
type Read struct {
	// Facts are the facts kept, in the order the model gave them.
	Facts []knowledge.Fact
	// Dropped counts the facts left out: blank, longer than FactMaxChars,
	// said twice, or past MaxFacts.
	Dropped int
}

// Texts returns the text of each fact kept.
func (r Read) Texts() []string {
	out := make([]string, len(r.Facts))
	for i, f := range r.Facts {
		out[i] = f.Text
	}
	return out
}

// Facts reads the model's answer: one JSON object with a list of facts,
// {"facts": ["...", "..."]}, each fact a string or an object with a text.
// Words around the object, such as a fenced block, are passed over. An
// answer with no such object, or whose list is not one, is ErrAnswer. An
// empty list is an answer: the exchange held nothing durable.
func Facts(resp seam.Response, o Options) (Read, error) {
	text := strings.TrimSpace(seam.Text(resp.Message))
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return Read{}, fmt.Errorf("%w: it holds no JSON object", ErrAnswer)
	}
	var raw struct {
		Facts *[]json.RawMessage `json:"facts"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err != nil {
		return Read{}, fmt.Errorf("%w: its JSON does not read", ErrAnswer)
	}
	if raw.Facts == nil {
		return Read{}, fmt.Errorf("%w: its object has no list of facts", ErrAnswer)
	}
	var out Read
	seen := map[string]bool{}
	for i, item := range *raw.Facts {
		var fact string
		if err := json.Unmarshal(item, &fact); err != nil {
			var obj struct {
				Text *string `json:"text"`
			}
			if err := json.Unmarshal(item, &obj); err != nil || obj.Text == nil {
				return Read{}, fmt.Errorf("%w: fact %d is neither a text nor an object with a text", ErrAnswer, i+1)
			}
			fact = *obj.Text
		}
		fact = strings.Join(strings.Fields(fact), " ")
		key := strings.ToLower(fact)
		switch {
		case fact == "", utf8.RuneCountInString(fact) > o.FactMaxChars, seen[key], len(out.Facts) >= o.MaxFacts:
			out.Dropped++
			continue
		}
		seen[key] = true
		out.Facts = append(out.Facts, knowledge.Fact{Text: fact})
	}
	return out, nil
}

// originPrefix starts the origin of every source the memory pass adds.
const originPrefix = "keystone://chats/"

// Origin is where a source of facts came from: the chat and the turn, in
// the shape of the API's path to the turn.
func Origin(chatID, turnID string) string {
	return originPrefix + chatID + "/turns/" + turnID
}

// ParseOrigin returns the chat and the turn an origin made by Origin
// points to. ok is false for any other origin.
func ParseOrigin(origin string) (chatID, turnID string, ok bool) {
	rest, found := strings.CutPrefix(origin, originPrefix)
	if !found {
		return "", "", false
	}
	chatID, turnID, found = strings.Cut(rest, "/turns/")
	if !found || chatID == "" || turnID == "" || strings.Contains(chatID, "/") || strings.Contains(turnID, "/") {
		return "", "", false
	}
	return chatID, turnID, true
}

// SourceName is the name of the source an exchange's facts become.
func SourceName(o Options, x Exchange) string {
	title := x.title()
	if r := []rune(title); len(r) > sourceTitleMaxChars {
		title = strings.TrimSpace(string(r[:sourceTitleMaxChars])) + "..."
	}
	return strings.NewReplacer("{title}", title, "{date}", x.day()).Replace(o.SourceName)
}

// Target says which space the facts of an exchange may go to, before any
// model reads it. sensitive is whether the exchange may hold content from
// a sensitive space, which it may when the chat was sent some.
//
// Facts from an ordinary exchange go to the memory space. Facts from one
// that may be sensitive go only to SensitiveSpace, and only when the
// store's catalog says that space is sensitive: it lists pages in it, and
// every one is labelled sensitive. Otherwise the answer is ErrNowhere and
// no model should be asked. That is fixed rule 3: a fact drawn from
// sensitive content is sensitive content.
func Target(ctx context.Context, ks knowledge.Store, sensitive bool, o Options) (string, error) {
	if !sensitive {
		return o.Space, nil
	}
	space := strings.TrimSpace(o.SensitiveSpace)
	if space == "" {
		return "", fmt.Errorf("%w: no sensitive space is set for the facts of a chat that was sent sensitive content", ErrNowhere)
	}
	if ks == nil {
		return "", fmt.Errorf("%w: there is no store to ask about the space %q", ErrNowhere, space)
	}
	entries, err := ks.Catalog(ctx)
	if err != nil {
		return "", err
	}
	pages, labelled := 0, 0
	for _, e := range entries {
		if e.Space != space {
			continue
		}
		pages++
		if e.Label.Sensitive() {
			labelled++
		}
	}
	if pages == 0 || labelled != pages {
		return "", fmt.Errorf("%w: the store does not say the space %q is sensitive", ErrNowhere, space)
	}
	return space, nil
}

// Add adds the facts of one exchange to the store as one source, in the
// space Target gave. sensitive is whether the exchange may hold content
// from a sensitive space, and mayRead is Model.MayReadSensitive for the
// model that read it. Facts drawn from sensitive content by a model that
// may not read it are refused with ErrSensitive and never added. A store
// that answers that such facts landed in a space that is not sensitive
// gives the source and ErrLanded.
func Add(ctx context.Context, ks knowledge.Store, o Options, x Exchange, facts []knowledge.Fact, space string, sensitive, mayRead bool) (knowledge.Source, error) {
	if sensitive && !mayRead {
		return knowledge.Source{}, ErrSensitive
	}
	if ks == nil || !ks.Capabilities().AddSource {
		return knowledge.Source{}, knowledge.ErrNotSupported
	}
	src, err := ks.AddSource(ctx, knowledge.NewSource{
		Space: space, Name: SourceName(o, x), Origin: Origin(x.ChatID, x.TurnID), Dated: x.day(), Facts: facts,
	})
	if err != nil {
		return knowledge.Source{}, err
	}
	if sensitive && !src.Label.Sensitive() {
		return src, fmt.Errorf("%w: source %s in the space %q", ErrLanded, src.ID, src.Space)
	}
	return src, nil
}
