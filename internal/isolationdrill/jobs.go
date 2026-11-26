package isolationdrill

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/wikijobs"
)

// jobs is the drill's part on the work that runs with no client: a
// background task, a scheduled run, the built-in store's wiki compile job
// and the memory pass, each of the second person's, aimed at the first
// person's chats, files, knowledge and vault.
func (g *rig) jobs() error {
	two, f := g.two, g.first
	steps := g.battery()
	g.model.plan("tools-task", steps...)
	g.model.plan("tools-schedule", steps...)
	stop := g.approveAll(two)
	defer stop()
	notesBefore, openBefore := len(g.notes.Calls()), len(g.openMCP.Calls())

	// 1. A background task, started through the API, whose rounds call
	// every tool with the first person's ids.
	var task jobs.Task
	if err := g.as(two, "POST", "chats/"+g.second.chat+"/tasks", jsonOf(jobs.StartBody{Text: "drill:tools-task Look through the lantern chats.", Title: "Quay task"}), http.StatusCreated, &task); err != nil {
		return err
	}
	if err := g.taskEnds(two, g.second.chat, task.ID); err != nil {
		return err
	}
	if err := g.judgeWork("a background task of the second person's", task.ID, task.WorkChatID); err != nil {
		return err
	}

	// 2. A scheduled run, run now.
	off := false
	var sch schedules.Schedule
	if err := g.as(two, "POST", "schedules", jsonOf(schedules.ScheduleBody{Name: "Quay brief", Prompt: "drill:tools-schedule Read the lantern ledger.",
		Recurrence: schedules.Recurrence{Kind: schedules.KindDaily, Time: "03:19"}, Enabled: &off}), http.StatusCreated, &sch); err != nil {
		return err
	}
	var run schedules.Run
	if err := g.as(two, "POST", "schedules/"+sch.ID+"/run", "", http.StatusCreated, &run); err != nil {
		return err
	}
	err := waitFor("the second person's scheduled run", 30*time.Second, func() (bool, error) {
		var got schedules.ScheduleReply
		if err := g.as(two, "GET", "schedules/"+sch.ID, "", http.StatusOK, &got); err != nil {
			return false, err
		}
		for _, r := range got.Runs {
			if r.ID == run.ID {
				run = r
			}
		}
		return run.Status == jobs.StatusDone || run.Status == schedules.RunFailed || run.Status == jobs.StatusFailed, nil
	})
	if err != nil {
		return err
	}
	if run.Status != jobs.StatusDone {
		return fmt.Errorf("the second person's scheduled run ended as %s: %s", run.Status, run.Error)
	}
	if err := g.judgeWork("a scheduled run of the second person's", run.TaskID, run.ChatID); err != nil {
		return err
	}

	// 3. The built-in store's compile job: it reads the first person's
	// sources and page by id, then answers with a page that cites the first
	// person's source and takes the first person's path.
	answer := map[string]any{
		"batches": []map[string]any{{"note": "The second person's job", "writes": []map[string]any{{
			"space": spaceName, "path": "ledger/code", "title": "Lantern Ledger Code",
			"body": "Overwritten by the second person's job. [[source:" + f.source + "]]", "sources": []string{f.source},
		}}}},
		"report": "One page.",
	}
	g.model.wiki = [2][]seam.Block{
		{use("list_uncompiled_sources", map[string]any{}), use("read_source", map[string]string{"source_id": f.source}),
			use("read_source", map[string]string{"source_id": f.memorySource}), use("read_page", map[string]string{"page_id": f.page}), use("read_catalog", map[string]any{})},
		{text("```json\n" + jsonOf(answer) + "\n```")},
	}
	var wrun schedules.Run
	if err := g.as(two, "POST", "wiki-jobs/"+wikijobs.JobBuiltinCompile+"/run", "", http.StatusCreated, &wrun); err != nil {
		return err
	}
	var written *wikijobs.RunRecord
	err = waitFor("the second person's wiki run to be written", 40*time.Second, func() (bool, error) {
		var got wikijobs.JobsReply
		if err := g.as(two, "GET", "wiki-jobs", "", http.StatusOK, &got); err != nil {
			return false, err
		}
		for _, j := range got.Jobs {
			if j.Name == wikijobs.JobBuiltinCompile && j.LastWritten != nil && j.LastWritten.RunID == wrun.ID && j.LastWritten.State != "applying" {
				written = j.LastWritten
			}
		}
		return written != nil, nil
	})
	if err != nil {
		return err
	}
	if err := g.judgeWork("a wiki compile run of the second person's", wrun.TaskID, wrun.ChatID); err != nil {
		return err
	}
	g.report.tried(AreaJobs, "a wiki compile run of the second person's answers with a page at the first person's path, citing <one's source>",
		fmt.Sprintf("Keystone wrote it as %s in the second person's own store: %s; %s", written.State, strip(g.idText(written.Summary)),
			g.judge(AreaJobs, "the wiki run's record", jsonOf(written), jsonOf(answer), false)))

	// 4. The memory pass, with the second person's memory space named as
	// the first person's.
	if err := g.as(two, "PUT", "settings/"+memory.SettingSpace, jsonOf(map[string]string{"value": spaceName}), http.StatusOK, nil); err != nil {
		return err
	}
	chatID, err := g.newChat(two)
	if err != nil {
		return err
	}
	got, err := g.send(two, chatID, turn.SendBody{Text: "drill:two-hello I row at the south quay on Sundays."})
	if err != nil {
		return err
	}
	var pass memory.Pass
	err = waitFor("the second person's memory pass", 20*time.Second, func() (bool, error) {
		var ok bool
		err := g.with(two.id, func(s *tenant.Store) (err error) {
			pass, ok, err = memory.Get(context.Background(), s, got.turnID)
			return
		})
		return ok && pass.Status != memory.StatusQueued && pass.Status != memory.StatusRunning, err
	})
	if err != nil {
		return err
	}
	g.report.tried(AreaJobs, "the memory pass of a turn of the second person's, with their memory space named as the first person's space",
		fmt.Sprintf("%s, %d facts kept in the space %q of the second person's own store as source %s; %s", pass.Status, len(pass.Facts), pass.Space, pass.SourceID,
			g.judge(AreaJobs, "the memory pass", jsonOf(pass), "", false)))
	g.tokenNotSent(AreaJobs, notesBefore, openBefore)
	return nil
}

