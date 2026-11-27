package research

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/subagents"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
	"github.com/jonathanmkosar/keystone/settings"
)

// The prefix of the title of a task's own chat, and of its run's title.
const workTitlePrefix = "Research: "

// The errors Start gives, beside the chat record's.
var (
	errInvalid = errors.New("the research task is refused")
	errTooMany = errors.New("too many research tasks are open")
)

// Options is everything a Runner is made with.
type Options struct {
	// Install is the data folder. The runner opens a store of its own for
	// each task it works.
	Install *tenant.Install
	// Log is the server's own log. Nil means none.
	Log *api.Log
	// Config is the install's settings for research.
	Config Config
	// Turns is what turns run with: how often a look comes when nothing has
	// woken it, how long a stop waits, and the chat record's limits.
	Turns turn.Config
	// Subagents runs the parts.
	Subagents *subagents.Runner
	// Artifacts are the limits the report is saved under.
	Artifacts artifacts.Options
	// PartChars is the most characters of each part's findings that go to
	// the report step: agents.subagent_result_max_chars.
	PartChars int
}

// Runner starts and works research tasks. It keeps nothing a restart would
// lose: a task is a row, its steps are turns, and its parts are sub-agents.
// What it holds in memory is which tasks have a goroutine at work, and who
// is waiting to be told a task has news.
type Runner struct {
	o Options

	mu      sync.Mutex
	turns   *turn.Runner
	working map[string]bool
	waiting map[string]chan struct{}
}

// NewRunner returns a runner. It works nothing until it is bound to the
// turn runner, with Bind.
func NewRunner(o Options) *Runner {
	return &Runner{o: o, working: map[string]bool{}, waiting: map[string]chan struct{}{}}
}

// Bind gives the runner the turn runner its tasks' turns run on. The first
// one given stays. A task's goroutine runs under the turn runner, so
// closing it stops the goroutine and waits for it.
func (r *Runner) Bind(t *turn.Runner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turns == nil {
		r.turns = t
	}
}

func (r *Runner) bound() *turn.Runner {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.turns
}

func (r *Runner) logf(format string, args ...any) { r.o.Log.Printf(format, args...) }

func (r *Runner) waitFor(key string) <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := r.waiting[key]
	if ch == nil {
		ch = make(chan struct{})
		r.waiting[key] = ch
	}
	return ch
}

func (r *Runner) wake(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch := r.waiting[key]; ch != nil {
		close(ch)
		delete(r.waiting, key)
	}
}

// SkipMemory returns the memory pass's check, as turn.Options.SkipMemory,
// with a research task's own chat added to inner's: its messages are
// Keystone's prompts and what the parts read on the web, not the person's
// words, so no fact is taken from them. Its parts' chats are sub-agents',
// which are skipped already.
func (r *Runner) SkipMemory(inner func(context.Context, *tenant.Store, string) bool) func(context.Context, *tenant.Store, string) bool {
	return func(ctx context.Context, s *tenant.Store, chatID string) bool {
		if inner != nil && inner(ctx, s, chatID) {
			return true
		}
		return isWork(ctx, s, chatID)
	}
}

// Recover starts the work of every task a stopped server left unfinished,
// for every tenant: those that have not ended, and those that ended and
// whose note or report is not yet written. The turn runner's Recover and
// the sub-agents' run first, so a turn the stop cut short is closed as
// interrupted and the parts that were working are ended.
func (r *Runner) Recover() {
	if r.o.Install == nil {
		return
	}
	for _, t := range r.o.Install.Tenants() {
		s, err := r.o.Install.Store(t.ID)
		if err != nil {
			r.logf("research: recovering tenant %s: %v", t.ID, err)
			continue
		}
		open, err := list(context.Background(), s.DB(),
			"status IN ('queued', 'running') OR note_message_id = 0 OR (report <> '' AND artifact_id = '')")
		s.Close()
		if err != nil {
			r.logf("research: recovering tenant %s: %v", t.ID, err)
			continue
		}
		for _, j := range open {
			r.kick(t.ID, j.ID)
		}
	}
}

// kick starts a goroutine that works a task, unless one is at work.
func (r *Runner) kick(tenantID, id string) {
	key := tenantID + "/" + id
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turns == nil || r.working[key] {
		return
	}
	r.working[key] = true
	if !r.turns.Go(func(ctx context.Context) { r.work(ctx, tenantID, id) }) {
		delete(r.working, key)
	}
}

