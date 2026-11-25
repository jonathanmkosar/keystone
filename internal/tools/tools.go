// Package tools is the registry of the tools an agent may use, and the one
// place a tool is run.
//
// A tool is plain data and a function: a name, a description, a JSON
// schema for its input, a flag that says whether it only reads, and the
// function that runs it for one tenant. A registry is made with New and
// filled with Add, by plain calls from wherever the server is put
// together. Nothing registers itself, so a test makes its own registry and
// leaves no trace.
//
// Run is the only way a tool runs. It records the call in the tenant's
// audit log before and after (the fourth fixed rule), and it asks
// approval.Check before the tool's function is called (the second fixed
// rule). The package reads no setting: what a caller may vary comes in the
// Call. The fixed rules check in verify fails if this package, or
// anything it imports, could read one.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools/approval"
)

// Input is what a tool's function is given.
type Input struct {
	// Store is the store of the tenant the turn belongs to, and of no
	// other. A tool reaches a person's data through it and nothing else.
	Store *tenant.Store
	// Args is what the model sent: a JSON object.
	Args json.RawMessage
	// ChatID is the chat the turn runs in.
	ChatID string
}

// Tool is one tool.
type Tool struct {
	// Name is what the model calls it by: lower-case letters, digits and
	// underscores, starting with a letter.
	Name string
	// Description tells the model what the tool does and when to use it.
	Description string
	// InputSchema is the JSON Schema of the arguments, an object.
	InputSchema json.RawMessage
	// ReadOnly says the tool only reads. A tool that is not read-only
	// changes something, inside Keystone or outside it, and runs only with
	// a person's approval.
	ReadOnly bool
	// Inside says the tool's result holds only what Keystone itself keeps
	// for the person: their chats, the clock, a page of their knowledge
	// store. A tool that does not say so is taken to bring content from
	// outside Keystone (a web page, a file, an email, a connector's
	// answer), and once a turn has read such content every tool that
	// changes something asks before it runs (P7-02). A forgotten flag
	// makes side effects ask; it never lets one run.
	Inside bool
	// Describe says what one call will do, in a sentence a person reads on
	// the approval card, from the call's input. Nil means a sentence made
	// from the tool's name and the input's plain values.
	Describe func(input json.RawMessage) string
	// Run does the work and returns the text the model reads. Its context
	// is cancelled when the turn is stopped or the tool has run too long,
	// and Run must return soon after. An error becomes an error result
	// with the error's text, so it is written for the model to read.
	Run func(ctx context.Context, in Input) (string, error)
}

// Info is a tool without its function, for a list.
type Info struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ReadOnly    bool   `json:"read_only"`
	// Inside says the tool's result is Keystone's own, not outside content.
	Inside bool `json:"inside"`
}

var nameShape = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Registry holds tools by name. The zero value is not ready: use New. A
// nil registry holds no tools, offers none and runs none.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// New returns an empty registry.
func New() *Registry { return &Registry{tools: map[string]Tool{}} }

