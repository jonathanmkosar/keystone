package turn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
)

// The test tenant's one model. It is in no registry: the test tenant is
// given it whatever its registry holds.
const (
	TestModelName  = "recorded-turns"
	testModelLabel = "Recorded turns (test data)"
)

func (r *Runner) testModel() models.Model {
	return models.Model{
		Name: TestModelName, Label: testModelLabel, Adapter: models.AdapterFake,
		ContextWindow: r.o.Config.TestContextWindow, OutputLimit: r.o.Config.TestOutputLimit,
		Capabilities: []models.Capability{models.CapTools, models.CapThinking},
		Class:        models.ClassLocal,
	}
}

func info(m models.Model) ModelInfo {
	caps := []string{}
	for _, c := range m.Capabilities {
		caps = append(caps, string(c))
	}
	return ModelInfo{
		Name: m.Name, Label: m.Label, Class: string(m.Class), Capabilities: caps,
		ContextWindow: m.ContextWindow, OutputLimit: m.OutputLimit, Test: m.Adapter == models.AdapterFake,
	}
}

// candidates returns the tenant's models a chat turn can be sent to: the
// usable entries whose adapter this build has, without the ones that only
// embed or only decide (P14-02).
func (r *Runner) candidates(ctx context.Context, s *tenant.Store) ([]models.Model, error) {
	usable, err := models.Usable(ctx, s)
	if err != nil {
		return nil, err
	}
	var out []models.Model
	for _, m := range usable {
		if r.set[m.Adapter] != nil && !m.Has(models.CapEmbedding) && !m.Has(models.CapDecision) {
			out = append(out, m)
		}
	}
	return out, nil
}

// choice says which model a chat's next turn uses when the turn names
// none. The order: the model the chat's last turn used, the person's
// chats.default_model, the first usable model in the registry by name.
// The test tenant always gets its recorded turns.
func (r *Runner) choice(ctx context.Context, s *tenant.Store, chatID string) (ModelChoice, error) {
	if err := r.setup(); err != nil {
		return ModelChoice{}, err
	}
	test, err := testtenant.IsTest(s)
	if err != nil {
		return ModelChoice{}, err
	}
	if test {
		m := info(r.testModel())
		return r.withAuto(ctx, s, chatID, ModelChoice{Model: &m, Source: SourceTest, Models: []ModelInfo{m}}) // P14-04 (auto.go)
	}
	can, err := r.candidates(ctx, s)
	if err != nil {
		return ModelChoice{}, err
	}
	out := ModelChoice{Source: SourceNone, Models: []ModelInfo{}}
	byName := map[string]ModelInfo{}
	for _, m := range can {
		out.Models = append(out.Models, info(m))
		byName[m.Name] = info(m)
	}
	if len(can) == 0 {
		out.Reason = "This person's model registry has no model that this build of Keystone can call."
		return r.withAuto(ctx, s, chatID, out) // P14-04 (auto.go)
	}
	last, err := lastModel(ctx, s, chatID)
	if err != nil {
		return ModelChoice{}, err
	}
	pick := func(name, source string) bool {
		m, ok := byName[name]
		if ok {
			out.Model, out.Source = &m, source
		}
		return ok
	}
	if !pick(last, SourceChat) && !pick(r.setting(ctx, s, KeyDefaultModel), SourceDefault) {
		pick(can[0].Name, SourceFirst)
	}
	return r.withAuto(ctx, s, chatID, out) // P14-04 (auto.go)
}

// open returns the model a turn runs on and a provider for it, behind the
// budget's guard. The test tenant gets the fake provider with its recorded
// turns, whatever the turn named and whatever its registry holds. secrets
// are the values the provider was built with, for the audit log to remove.
func (r *Runner) open(ctx context.Context, s *tenant.Store, t row, named string) (models.Model, seam.Provider, []string, *TurnError) {
	broke := func(what string, err error) (models.Model, seam.Provider, []string, *TurnError) {
		r.logf("turns: tenant %s: %s: %v", s.TenantID(), what, err)
		return models.Model{}, nil, nil, &TurnError{Code: CodeFailed, Message: "The turn failed inside Keystone. The reason is in the server's log."}
	}
	if err := r.setup(); err != nil {
		return broke("setting up the runner", err)
	}
	test, err := testtenant.IsTest(s)
	if err != nil {
		return broke("reading the test tenant's mark", err)
	}
	if test {
		m := r.testModel()
		return m, budget.Guard(m, r.test, r.o.Config.Budget), nil, nil
	}
	name := named
	if name == "" {
		c, err := r.choice(ctx, s, t.ChatID)
		if err != nil {
			return broke("choosing a model", err)
		}
		if c.Model == nil {
			return models.Model{}, nil, nil, &TurnError{Code: CodeNoModel, Message: c.Reason}
		}
		name = c.Model.Name
	}
	return r.routedChat(ctx, s, t, name)
}