var registry = sync.OnceValues(settings.Registry)

// Budget is the person's budget for one research task, in millionths of a
// US dollar: agents.research_budget_usd.
func Budget(ctx context.Context, s *tenant.Store) (int64, error) {
	reg, err := registry()
	if err != nil {
		return 0, err
	}
	v, err := tenantsettings.Get(ctx, s, reg, KeyBudget)
	if err != nil {
		return 0, err
	}
	usd, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", KeyBudget, err)
	}
	n, err := unattended.Micros(usd)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", KeyBudget, err)
	}
	return n, nil
}

// open counts the person's tasks that have not ended.
func open(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT count(*) FROM research WHERE status IN ('queued', 'running')").Scan(&n)
	return n, err
}

// Terms says what a task started now would run with.
func (r *Runner) Terms(ctx context.Context, s *tenant.Store) (Terms, error) {
	budget, err := Budget(ctx, s)
	if err != nil {
		return Terms{}, err
	}
	n, err := open(ctx, s.DB())
	if err != nil {
		return Terms{}, err
	}
	paused, _ := unattended.Paused(ctx, s) // one that cannot be read counts as on
	c := r.o.Config
	return Terms{BudgetMicros: budget, Parts: c.Parts, SourcesPerPart: c.SourcesPerPart, ReportWords: c.ReportWords,
		Open: n, OpenMax: c.OpenMax, Paused: paused}, nil
}

// Start writes a research task, with its own chat, and starts its work.
// chatID is the chat it is started from, startedRequest the API call that
// started it, and actor who it runs for.
func (r *Runner) Start(ctx context.Context, s *tenant.Store, chatID, startedRequest, actor string, body StartBody) (Research, error) {
	question := strings.TrimSpace(body.Question)
	if question == "" {
		return Research{}, fmt.Errorf("%w: the question is empty: say what to research", errInvalid)
	}
	if _, err := chat.Get(s, chatID); err != nil {
		return Research{}, err
	}
	if isWork(ctx, s, chatID) {
		return Research{}, fmt.Errorf("%w: this chat is a research task's own: start research from the chat you asked in", errInvalid)
	}
	var budget int64
	var err error
	if body.BudgetUSD != nil {
		if budget, err = unattended.Micros(*body.BudgetUSD); err != nil {
			return Research{}, fmt.Errorf("%w: %s", errInvalid, strings.TrimPrefix(err.Error(), unattended.ErrInvalid.Error()+": "))
		}
	} else if budget, err = Budget(ctx, s); err != nil {
		return Research{}, err
	}
	tooMany := func(q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	}) error {
		n, err := open(ctx, q)
		if err != nil {
			return err
		}
		if n >= r.o.Config.OpenMax {
			return fmt.Errorf("%w: %d are under way, the most one person may have", errTooMany, n)
		}
		return nil
	}
	if err := tooMany(s.DB()); err != nil {
		return Research{}, err
	}
	model := ""
	if t := r.bound(); t != nil {
		model = t.ChatModel(ctx, s, chatID)
	}
	title := r.titleOf(body.Title, question)
	work, err := chat.Create(s)
	if err != nil {
		return Research{}, err
	}
	drop := func(err error) (Research, error) {
		if derr := chat.Delete(s, work.ID); derr != nil {
			r.logf("research: removing the chat made for a task that did not start: %v", derr)
		}
		return Research{}, err
	}
	if _, err := chat.Rename(s, r.o.Turns.Limits, work.ID, workTitlePrefix+title); err != nil {
		return drop(err)
	}
	// The question comes from the chat it was started in, so a task from an
	// incognito chat works in an incognito chat (P6-06). A flag that cannot
	// be read counts as on.
	if on, err := memory.Incognito(ctx, s, chatID); err != nil || on {
		if err := memory.SetIncognito(ctx, s, work.ID, true); err != nil {
			return drop(err)
		}
	}
	id, err := newID()
	if err != nil {
		return drop(err)
	}
	c := r.o.Config
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		if err := tooMany(tx); err != nil {
			return err
		}
		requestID := audit.NewRequestID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO research(id, chat_id, work_chat_id, title, question, status, request_id, started_request,
			actor, model, budget_micros, max_parts, sources_per_part, report_words, call_id, progress, created_at)
			VALUES(?, ?, ?, ?, ?, 'queued', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, chatID, work.ID, title, question, requestID, startedRequest, actor, model, budget,
			c.Parts, c.SourcesPerPart, c.ReportWords, "research-"+id, "Waiting to start", stamp()); err != nil {
			return err
		}
		// The task is an unattended run, with its whole budget, before any of
		// its turns can be sent (P8-05). It runs as a background task does.
		return unattended.Register(ctx, tx, unattended.Run{Kind: unattended.KindTask, ID: id, RequestID: requestID,
			Title: workTitlePrefix + title, BudgetMicros: budget})
	})
	if err != nil {
		return drop(err)
	}
	r.kick(s.TenantID(), id)
	j, err := get(ctx, s.DB(), id)
	if err != nil {
		return Research{}, err
	}
	return r.show(ctx, s, j), nil
}