// Add registers a tool. It refuses a name that is not a tool's name, a
// name already taken, a schema that is not a JSON object, and a tool with
// no description or no function.
func (r *Registry) Add(t Tool) error {
	switch {
	case r == nil:
		return errors.New("tools: there is no registry to add to")
	case !nameShape.MatchString(t.Name):
		return fmt.Errorf("tools: %q is not a tool's name: lower-case letters, digits and underscores, starting with a letter", t.Name)
	case strings.TrimSpace(t.Description) == "":
		return fmt.Errorf("tools: %s has no description, and a model cannot use a tool it is told nothing about", t.Name)
	case !isObject(t.InputSchema):
		return fmt.Errorf("tools: the input schema of %s is not a JSON object", t.Name)
	case t.Run == nil:
		return fmt.Errorf("tools: %s has no function to run", t.Name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, taken := r.tools[t.Name]; taken {
		return fmt.Errorf("tools: a tool named %s is already registered", t.Name)
	}
	r.tools[t.Name] = t
	return nil
}

func (r *Registry) names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// List returns every tool, by name.
func (r *Registry) List() []Info {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Info
	for _, name := range r.names() {
		t := r.tools[name]
		out = append(out, Info{Name: t.Name, Description: t.Description, ReadOnly: t.ReadOnly, Inside: t.Inside})
	}
	return out
}

// Get returns one tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	if r == nil {
		return Tool{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Summary says what one call of t will do, for the approval card: the
// tool's own Describe, or its name and the plain values of its input.
func Summary(t Tool, input json.RawMessage) string {
	if t.Describe != nil {
		if s := strings.TrimSpace(t.Describe(input)); s != "" {
			return s
		}
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(input, &args) != nil || len(args) == 0 {
		return fmt.Sprintf("Use %s.", t.Name)
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		var v any
		if json.Unmarshal(args[k], &v) != nil {
			continue
		}
		switch x := v.(type) {
		case string:
			if strings.TrimSpace(x) != "" {
				parts = append(parts, fmt.Sprintf("%s “%s”", strings.ReplaceAll(k, "_", " "), clip(strings.Join(strings.Fields(x), " "), 120)))
			}
		case float64, bool:
			parts = append(parts, fmt.Sprintf("%s %v", strings.ReplaceAll(k, "_", " "), x))
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("Use %s.", t.Name)
	}
	s := fmt.Sprintf("Use %s with %s", t.Name, strings.Join(parts, ", "))
	if strings.HasSuffix(s, ".”") || strings.HasSuffix(s, "?”") || strings.HasSuffix(s, "!”") {
		return s // a quoted sentence already ends it
	}
	return s + "."
}

// Definitions returns the tools as a model is offered them, by name, so
// the same registry always gives the same start to a call.
func (r *Registry) Definitions() []seam.Tool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []seam.Tool
	for _, name := range r.names() {
		t := r.tools[name]
		out = append(out, seam.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return out
}

// Call is one tool call a model made, and what it is run with.
type Call struct {
	// Store is the tenant's store. The audit entries are written to it,
	// and the tool is given it.
	Store *tenant.Store
	// RequestID is the id of the API call that set the turn off. It ties
	// the tool call's audit entries to the turn's model calls.
	RequestID string
	// Actor is who the turn runs for, as the audit log names them.
	Actor string
	// ChatID is the chat the turn runs in.
	ChatID string

	// ID, Name and Input are the model's tool call.
	ID    string
	Name  string
	Input json.RawMessage

	// Approval is a person's agreement to this call: their answer to it,
	// or their standing policy for the tool (P7-02). The zero value says
	// there is none. A tool that changes something runs only with one that
	// covers this tool and this input.
	Approval approval.Approval

	// Timeout is how long the tool may run. Zero means as long as its
	// context lasts.
	Timeout time.Duration
	// MaxResultChars is the most characters of a result that are kept.
	// Zero means all of them.
	MaxResultChars int
}

// Status says how a tool call went.
type Status string

const (
	// StatusOK is a tool that ran and gave its result.
	StatusOK Status = "ok"
	// StatusError is a tool that failed, panicked, ran too long, was given
	// input it could not read, or is not in the registry.
	StatusError Status = "error"
	// StatusRefused is a tool that was not run because the approval rule
	// did not let it.
	StatusRefused Status = "refused"
	// StatusStopped is a tool whose context ended while it ran: the turn
	// was stopped.
	StatusStopped Status = "stopped"
)

// Result is what a tool call came to.
type Result struct {
	// Block is the tool_result block that answers the call. It is always
	// one a provider takes, whatever happened.
	Block  seam.Block
	Status Status
	// Ran says the tool's function was called.
	Ran bool
	// Log is for the server's own log and never for the model: what a
	// tool that broke said as it did. Empty otherwise.
	Log string
}

// What a model reads when a tool did not give a result of its own.
const (
	textStopped   = "The turn was stopped while this tool was running, so it has no result."
	textNothing   = "The tool ran and returned nothing."
	textPanicked  = "The tool broke while it ran, and has no result. The reason is in the server's log."
	textUnrecord  = "The tool call could not be recorded, so it was not run."
	textUnrecord2 = "The tool ran, and its result could not be recorded, so the result was not used."
)

// Decline answers a call without running anything, and records that it
// did: the turn is at its step limit, or tools are switched off. why is
// what the model reads. The audit log gets the same two entries a call
// that ran gets, the second with the outcome refused.
func (r *Registry) Decline(c Call, why string) Result {
	res := Result{Block: failed(c.ID, why), Status: StatusRefused}
	return record(c, func() Result { return res })
}

func failed(id, text string) seam.Block {
	return seam.Block{Type: seam.BlockToolResult, ID: id, IsError: true, Content: []seam.Block{{Type: seam.BlockText, Text: text}}}
}

// Run runs one tool call and returns its result. It never fails: whatever
// goes wrong becomes an error result the model can read.
//
// The audit log gets an entry before the tool runs and an entry for its
// end, of the kind tool, under the call's request id and with the tool's
// name as the action. A call whose first entry cannot be written is not
// run. A result whose entry cannot be written is not used.
//
// A tool that changes something is refused unless the call carries an
// approval. A tool that fails, panics or runs past its timeout gives an
// error result. When ctx ends while the tool runs, Run returns at once
// with the status stopped, and the tool's function is left to notice its
// own context.
func (r *Registry) Run(ctx context.Context, c Call) Result {
	if len(c.Input) == 0 {
		c.Input = json.RawMessage(`{}`)
	}
	return record(c, func() Result { return r.run(ctx, c, c.Input) })
}

// record writes a call's two audit entries around do, which is the call.
func record(c Call, do func() Result) Result {
	bg := context.Background()
	input := c.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	name := c.Name
	if strings.TrimSpace(name) == "" {
		name = "(no name)"
	}
	entry := audit.Entry{Kind: audit.KindTool, RequestID: c.RequestID, Actor: c.Actor, Action: name, Outcome: audit.OutcomeStarted, Sent: string(input)}
	if _, err := audit.Append(bg, c.Store, entry); err != nil {
		return Result{Block: failed(c.ID, textUnrecord), Status: StatusError}
	}
	began := time.Now()
	res := do()
	entry.Sent, entry.DurationMS = "", time.Since(began).Milliseconds()
	switch res.Status {
	case StatusOK:
		entry.Outcome = audit.OutcomeOK
	case StatusRefused:
		entry.Outcome = audit.OutcomeRefused
	case StatusStopped:
		// As a stopped model call is recorded: it did what was asked.
		entry.Outcome = audit.OutcomeOK
	default:
		entry.Outcome = audit.OutcomeError
	}
	received, _ := json.Marshal(struct {
		Status Status     `json:"status"`
		Result seam.Block `json:"result"`
	}{res.Status, res.Block})
	entry.Received = string(received)
	if _, err := audit.Append(bg, c.Store, entry); err != nil {
		return Result{Block: failed(c.ID, textUnrecord2), Status: StatusError, Ran: res.Ran}
	}
	return res
}

// run is Run without the audit log.
func (r *Registry) run(ctx context.Context, c Call, input json.RawMessage) Result {
	var t Tool
	known := false
	if r != nil {
		r.mu.RLock()
		t, known = r.tools[c.Name]
		r.mu.RUnlock()
	}
	if !known {
		return Result{Block: failed(c.ID, fmt.Sprintf("There is no tool named %q. Use only the tools you were offered.", c.Name)), Status: StatusError}
	}
	// The second fixed rule. Nothing below this line runs for a tool that
	// changes something unless a person approved this call: an approval
	// covers one tool with one input (P7-02).
	if err := approval.CheckCall(t.ReadOnly, c.Approval, c.Name, input); err != nil {
		return Result{Block: failed(c.ID, upper(err.Error())), Status: StatusRefused}
	}
	if !isObject(input) {
		return Result{Block: failed(c.ID, "The tool's input is not a JSON object, so it was not run."), Status: StatusError}
	}
	if ctx.Err() != nil {
		return Result{Block: failed(c.ID, "The turn was stopped before this tool ran."), Status: StatusStopped}
	}

	runCtx, cancel := ctx, context.CancelFunc(func() {})
	if c.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, c.Timeout)
	}
	defer cancel()
	type outcome struct {
		text     string
		err      error
		panicked bool
	}
	// The tool runs in a goroutine of its own, so a tool that does not
	// watch its context cannot hold a stopped turn open.
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- outcome{panicked: true, err: fmt.Errorf("%v", p)}
			}
		}()
		text, err := t.Run(runCtx, Input{Store: c.Store, Args: input, ChatID: c.ChatID})
		done <- outcome{text: text, err: err}
	}()

	var out outcome
	select {
	case out = <-done:
	case <-runCtx.Done():
		if ctx.Err() != nil {
			return Result{Block: failed(c.ID, textStopped), Status: StatusStopped, Ran: true}
		}
		return Result{Block: failed(c.ID, fmt.Sprintf("The tool ran for longer than %s and was stopped, so it has no result.", c.Timeout)), Status: StatusError, Ran: true}
	}
	switch {
	case out.panicked:
		return Result{Block: failed(c.ID, textPanicked), Status: StatusError, Ran: true, Log: fmt.Sprintf("the tool %s panicked: %v", c.Name, out.err)}
	case out.err != nil && ctx.Err() != nil:
		return Result{Block: failed(c.ID, textStopped), Status: StatusStopped, Ran: true}
	case out.err != nil:
		return Result{Block: failed(c.ID, cut(upper(out.err.Error()), c.MaxResultChars)), Status: StatusError, Ran: true}
	}
	text := out.text
	if strings.TrimSpace(text) == "" {
		text = textNothing
	}
	return Result{
		Block:  seam.Block{Type: seam.BlockToolResult, ID: c.ID, Content: []seam.Block{{Type: seam.BlockText, Text: cut(text, c.MaxResultChars)}}},
		Status: StatusOK, Ran: true,
	}
}

// cut keeps the first most characters of a result, and says so when it
// left some out.
func cut(text string, most int) string {
	if most <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= most {
		return text
	}
	return string(runes[:most]) + fmt.Sprintf("\n\n[The result was cut here. It had %d characters, and %d are kept.]", len(runes), most)
}

// clip keeps the first most characters of a short text, with an ellipsis
// when it left some out.
func clip(text string, most int) string {
	runes := []rune(text)
	if len(runes) <= most {
		return text
	}
	return strings.TrimSpace(string(runes[:most-1])) + "…"
}

// upper makes an error's text a sentence: a capital first, a full stop last.
func upper(s string) string {
	if s == "" {
		return s
	}
	if !strings.ContainsAny(s[len(s)-1:], ".!?") {
		s += "."
	}
	if s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func isObject(raw json.RawMessage) bool {
	var v map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &v) == nil && v != nil
}
