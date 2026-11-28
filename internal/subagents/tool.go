package subagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/unattended"
)

// The prefix of a sub-agent's chat's title.
const workTitlePrefix = "Sub-agent: "

// input is what a model sends run_subagents.
type input struct {
	Agents []agentInput `json:"agents"`
}

// agentInput is one sub-agent a model asks for.
type agentInput struct {
	Task      string   `json:"task"`
	Title     string   `json:"title"`
	Context   string   `json:"context"`
	Model     string   `json:"model"`
	BudgetUSD *float64 `json:"budget_usd"`
}

// batch is one call's sub-agents, as its supervisor works them.
type batch struct {
	tenantID string
	chatID   string // the chat they were started from
	turnID   string // its turn that made the call, when one is running
	ids      []string
	key      string // what the supervisor waits on: tenant/turn/call
}

// callKey is what a call's supervisor waits on, so a stop from a lane
// wakes it.
func callKey(tenantID, turnID, callID string) string {
	return tenantID + "/" + turnID + "/" + callID
}

const toolSchema = `{"type":"object","properties":{"agents":{"type":"array","minItems":1,` +
	`"description":"The sub-agents to start, each with a part of the work. They run at the same time.","items":{"type":"object","properties":{` +
	`"task":{"type":"string","description":"What this sub-agent is to do, in full. It sees nothing of this conversation but what you write here and in context."},` +
	`"title":{"type":"string","description":"Its name in a few words, shown on its lane in the chat."},` +
	`"context":{"type":"string","description":"What it needs from this conversation: facts, names, what was decided, what to leave alone."},` +
	`"model":{"type":"string","description":"The registry name of one of the person's models for it to use. Leave it out to use this chat's own."},` +
	`"budget_usd":{"type":"number","description":"The most it may spend, in US dollars, on models that are not local: at most the person's limit for a sub-agent. Leave it out for that limit."}},` +
	`"required":["task"]}}},"required":["agents"]}`

// Tool returns run_subagents, the tool a model starts sub-agents with from
// the chat its turn runs in. It waits for them and returns their results.
//
// It says it only reads and that its result is Keystone's own, as the
// artifact tools do (decision 0420), so it runs under the default policy
// always and does not ask because a chat read outside content: it changes
// nothing outside Keystone and loses nothing. What a sub-agent does that
// changes something asks the person as any turn's call does, and its spend
// is held to its own budget. A result that brings outside content back
// carries the mark and marks the chat (internal/subagents/carry). A person
// can set it to ask or never on Settings → Approvals (decision 0481).
func (r *Runner) Tool() tools.Tool {
	return tools.Tool{
		Name: ToolName,
		Description: "Start sub-agents: helpers that each do one part of the work at the same time, each in a chat of its own with the same tools, and wait for their results, which come back as this tool's result. " +
			"Use it when the work splits into parts that do not depend on each other, such as looking into several things at once. " +
			"A sub-agent sees nothing of this conversation: write each task so it stands alone, and put what it needs in its context. " +
			"Each has its own budget and step limit, and anything it would change still asks the person. A sub-agent cannot start sub-agents.",
		InputSchema: json.RawMessage(toolSchema),
		ReadOnly:    true,
		Inside:      true,
		Timeout:     r.o.Config.Timeout,
		Describe:    r.describe,
		Run:         r.run,
	}
}

// AddTool adds run_subagents to a registry.
func (r *Runner) AddTool(reg *tools.Registry) error { return reg.Add(r.Tool()) }

// describe says what one call will do, for an approval card when a person
// has set the tool to ask.
func (r *Runner) describe(raw json.RawMessage) string {
	var in input
	if json.Unmarshal(raw, &in) != nil || len(in.Agents) == 0 {
		return "Start sub-agents."
	}
	var titles []string
	for _, a := range in.Agents {
		titles = append(titles, "“"+r.titleOf(a.Title, a.Task)+"”")
	}
	n := "one sub-agent"
	if len(in.Agents) > 1 {
		n = fmt.Sprintf("%d sub-agents in parallel", len(in.Agents))
	}
	return fmt.Sprintf("Run %s: %s. Each works in a chat of its own with the same tools, spends up to its budget, and its result comes back here.", n, strings.Join(titles, ", "))
}

// outcome is what a call's supervisor hands back: the tool's result, or
// why there is none.
type outcome struct {
	text string
	err  error
}