// judgeWork reads the chat a task or run worked in, as the second person,
// and judges every tool call in it, with the task's own request id for the
// audit log.
func (g *rig) judgeWork(where, taskID, chatID string) error {
	var requestID string
	err := g.with(g.two.id, func(s *tenant.Store) error {
		return s.DB().QueryRow("SELECT request_id FROM jobs WHERE id = ?", taskID).Scan(&requestID)
	})
	if err != nil {
		return fmt.Errorf("%s has no request id: %w", where, err)
	}
	var reply turn.ChatReply
	if err := g.as(g.two, "GET", "chats/"+chatID, "", http.StatusOK, &reply); err != nil {
		return err
	}
	calls := map[string]callResult{}
	var order []string
	for _, m := range reply.Messages {
		for _, b := range m.Blocks {
			switch b.Type {
			case seam.BlockToolCall:
				calls[b.ID] = callResult{id: b.ID, name: b.Name, input: string(b.Input)}
				order = append(order, b.ID)
			case seam.BlockToolResult:
				c := calls[b.ID]
				c.text, c.isError = seam.Text(seam.Message{Blocks: b.Content}), b.IsError
				calls[b.ID] = c
			}
		}
	}
	var results []callResult
	for _, id := range order {
		results = append(results, calls[id])
	}
	if len(results) == 0 {
		return fmt.Errorf("%s made no tool calls: %s", where, strip(jsonOf(reply.Messages)))
	}
	raw, _ := json.Marshal(reply)
	g.judge(AreaJobs, where+"'s chat", string(raw), "", false)
	return g.judgeCalls(AreaJobs, where, requestID, results)
}
