package testtenant

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The kinds of use case. An item that builds something the test tenant
// should prove adds a kind here: a field or two on Case and Expect, a line
// in checkCase and a case in runCase.
const (
	// KindTurn sends one message to the test tenant's model.
	KindTurn = "turn"
	// KindSearch searches the test tenant's chats.
	KindSearch = "search"
	// KindChat reads one loaded chat's current branch.
	KindChat = "chat"
)

// checkCase refuses a use case that could not be run or that expects
// nothing.
func checkCase(c Case) error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("a use case has no name")
	}
	e := c.Expect
	var needs, expects bool
	switch c.Kind {
	case KindTurn:
		needs = c.Ask != ""
		expects = e.StopReason != "" || len(e.TextContains) > 0 || len(e.ToolCalls) > 0
	case KindSearch:
		needs = c.Query != ""
		expects = e.Titles != nil
	case KindChat:
		needs = c.Title != ""
		expects = e.Messages > 0 || e.Starred != nil || e.Branches > 0 || len(e.TextContains) > 0
	default:
		return fmt.Errorf("use case %q has kind %q, which is not turn, search or chat", c.Name, c.Kind)
	}
	if !needs {
		return fmt.Errorf("use case %q, a %s, has nothing to do: a turn needs ask, a search needs query and a chat needs title", c.Name, c.Kind)
	}
	if !expects {
		return fmt.Errorf("use case %q expects nothing a %s can show", c.Name, c.Kind)
	}
	return nil
}

// RunCases runs every use case against a loaded test tenant and returns one
// line for each that did not give its expected result. An empty answer
// means every use case passed.
func RunCases(s *tenant.Store, fx Fixtures, lim chat.Limits) ([]string, error) {
	p, err := Provider(fx, fake.Options{})
	if err != nil {
		return nil, err
	}
	var failed []string
	for _, c := range fx.Cases {
		problems, err := runCase(s, p, c, lim)
		if err != nil {
			return nil, fmt.Errorf("use case %q: %w", c.Name, err)
		}
		for _, problem := range problems {
			failed = append(failed, fmt.Sprintf("use case %q: %s", c.Name, problem))
		}
	}
	return failed, nil
}

func runCase(s *tenant.Store, p seam.Provider, c Case, lim chat.Limits) ([]string, error) {
	var problems []string
	miss := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	e := c.Expect
	switch c.Kind {
	case KindTurn:
		reply, err := seam.Call(context.Background(), p, seam.Request{Messages: []seam.Message{{
			Role:   seam.RoleUser,
			Blocks: []seam.Block{{Type: seam.BlockText, Text: c.Ask}},
		}}})
		if err != nil {
			miss("the turn failed: %v", err)
			break
		}
		if e.StopReason != "" && string(reply.StopReason) != e.StopReason {
			miss("the reply stopped with %q, and %q was expected", reply.StopReason, e.StopReason)
		}
		text := seam.Text(reply.Message)
		for _, want := range e.TextContains {
			if !strings.Contains(text, want) {
				miss("the reply does not hold %q", want)
			}
		}
		if e.ToolCalls != nil {
			var got []string
			for _, b := range seam.ToolCalls(reply.Message) {
				got = append(got, b.Name)
			}
			if strings.Join(got, ", ") != strings.Join(e.ToolCalls, ", ") {
				miss("the reply asks for the tools [%s], and [%s] was expected", strings.Join(got, ", "), strings.Join(e.ToolCalls, ", "))
			}
		}

	case KindSearch:
		hits, err := chat.Search(s, lim, c.Query)
		if err != nil {
			return nil, err
		}
		found := map[string]bool{}
		for _, h := range hits {
			found[h.Title] = true
		}
		got := make([]string, 0, len(found))
		for title := range found {
			got = append(got, title)
		}
		want := append([]string(nil), e.Titles...)
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, " | ") != strings.Join(want, " | ") {
			miss("the search found [%s], and [%s] was expected", strings.Join(got, " | "), strings.Join(want, " | "))
		}

	case KindChat:
		chats, err := chat.List(s, chat.Filter{})
		if err != nil {
			return nil, err
		}
		var match []chat.Chat
		for _, ch := range chats {
			if ch.Title == c.Title {
				match = append(match, ch)
			}
		}
		if len(match) != 1 {
			miss("%d chats are titled %q, and one was expected", len(match), c.Title)
			break
		}
		path, err := chat.Path(s, match[0].ID)
		if err != nil {
			return nil, err
		}
		if e.Messages > 0 && len(path) != e.Messages {
			miss("the current branch has %d messages, and %d were expected", len(path), e.Messages)
		}
		if e.Starred != nil && match[0].Starred != *e.Starred {
			miss("starred is %v, and %v was expected", match[0].Starred, *e.Starred)
		}
		branches := 0
		var text strings.Builder
		for _, m := range path {
			branches = max(branches, len(m.Siblings))
			text.WriteString(seam.Text(seam.Message{Role: m.Role, Blocks: m.Blocks}))
			text.WriteString("\n")
		}
		if e.Branches > 0 && branches != e.Branches {
			miss("the most branches at one point is %d, and %d was expected", branches, e.Branches)
		}
		for _, want := range e.TextContains {
			if !strings.Contains(text.String(), want) {
				miss("no message on the current branch holds %q", want)
			}
		}
	}
	return problems, nil
}