// run is run_subagents: it writes the sub-agents, hands them to a
// supervisor that runs under the turn runner, and waits for their results.
// When the turn is stopped, or the call runs past agents.subagent_timeout,
// the supervisor stops them; the turn does not wait for that.
func (r *Runner) run(ctx context.Context, in tools.Input) (string, error) {
	t := r.bound()
	if t == nil || r.o.Install == nil {
		return "", errors.New("sub-agents cannot run on this server")
	}
	var args input
	if err := json.Unmarshal(in.Args, &args); err != nil {
		return "", errors.New("the input does not read: give agents, a list of objects each with its task")
	}
	b, err := r.start(ctx, in.Store, in.ChatID, in.CallID, args)
	if err != nil {
		return "", err
	}
	out := make(chan outcome, 1)
	if !t.Go(func(base context.Context) { r.supervise(base, ctx, b, out) }) {
		return "", errors.New("Keystone is stopping, so the sub-agents were not run")
	}
	select {
	case o := <-out:
		return o.text, o.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// start checks a call and writes its sub-agents: for each, its chat, its
// row, and its run in the registry with its budget, in the transaction that
// writes the row (P8-05). Nothing is started that a later check refuses: a
// call that fails part of the way removes what it wrote. Its errors are
// written for the model.
func (r *Runner) start(ctx context.Context, s *tenant.Store, chatID, callID string, in input) (batch, error) {
	if len(in.Agents) == 0 {
		return batch{}, errors.New("give at least one sub-agent in agents, each with its task")
	}
	for i, a := range in.Agents {
		if strings.TrimSpace(a.Task) == "" {
			return batch{}, fmt.Errorf("sub-agent %d has no task: say what it is to do", i+1)
		}
	}
	if _, err := chat.Get(s, chatID); err != nil {
		return batch{}, err
	}
	if isWork(ctx, s, chatID) {
		return batch{}, errors.New("a sub-agent cannot start sub-agents: do this part of the work yourself, with your tools")
	}
	if on, err := unattended.Paused(ctx, s); err != nil || on {
		return batch{}, errors.New("no sub-agent was started: every agent of the person's is paused. Tell them, and do the work yourself or wait until they resume their agents")
	}
	turnID := runningTurn(ctx, s, chatID, audit.RequestID(ctx))
	var already int
	if turnID != "" {
		if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM subagents WHERE turn_id = ?", turnID).Scan(&already); err != nil {
			return batch{}, err
		}
	}
	if most := r.o.Config.PerTurn; already+len(in.Agents) > most {
		left := max(most-already, 0)
		return batch{}, fmt.Errorf("no sub-agent was started: one turn may start %d in all, and this one has started %d, so it may start %d more", most, already, left)
	}
	limit, err := unattended.Budget(ctx, s, unattended.KindSubagent)
	if err != nil {
		r.logf("sub-agents: tenant %s: reading %s: %v", s.TenantID(), unattended.KeySubagentBudget, err)
		return batch{}, errors.New("no sub-agent was started: the person's budget for a sub-agent could not be read")
	}
	budgets := make([]int64, len(in.Agents))
	for i, a := range in.Agents {
		budgets[i] = limit
		if a.BudgetUSD != nil {
			n, err := unattended.Micros(*a.BudgetUSD)
			if err != nil {
				return batch{}, fmt.Errorf("sub-agent %d: budget_usd is a number of US dollars, 0 or more", i+1)
			}
			budgets[i] = min(n, limit) // within the person's limit (decision 0482)
		}
		if name := strings.TrimSpace(a.Model); name != "" {
			if _, err := models.Get(ctx, s, name); err != nil {
				return batch{}, fmt.Errorf("sub-agent %d: there is no model named %q among the person's models; leave model out to use this chat's own", i+1, name)
			}
		}
	}
	// The incognito mark goes with the words, as a task's does (P6-06). A
	// mark that cannot be read counts as on.
	incognito, err := memory.Incognito(ctx, s, chatID)
	if err != nil {
		incognito = true
	}
	chatModel := r.bound().ChatModel(ctx, s, chatID)

	b := batch{tenantID: s.TenantID(), chatID: chatID, turnID: turnID, key: callKey(s.TenantID(), turnID, callID)}
	var made []string
	drop := func(err error) (batch, error) {
		for _, id := range made {
			if derr := chat.Delete(s, id); derr != nil {
				r.logf("sub-agents: removing the chat made for a sub-agent that did not start: %v", derr)
			}
		}
		for _, id := range b.ids {
			if _, derr := s.DB().ExecContext(context.Background(), "DELETE FROM subagents WHERE id = ?", id); derr != nil {
				r.logf("sub-agents: removing a sub-agent that did not start: %v", derr)
			}
		}
		return batch{}, err
	}
	for i, a := range in.Agents {
		title := r.titleOf(a.Title, a.Task)
		work, err := chat.Create(s)
		if err != nil {
			return drop(err)
		}
		made = append(made, work.ID)
		if _, err := chat.Rename(s, r.o.Turns.Limits, work.ID, workTitlePrefix+title); err != nil {
			return drop(err)
		}
		if incognito {
			if err := memory.SetIncognito(ctx, s, work.ID, true); err != nil {
				return drop(err)
			}
		}
		id, err := newID()
		if err != nil {
			return drop(err)
		}
		model := strings.TrimSpace(a.Model)
		on, err := r.onAuto(ctx, s, work.ID, model) // P14-06: no model named, Auto (auto.go)
		if err != nil {
			return drop(err)
		}
		if model == "" && !on {
			model = chatModel
		}
		requestID := audit.NewRequestID()
		err = inTx(ctx, s, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO subagents(id, chat_id, turn_id, call_id, position, work_chat_id, title, task, context,
				request_id, actor, model, budget_micros, max_steps, status, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'queued', ?)`,
				id, chatID, turnID, callID, i+1, work.ID, title, strings.TrimSpace(a.Task), strings.TrimSpace(a.Context),
				requestID, api.ActorPerson, model, budgets[i], r.o.Config.MaxSteps, stamp()); err != nil {
				return err
			}
			// The sub-agent is an unattended run, with its budget, before its
			// turn can be sent (P8-05).
			return unattended.Register(ctx, tx, unattended.Run{Kind: unattended.KindSubagent, ID: id, RequestID: requestID, Title: title, BudgetMicros: budgets[i]})
		})
		if err != nil {
			return drop(err)
		}
		b.ids = append(b.ids, id)
	}
	return b, nil
}

// runningTurn returns the turn of a chat that is running, the one whose
// tool call this is: the one sent under requestID when there is one. "" when
// none is, as when a test calls the tool by itself.
func runningTurn(ctx context.Context, s *tenant.Store, chatID, requestID string) string {
	var id string
	if requestID != "" {
		if s.DB().QueryRowContext(ctx, "SELECT id FROM turns WHERE chat_id = ? AND request_id = ? AND status = 'running' ORDER BY seq DESC LIMIT 1", chatID, requestID).Scan(&id) == nil {
			return id
		}
	}
	if s.DB().QueryRowContext(ctx, "SELECT id FROM turns WHERE chat_id = ? AND status = 'running' ORDER BY seq DESC LIMIT 1", chatID).Scan(&id) == nil {
		return id
	}
	return ""
}

// titleOf is a sub-agent's title: the one given, or the first words of its
// task, on one line, short enough that its chat's title fits.
func (r *Runner) titleOf(given, task string) string {
	words := strings.Fields(given)
	if len(words) == 0 {
		words = strings.Fields(task)
		if len(words) > 6 {
			words = words[:6]
		}
	}
	title := []rune(strings.Join(words, " "))
	most := r.o.Turns.Limits.TitleLength - len([]rune(workTitlePrefix))
	if most < 1 {
		most = 1
	}
	if len(title) > most {
		title = append([]rune(strings.TrimSpace(string(title[:most-1]))), '…')
	}
	if len(title) == 0 {
		return "Sub-agent"
	}
	return string(title)
}

// message is a sub-agent's first and only message: the brief, its task, and
// what the model passed it. It is all of its context from the parent chat.
func (r *Runner) message(a row) string {
	var b strings.Builder
	b.WriteString(r.o.Config.Brief)
	b.WriteString("\n\nYour task:\n")
	b.WriteString(strings.TrimSpace(a.Task))
	if c := strings.TrimSpace(a.context); c != "" {
		b.WriteString("\n\nWhat you were given:\n")
		b.WriteString(c)
	}
	return b.String()
}
