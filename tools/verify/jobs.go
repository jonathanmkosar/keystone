package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// The standing check "background tasks" (P8-01). The plan's words for phase
// 8: a background task survives a restart. The item's proof: a background
// task finishes after a server restart and its result appears in the chat.
//
// On the real server, put together as internal/app puts it, with the
// shipped settings, as the test tenant, whose model is its recorded turns
// (fixtures/test-tenant/turns/background-task.json):
//
//  1. The person starts a task from a chat. Its first round calls
//     wait_for_tide, a tool only this check registers, which waits until it
//     is stopped. The server is stopped while it waits.
//  2. A second server on the same data folder closes the round as
//     interrupted and resumes the task with another round, which finishes.
//  3. The task ends as done, with two rounds and its result; the result is
//     a note in the chat it was started from; the round the stop cut short
//     is in its own chat as interrupted; its model calls and tool call are
//     in the audit log, and its model calls in the usage records, under the
//     task's own request id, on both sides of the restart; and another
//     tenant cannot read it or stop it.
//
// The task routes are walked with every other route by the tenant
// isolation, calls recorded and no keys in output checks (routes.go).

func init() {
	componentDocs["internal/jobs"] = "docs/architecture/jobs.md"
	networkAllowed["internal/jobs"] = "the background task routes' handlers, served by the server; it opens no connection"
}

const (
	tideTool    = "wait_for_tide"
	tideTask    = "Wait for the tide table, then say when the tide turns."
	tideResult  = "Carrying on after the restart: the tide turns at six this evening"
	tasksWaitUp = 10 * time.Second
)

// tideTools returns the server's own tools with wait_for_tide, which waits
// until it is stopped the first time it is called and answers at once after
// that, and a channel closed when it is first called.
func tideTools() (*tools.Registry, chan struct{}, *atomic.Int32, error) {
	c, err := app.Defaults()
	if err != nil {
		return nil, nil, nil, err
	}
	reg, err := app.Tools(c)
	if err != nil {
		return nil, nil, nil, err
	}
	called := make(chan struct{})
	var calls atomic.Int32
	err = reg.Add(tools.Tool{
		Name:        tideTool,
		Description: "Waits for the tide table and gives it. A tool for the background tasks check.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		ReadOnly:    true,
		Inside:      true,
		Run: func(ctx context.Context, _ tools.Input) (string, error) {
			if calls.Add(1) == 1 {
				close(called)
				<-ctx.Done()
				return "", ctx.Err()
			}
			return "High water at noon; the tide turns at six this evening.", nil
		},
	})
	return reg, called, &calls, err
}

