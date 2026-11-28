package loadtest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/operations"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/uploads"
	"github.com/jonathanmkosar/keystone/internal/usagereport"
)

// Measurement is one thing a person waits on, timed over a number of runs.
type Measurement struct {
	Name string `json:"name"`
	What string `json:"what"`
	Runs int    `json:"runs"`
	// Median and P95 are over the runs. P95 is the nearest rank: with 30
	// runs, the 29th when sorted.
	Median time.Duration `json:"median"`
	P95    time.Duration `json:"p95"`
	// Budget is the setting, and Limit what this profile holds Held to:
	// the 95th percentile against the whole budget, or the small profile's
	// median against its share.
	Budget time.Duration `json:"budget"`
	Limit  time.Duration `json:"limit"`
	Held   time.Duration `json:"held"`
	Over   bool          `json:"over"`
}

// summarise makes a measurement of its runs.
func summarise(name string, runs []time.Duration, budgets Budgets, p Profile) Measurement {
	sorted := append([]time.Duration(nil), runs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	m := Measurement{Name: name, Runs: len(sorted), Budget: budgets[name]}
	for _, x := range Measures {
		if x.Name == name {
			m.What = x.What
		}
	}
	if len(sorted) > 0 {
		m.Median = sorted[(len(sorted)-1)/2]
		if len(sorted)%2 == 0 {
			m.Median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
		}
		rank := (len(sorted)*95 + 99) / 100 // the nearest rank, rounded up
		m.P95 = sorted[min(max(rank, 1), len(sorted))-1]
	}
	m.Limit, m.Held = m.Budget*time.Duration(p.BudgetPercent)/100, m.P95
	if p.OnMedian {
		m.Held = m.Median
	}
	m.Over = len(sorted) == 0 || m.Held > m.Limit
	return m
}

// measure makes every measurement against the real handler and returns
// them in the order of Measures.
func (g *rig) measure(ctx context.Context, l *load, budgets Budgets, say func(string)) ([]Measurement, error) {
	p, first := g.p, g.people[0]
	runs := map[string][]time.Duration{}
	r := rand.New(rand.NewPCG(p.Seed, 0x6d656173)) // "meas"
	words := func() string { return g.pl.vocab.text(r, 24, nil) }
	note := func(what string) {
		if say != nil {
			say(what)
		}
	}

	// Context packing in the long chat: a message sent to its model call
	// opening. The chat is read, packed under the local model's window with
	// its oldest turns left out, and the call recorded.
	for i := 0; i < p.Runs; i++ {
		got, err := g.send(first, l.long.id, turn.SendBody{Text: words(), Model: localModel.Name})
		if err != nil {
			return nil, fmt.Errorf("a turn in the long chat: %w", err)
		}
		runs[MeasureContextLong] = append(runs[MeasureContextLong], got.opened)
		g.mu.Lock()
		g.loose[got.requestID] = true
		g.mu.Unlock()
	}
	note("measured: context packing in the long chat")

	// Context packing in a chat with a project, an upload and attached
	// chats: every call carries the project's instructions and file, the
	// upload's text and each attached chat's recap.
	chatID, upload, err := g.projectChat(first, l)
	if err != nil {
		return nil, err
	}
	for i := 0; i < p.Runs; i++ {
		body := turn.SendBody{Text: words(), Model: localModel.Name}
		if i == 0 {
			body = turn.SendBody{Model: localModel.Name, Blocks: []seam.Block{
				{Type: seam.BlockText, Text: words()},
				{Type: seam.BlockFile, MediaType: upload.MediaType, Name: upload.Name, Ref: upload.ID},
			}}
		}
		got, err := g.send(first, chatID, body)
		if err != nil {
			return nil, fmt.Errorf("a turn in the project's chat: %w", err)
		}
		runs[MeasureContextProject] = append(runs[MeasureContextProject], got.opened)
	}
	note("measured: context packing with a project, an upload and attached chats")

	// Past-chat search, as the model does it: a real turn whose scripted
	// model calls search_chats, timed from the call to its result.
	for i := 0; i < p.Runs; i++ {
		chat, err := g.newChat(first)
		if err != nil {
			return nil, err
		}
		q := g.queries[i%len(g.queries)]
		got, err := g.send(first, chat, turn.SendBody{Text: searchAsk(q), Model: localModel.Name})
		if err != nil {
			return nil, fmt.Errorf("a search turn: %w", err)
		}
		if got.toolAsked == 0 || got.toolAnswered == 0 {
			return nil, fmt.Errorf("the search turn for %q ran no tool", q)
		}
		runs[MeasureSearchTool] = append(runs[MeasureSearchTool], got.toolAnswered-got.toolAsked)
	}
	// And as the person does it, from the sidebar.
	for i := 0; i < p.Runs; i++ {
		var found turn.SearchReply
		took, err := g.timed(first, "chats/search?q="+url.QueryEscape(g.queries[i%len(g.queries)]), &found)
		if err != nil {
			return nil, err
		}
		runs[MeasureSearchRoute] = append(runs[MeasureSearchRoute], took)
	}
	note("measured: past-chat search")

	// The chat list and the long chat's page.
	for i := 0; i < p.Runs; i++ {
		var list turn.ListReply
		took, err := g.timed(first, "chats", &list)
		if err != nil {
			return nil, err
		}
		if len(list.Chats) == 0 {
			return nil, fmt.Errorf("the chat list is empty")
		}
		runs[MeasureChatList] = append(runs[MeasureChatList], took)
		var page turn.ChatReply
		if took, err = g.timed(first, "chats/"+l.long.id, &page); err != nil {
			return nil, err
		}
		if len(page.Messages) < 2*p.LongChatTurns {
			return nil, fmt.Errorf("the long chat's page holds %d messages, and it has %d turns", len(page.Messages), p.LongChatTurns)
		}
		runs[MeasureChatPage] = append(runs[MeasureChatPage], took)
	}
	note("measured: the chat list and the long chat's page")

	// The reports.
	for i := 0; i < p.Runs; i++ {
		var usage usagereport.Report
		took, err := g.timed(first, "usage?period=this_month", &usage)
		if err != nil {
			return nil, err
		}
		runs[MeasureUsageReport] = append(runs[MeasureUsageReport], took)
		var rep operations.Report
		if took, err = g.timed(first, "operations", &rep); err != nil {
			return nil, err
		}
		runs[MeasureOperations] = append(runs[MeasureOperations], took)
		var watch operations.WatchReply
		if took, err = g.timed(first, "operations/watch", &watch); err != nil {
			return nil, err
		}
		runs[MeasureOperationsWatch] = append(runs[MeasureOperationsWatch], took)
	}
	note("measured: the usage and Operations reports")

	// Streaming, one person: a new chat on the hosted model, so the monthly
	// cap is read before the call, and a model that answers at once.
	for i := 0; i < p.Runs; i++ {
		got, err := g.fresh(first, words())
		if err != nil {
			return nil, err
		}
		runs[MeasureFirstWordsOne] = append(runs[MeasureFirstWordsOne], got.first)
		runs[MeasureStreamOne] = append(runs[MeasureStreamOne], got.last-got.first)
	}
	// Every person sending at once, each in a new chat of their own.
	for i := 0; i < p.Runs; i++ {
		texts := make([]string, len(g.people))
		for j := range texts {
			texts[j] = words()
		}
		got := make([]streamed, len(g.people))
		errs := make([]error, len(g.people))
		var ready, done sync.WaitGroup
		start := make(chan struct{})
		for j, who := range g.people {
			ready.Add(1)
			done.Add(1)
			go func() {
				defer done.Done()
				chat, err := g.newChat(who)
				ready.Done()
				if err != nil {
					errs[j] = err
					return
				}
				<-start
				got[j], errs[j] = g.send(who, chat, turn.SendBody{Text: texts[j], Model: hostedModel.Name})
			}()
		}
		ready.Wait()
		close(start)
		done.Wait()
		for j := range g.people {
			if errs[j] != nil {
				return nil, fmt.Errorf("every person sending at once: %w", errs[j])
			}
			runs[MeasureFirstWordsMany] = append(runs[MeasureFirstWordsMany], got[j].first)
			runs[MeasureStreamMany] = append(runs[MeasureStreamMany], got[j].last-got[j].first)
		}
	}
	note("measured: streaming, for one person and for every person at once")

	out := make([]Measurement, 0, len(Measures))
	for _, m := range Measures {
		out = append(out, summarise(m.Name, runs[m.Name], budgets, p))
	}
	return out, nil
}

// fresh sends one message in a new chat on the hosted model.
func (g *rig) fresh(who *person, text string) (streamed, error) {
	chat, err := g.newChat(who)
	if err != nil {
		return streamed{}, err
	}
	got, err := g.send(who, chat, turn.SendBody{Text: text, Model: hostedModel.Name})
	if err != nil {
		return streamed{}, err
	}
	if got.chars != g.p.ReplyChars {
		return streamed{}, fmt.Errorf("a measured reply streamed %d characters, want %d", got.chars, g.p.ReplyChars)
	}
	return got, nil
}

// projectChat makes, through the API, a project with instructions and a
// knowledge file, a chat in it with an upload, and attaches two of the
// person's generated chats to it.
func (g *rig) projectChat(who *person, l *load) (chatID string, up uploads.AddReply, err error) {
	r := rand.New(rand.NewPCG(g.p.Seed, 0x70726f6a)) // "proj"
	name, instructions := "Load Works", g.pl.vocab.text(r, 120, nil)
	var proj turn.ProjectReply
	if err = g.as(who, "POST", "projects", turn.ProjectBody{Name: &name, Instructions: &instructions}, http.StatusCreated, &proj); err != nil {
		return
	}
	file := []byte("# Load Works\n\n" + g.pl.vocab.text(r, 1500, nil) + "\n")
	var pf turn.ProjectFileReply
	if err = g.upload(who, "projects/"+proj.Project.ID+"/files?name=works.md", file, &pf); err != nil {
		return
	}
	if chatID, err = g.newChat(who); err != nil {
		return
	}
	if err = g.as(who, "PUT", "projects/"+proj.Project.ID+"/chats/"+chatID, nil, http.StatusOK, nil); err != nil {
		return
	}
	notes := []byte(g.pl.vocab.text(r, 1200, nil) + "\n")
	if err = g.upload(who, "chats/"+chatID+"/uploads?name=notes.txt", notes, &up); err != nil {
		return
	}
	for _, other := range l.attachable {
		if err = g.as(who, "PUT", "chats/"+chatID+"/attached/"+other.id, nil, http.StatusOK, nil); err != nil {
			return
		}
	}
	g.mu.Lock()
	g.extraFileBytes += onDisk(who, len(file)) + onDisk(who, len(notes))
	g.mu.Unlock()
	return
}