// titleOf is a task's title: the one given, or the first words of the
// question, on one line, short enough that its chat's title fits.
func (r *Runner) titleOf(given, question string) string {
	words := strings.Fields(given)
	more := ""
	if len(words) == 0 {
		words = strings.Fields(question)
		if len(words) > 8 {
			words, more = words[:8], "…"
		}
	}
	title := []rune(strings.TrimRight(strings.Join(words, " "), "?.!,;:") + more)
	most := min(r.o.Turns.Limits.TitleLength-len([]rune(workTitlePrefix)), r.o.Artifacts.TitleMaxChars)
	if most < 1 {
		most = 1
	}
	if len(title) > most {
		title = append([]rune(strings.TrimSpace(string(title[:most-1]))), '…')
	}
	if len(title) == 0 {
		return "Research"
	}
	return string(title)
}

// Stop stops one task and returns it as it then stands. The stop is written
// on its row, its goroutine stops the step under way and ends it, and this
// waits for that, up to chats.stop_wait. One that has ended is returned as
// it is.
func (r *Runner) Stop(ctx context.Context, s *tenant.Store, chatID, id string) (Research, error) {
	j, err := getInChat(ctx, s, chatID, id)
	if err != nil {
		return Research{}, err
	}
	if !ended(j.stored) {
		if err := change(ctx, s, j.ID, "stop_requested = 1"); err != nil {
			return Research{}, err
		}
		key := s.TenantID() + "/" + j.ID
		r.kick(s.TenantID(), j.ID)
		r.wake(key)
		deadline := time.Now().Add(r.o.Turns.StopWait)
		for {
			news := r.waitFor(key + "/ended")
			if j, err = get(ctx, s.DB(), j.ID); err != nil || ended(j.stored) {
				break
			}
			left := time.Until(deadline)
			if left <= 0 || ctx.Err() != nil {
				break
			}
			select {
			case <-news:
			case <-time.After(min(left, r.o.Turns.StreamPoll)):
			case <-ctx.Done():
			}
		}
		if err != nil {
			return Research{}, err
		}
	}
	return r.show(ctx, s, j), nil
}

// work is a task's goroutine. It reads the task, does the next thing it
// needs, and waits for news, until the task has ended and its note and its
// report are written. It has a store of its own, because the store a call
// came with closes when the call ends.
func (r *Runner) work(ctx context.Context, tenantID, id string) {
	key := tenantID + "/" + id
	defer func() {
		r.mu.Lock()
		delete(r.working, key)
		delete(r.waiting, key) // only this goroutine waits on the task's own key
		r.mu.Unlock()
	}()
	s, err := r.o.Install.Store(tenantID)
	if err != nil {
		r.logf("research: opening the store of tenant %s: %v", tenantID, err)
		return
	}
	defer s.Close()
	var last row
	for ctx.Err() == nil {
		news := r.waitFor(key)
		j, err := get(ctx, s.DB(), id)
		if errors.Is(err, errNoResearch) {
			// It went with the chat it was started from: what it had under way
			// goes too.
			if last.ID != "" {
				r.stopSteps(ctx, s, last)
			}
			return
		}
		if err != nil {
			if ctx.Err() == nil {
				r.logf("research: reading task %s of tenant %s: %v", id, tenantID, err)
			}
			return
		}
		last = j
		if ended(j.stored) {
			r.deliver(ctx, s, j)
			r.wake(key + "/ended")
			return
		}
		turnNews, wait := r.next(ctx, s, j)
		if !wait {
			continue
		}
		select {
		case <-news:
		case <-turnNews:
		case <-time.After(r.o.Turns.StreamPoll):
		case <-ctx.Done():
			return
		}
	}
}