func checkBackgroundTasks(string) error {
	dir, err := os.MkdirTemp("", "keystone-tasks-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return err
	}
	fx, err := testtenant.Shipped()
	if err != nil {
		return err
	}
	o, err := testtenant.DefaultOptions()
	if err != nil {
		return err
	}
	loaded, err := testtenant.Load(in, fx, o)
	if err != nil {
		return err
	}
	other, err := in.Create("Ordinary Person")
	if err != nil {
		return err
	}
	s, err := in.Store(loaded.Tenant.ID)
	if err != nil {
		return err
	}
	defer s.Close()
	os2, err := in.Store(other.ID)
	if err != nil {
		return err
	}
	defer os2.Close()
	if err := signin.SetPassword(os2, walkPasswordTwo, o.SignIn); err != nil {
		return err
	}
	reg, called, calls, err := tideTools()
	if err != nil {
		return err
	}
	start := func() (*walker, func(), string, error) {
		c, err := app.Defaults()
		if err != nil {
			return nil, nil, "", err
		}
		w := &walker{log: &bytes.Buffer{}}
		c.Install, c.Log, c.Tools = in, w.log, reg
		h, stop, err := app.Open(c)
		if err != nil {
			return nil, nil, "", err
		}
		w.h = h
		token, rec := w.signIn(loaded.Tenant.ID, fx.Password)
		if token == "" {
			stop()
			return nil, nil, "", fmt.Errorf("the test tenant cannot sign in: status %d", rec.Code)
		}
		return w, stop, token, nil
	}

	// 1. The first server: the task starts, and is cut short in its tool.
	w, stop, token, err := start()
	if err != nil {
		return err
	}
	rec := w.request("POST", api.Prefix+"chats", token, "", nil)
	var made turn.ChatReply
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil {
		stop()
		return fmt.Errorf("the test tenant cannot make a chat: status %d", rec.Code)
	}
	chatID := made.Chat.ID
	body, _ := json.Marshal(jobs.StartBody{Text: tideTask, Title: "The tide table"})
	rec = w.request("POST", api.Prefix+"chats/"+chatID+"/tasks", token, string(body), nil)
	var started jobs.Task
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &started) != nil || started.WorkChatID == "" {
		stop()
		return fmt.Errorf("the test tenant cannot start a background task: status %d: %s", rec.Code, rec.Body.String())
	}
	select {
	case <-called:
	case <-time.After(tasksWaitUp):
		stop()
		return fmt.Errorf("the background task never called %s", tideTool)
	}
	stop() // as a server that stops while the task's round runs
	var problems []string
	var status string
	var rounds int
	var requestID string
	if err := s.DB().QueryRow("SELECT status, rounds, request_id FROM jobs WHERE id = ?", started.ID).Scan(&status, &rounds, &requestID); err != nil {
		return err
	}
	if status != jobs.StatusRunning || rounds != 1 {
		problems = append(problems, fmt.Sprintf("the stopped server left the task %s after %d rounds, and it must be running in its first", status, rounds))
	}

	// 2. The second server resumes it.
	w, stop, token, err = start()
	if err != nil {
		return err
	}
	defer stop()
	base := api.Prefix + "chats/" + chatID + "/tasks/" + started.ID
	var end jobs.Task
	deadline := time.Now().Add(tasksWaitUp)
	for {
		rec := w.request("GET", base, token, "", nil)
		if json.Unmarshal(rec.Body.Bytes(), &end) == nil && end.Status != jobs.StatusQueued && end.Status != jobs.StatusRunning && end.NoteMessageID != 0 {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("after a restart the background task is %s, with its note %d, and it must finish: %s", end.Status, end.NoteMessageID, strings.Join(problems, "; "))
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 3. Where it ended, and what it left.
	if end.Status != jobs.StatusDone || end.Rounds != 2 || !strings.HasPrefix(end.Result, tideResult) {
		problems = append(problems, fmt.Sprintf("after a restart the background task ended as %s after %d rounds with %q", end.Status, end.Rounds, end.Result))
	}
	var parent turn.ChatReply
	if json.Unmarshal(w.request("GET", api.Prefix+"chats/"+chatID, token, "", nil).Body.Bytes(), &parent) != nil || len(parent.Messages) == 0 {
		problems = append(problems, "the chat the task was started from cannot be read")
	} else if note := parent.Messages[len(parent.Messages)-1]; note.ID != end.NoteMessageID || note.StopReason != turn.StopTask ||
		note.Role != "assistant" || len(note.Blocks) == 0 || !strings.Contains(note.Blocks[0].Text, tideResult) {
		problems = append(problems, fmt.Sprintf("the task's result is not the last message of the chat it was started from: %+v", note))
	}
	var work turn.ChatReply
	if json.Unmarshal(w.request("GET", api.Prefix+"chats/"+started.WorkChatID, token, "", nil).Body.Bytes(), &work) != nil || len(work.Messages) < 3 {
		problems = append(problems, "the task's own chat does not hold its rounds")
	}
	var interrupted, done int
	if err := s.DB().QueryRow(`SELECT count(*) FILTER (WHERE status = 'failed' AND error LIKE '%interrupted%'), count(*) FILTER (WHERE status = 'done')
		FROM turns WHERE chat_id = ? AND request_id = ?`, started.WorkChatID, requestID).Scan(&interrupted, &done); err != nil {
		return err
	}
	if interrupted != 1 || done != 1 {
		problems = append(problems, fmt.Sprintf("the task's rounds are %d interrupted and %d done, and must be one of each", interrupted, done))
	}
	if n := calls.Load(); n != 1 {
		problems = append(problems, fmt.Sprintf("%s ran %d times, and the round that was cut short ran it once", tideTool, n))
	}

	// Recorded, under the task's own request id, on both sides of the restart.
	entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID})
	if err != nil {
		return err
	}
	var modelStarts, modelEnds, toolStarts int
	for _, e := range entries {
		switch {
		case e.Kind == audit.KindModel && e.Outcome == audit.OutcomeStarted:
			modelStarts++
		case e.Kind == audit.KindModel:
			modelEnds++
		case e.Kind == audit.KindTool && e.Action == tideTool && e.Outcome == audit.OutcomeStarted:
			toolStarts++
		}
	}
	if modelStarts != 2 || modelEnds != 2 || toolStarts != 1 {
		problems = append(problems, fmt.Sprintf("the task's audit log holds %d model calls started, %d ended and %d calls of %s, and must hold one model call before the restart and one after, and the tool", modelStarts, modelEnds, toolStarts, tideTool))
	}
	records, err := usage.List(context.Background(), s, usage.Filter{RequestID: requestID})
	if err != nil {
		return err
	}
	if len(records) != 2 {
		problems = append(problems, fmt.Sprintf("the task's model calls have %d usage records under its request id, and must have two", len(records)))
	}

	// Another tenant reaches none of it.
	otherToken, rec := w.signIn(other.ID, walkPasswordTwo)
	if otherToken == "" {
		return fmt.Errorf("the ordinary tenant cannot sign in: status %d", rec.Code)
	}
	for _, call := range []struct{ method, path string }{
		{"GET", base}, {"GET", api.Prefix + "chats/" + chatID + "/tasks"}, {"POST", base + "/stop"}, {"GET", base + "/events"},
	} {
		if rec := w.request(call.method, call.path, otherToken, "", nil); rec.Code != http.StatusNotFound {
			problems = append(problems, fmt.Sprintf("another tenant's %s %s answered %d", call.method, call.path, rec.Code))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}