// openNamed opens one model of the tenant's registry by name.
func (r *Runner) openNamed(ctx context.Context, s *tenant.Store, name string) (models.Model, seam.Provider, []string, *TurnError) {
	m, p, err := adapters.Open(ctx, s, r.set, name)
	if err != nil {
		why := "The model " + name + " cannot be used: its entry in the registry is refused."
		switch {
		case errors.Is(err, models.ErrUnknown):
			why = "There is no model named " + name + " in this person's registry."
		case errors.Is(err, adapters.ErrNotBuilt):
			why = "The model " + name + " uses an adapter this build of Keystone does not have."
		case errors.Is(err, adapters.ErrNoKey):
			why = "The model " + name + " needs a key that is not in this person's vault."
		}
		r.logf("turns: tenant %s: opening model %s: %v", s.TenantID(), name, err)
		return models.Model{}, nil, nil, &TurnError{Code: CodeModelUnavailable, Message: why}
	}
	var secrets []string
	if m.Key != "" {
		if v, ok, err := s.Secrets().Get(m.Key); err == nil && ok && v != "" {
			secrets = append(secrets, v)
		}
	}
	return m, budget.Guard(m, p, r.o.Config.Budget), secrets, nil
}

// title gives a chat its automatic title after a reply, while it has none,
// and returns the title when it set one. A model the settings name writes
// it. The fake provider writes none: its title is the first words of the
// person's message, and so is the title when the model's call fails.
func (r *Runner) title(ctx context.Context, s *tenant.Store, t row, m models.Model, p seam.Provider, secrets []string, reply string) (string, bool) {
	if ctx.Err() != nil || r.setting(ctx, s, KeyTitleAuto) != "true" {
		return "", false
	}
	c, err := chat.Get(s, t.ChatID)
	if err != nil || c.Title != "" {
		return "", false
	}
	first, err := chat.GetMessage(s, t.ChatID, t.UserMessageID)
	if err != nil {
		return "", false
	}
	asked := seam.Text(seam.Message{Role: first.Role, Blocks: first.Blocks})
	lim := r.o.Config.Limits.TitleLength

	title := ""
	if m.Adapter != models.AdapterFake {
		if name := r.setting(ctx, s, KeyTitleModel); name != "" && name != m.Name {
			if tm, tp, ts, failure := r.routedTitle(ctx, s, t, m.Name); failure == nil {
				m, p, secrets = tm, tp, ts
			}
		}
		if m.Adapter != models.AdapterFake {
			title = tidyTitle(r.askForTitle(ctx, s, t, m, p, secrets, asked, reply), lim)
		}
	}
	if title == "" {
		title = tidyTitle(strings.Join(firstWords(asked, r.o.Config.TitleDerivedWords), " "), lim)
	}
	if title == "" {
		return "", false
	}
	// The person may have named the chat while the reply streamed.
	if c, err := chat.Get(s, t.ChatID); err != nil || c.Title != "" {
		return "", false
	}
	renamed, err := chat.Rename(s, r.o.Config.Limits, t.ChatID, title)
	if err != nil {
		r.logf("turns: %s giving chat %s its title: %v", t.requestID, t.ChatID, err)
		return "", false
	}
	return renamed.Title, true
}

// askForTitle calls the title model. It is a model call like any other:
// checked against the window, and recorded before and after.
func (r *Runner) askForTitle(ctx context.Context, s *tenant.Store, t row, m models.Model, p seam.Provider, secrets []string, asked, reply string) string {
	ctx, cancel := context.WithTimeout(ctx, r.o.Config.TitleTimeout)
	defer cancel()
	short := func(text string) string {
		if n := r.o.Config.TitleInputChars; utf8.RuneCountInString(text) > n {
			return string([]rune(text)[:n])
		}
		return text
	}
	req := seam.Request{
		Model:           m.Name,
		System:          []seam.Block{{Type: seam.BlockText, Text: r.o.Config.TitlePrompt}},
		Messages:        []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: fmt.Sprintf("The person wrote:\n%s\n\nThe reply began:\n%s", short(asked), short(reply))}}}},
		MaxOutputTokens: r.o.Config.TitleMaxTokens,
	}
	out := r.call(ctx, s, t, 0, m, p, secrets, req, func(string, any) {})
	if out.err != nil {
		if out.failure != nil {
			r.logf("turns: %s the title of chat %s: %s", t.requestID, t.ChatID, audit.Scrub(out.failure.Message, secrets...))
		}
		return ""
	}
	return seam.Text(out.resp.Message)
}

// firstWords returns up to n words from the start of text.
func firstWords(text string, n int) []string {
	words := strings.Fields(text)
	if len(words) > n {
		words = words[:n]
	}
	return words
}

// tidyTitle makes a model's answer, or a person's first words, a title:
// its first line, on one line, without the quotation marks or the full
// stop a model adds, and no longer than a title may be.
func tidyTitle(text string, most int) string {
	text = strings.TrimSpace(text)
	if line, _, found := strings.Cut(text, "\n"); found {
		text = line
	}
	text = strings.Join(strings.Fields(text), " ")
	text = strings.Trim(text, "\"'`*#“”‘’ ")
	text = strings.TrimRight(text, ".,;:")
	if utf8.RuneCountInString(text) > most {
		text = strings.TrimSpace(string([]rune(text)[:most]))
	}
	return text
}
