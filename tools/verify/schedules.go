package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// The standing check "scheduled tasks" (P8-03). The plan's words: a prompt
// on a schedule; each run is a chat turn; a missed run happens once when
// the machine is back. The item's proof: a missed run fires once after a
// simulated outage; runs appear as chats.
//
// On the real server, put together as internal/app puts it, with the
// shipped settings but a scheduler that looks every few milliseconds, as
// the test tenant, whose model is its recorded turns
// (fixtures/test-tenant/turns/scheduled-task.json), and with the server's
// clock set by the check:
//
//  1. The person's zone is Europe/Lisbon. A daily schedule at 07:00 and a
//     paused one are made on the 24th, and the server is stopped.
//  2. Three mornings pass. A second server starts one run, and only one,
//     marked late because Keystone was not running, standing for three due
//     times; its chat is titled by the schedule and the date and holds the
//     prompt, the run's step and its result; its calls are recorded as the
//     schedule's under the run's own request id; the schedule is next due
//     the morning after. The paused schedule has not run.
//  3. More looks and a third start run nothing more.
//  4. The switch holds every schedule over another stop, and nothing is
//     made up when it goes off.
//  5. The next due time is right across a change of the clocks, worked out
//     alone and through the route.
//  6. Another tenant reaches none of it.
//
// The schedule routes are walked with every other route by the tenant
// isolation, calls recorded and no keys in output checks (routes.go).

func init() {
	componentDocs["internal/schedules"] = "docs/architecture/schedules.md"
	networkAllowed["internal/schedules"] = "the schedule routes' handlers, served by the server; it opens no connection"
}

const (
	briefPrompt     = "Write the morning harbour brief: today's date, and what my chats say about the lighthouse walk."
	briefResult     = "Good morning. The harbour brief:"
	schedulesWaitUp = 10 * time.Second
)

// checkClock is the server's clock in the check.
type checkClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *checkClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *checkClock) set(text string) {
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		panic(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = at
}

func checkSchedules(string) error {
	dir, err := os.MkdirTemp("", "keystone-schedules-")
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
	clock := &checkClock{}
	clock.set("2026-11-24T09:00:00Z")
	start := func() (*walker, func(), string, error) {
		c, err := app.Defaults()
		if err != nil {
			return nil, nil, "", err
		}
		w := &walker{log: &bytes.Buffer{}}
		c.Install, c.Log, c.Clock = in, w.log, clock.now
		c.Schedules.Poll = 10 * time.Millisecond
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
	call := func(w *walker, token, method, path string, body any, want int, out any) error {
		text := ""
		if body != nil {
			raw, _ := json.Marshal(body)
			text = string(raw)
		}
		rec := w.request(method, api.Prefix+path, token, text, nil)
		if rec.Code != want {
			return fmt.Errorf("%s %s answered %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		if out != nil {
			if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
				return fmt.Errorf("%s %s: %v", method, path, err)
			}
		}
		return nil
	}
	runsOf := func(w *walker, token, id string) ([]schedules.Run, error) {
		var got schedules.ScheduleReply
		err := call(w, token, "GET", "schedules/"+id, nil, http.StatusOK, &got)
		return got.Runs, err
	}
	// looks lets the scheduler look many times.
	looks := func() { time.Sleep(300 * time.Millisecond) }
	var problems []string

	// 1. The first server: the zone and two schedules.
	w, stop, token, err := start()
	if err != nil {
		return err
	}
	if err := call(w, token, "PUT", "settings/"+schedules.KeyTimeZone, map[string]string{"value": "Europe/Lisbon"}, http.StatusOK, nil); err != nil {
		stop()
		return err
	}
	var brief, paused schedules.Schedule
	if err := call(w, token, "POST", "schedules", schedules.ScheduleBody{Name: "Harbour brief", Prompt: briefPrompt,
		Recurrence: schedules.Recurrence{Kind: schedules.KindDaily, Time: "07:00"}}, http.StatusCreated, &brief); err != nil {
		stop()
		return err
	}
	off := false
	if err := call(w, token, "POST", "schedules", schedules.ScheduleBody{Name: "Paused brief", Prompt: briefPrompt, Enabled: &off,
		Recurrence: schedules.Recurrence{Kind: schedules.KindDaily, Time: "07:00"}}, http.StatusCreated, &paused); err != nil {
		stop()
		return err
	}
	if brief.Next == nil || brief.Next.Format(time.RFC3339) != "2026-11-25T07:00:00Z" {
		problems = append(problems, fmt.Sprintf("a daily schedule at 07:00 in Lisbon made on the 24th is next due at %v", brief.Next))
	}
	looks()
	if runs, err := runsOf(w, token, brief.ID); err != nil || len(runs) != 0 {
		stop()
		return fmt.Errorf("a schedule ran %d times before it was due (%v)", len(runs), err)
	}
	stop()

	// 2. Three mornings pass while the server is stopped.
	clock.set("2026-11-27T08:12:00Z")
	w, stop, token, err = start()
	if err != nil {
		return err
	}
	var runs []schedules.Run
	var task jobs.Task
	deadline := time.Now().Add(schedulesWaitUp)
	for {
		if runs, err = runsOf(w, token, brief.ID); err != nil {
			stop()
			return err
		}
		if len(runs) > 0 && runs[0].TaskID != "" {
			if err := call(w, token, "GET", "chats/"+runs[0].ChatID+"/tasks/"+runs[0].TaskID, nil, http.StatusOK, &task); err != nil {
				stop()
				return err
			}
			if task.NoteMessageID != 0 {
				break
			}
		}
		if time.Now().After(deadline) {
			stop()
			return fmt.Errorf("after a restart past three due times the schedule has %d runs and none has ended: %+v", len(runs), runs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Read again, now that its task has ended.
	if runs, err = runsOf(w, token, brief.ID); err != nil || len(runs) == 0 {
		stop()
		return fmt.Errorf("the schedule's runs cannot be read again: %d, %v", len(runs), err)
	}
	run := runs[0]
	switch {
	case len(runs) != 1:
		problems = append(problems, fmt.Sprintf("after a restart past three due times the schedule has %d runs, and must have one", len(runs)))
	case run.Trigger != schedules.CauseSchedule || !run.Late || run.LateReason != schedules.LateStopped || run.Missed != 3:
		problems = append(problems, fmt.Sprintf("the missed run is %s, late %v (%q), for %d due times, and must be the schedule's, late because Keystone was not running, for three", run.Trigger, run.Late, run.LateReason, run.Missed))
	case run.Due.Format(time.RFC3339) != "2026-11-27T07:00:00Z" || run.Status != jobs.StatusDone:
		problems = append(problems, fmt.Sprintf("the missed run was for %s and ended as %s", run.Due.Format(time.RFC3339), run.Status))
	}
	var after schedules.ScheduleReply
	if err := call(w, token, "GET", "schedules/"+brief.ID, nil, http.StatusOK, &after); err != nil {
		stop()
		return err
	}
	if after.Schedule.Next == nil || after.Schedule.Next.Format(time.RFC3339) != "2026-11-28T07:00:00Z" {
		problems = append(problems, fmt.Sprintf("after the missed run the schedule is next due at %v, and must be the next morning", after.Schedule.Next))
	}
	// The run is a chat, with its result.
	var c turn.ChatReply
	if err := call(w, token, "GET", "chats/"+run.ChatID, nil, http.StatusOK, &c); err != nil {
		stop()
		return err
	}
	if c.Chat.Title != "Harbour brief, Fri 27 Nov 2026" {
		problems = append(problems, fmt.Sprintf("the run's chat is titled %q", c.Chat.Title))
	}
	if n := len(c.Messages); n < 3 || c.Messages[0].Role != "user" || len(c.Messages[0].Blocks) == 0 || c.Messages[0].Blocks[0].Text != briefPrompt ||
		c.Messages[n-1].ID != task.NoteMessageID || c.Messages[n-1].StopReason == turn.StopTask || !strings.HasPrefix(c.Messages[n-1].Blocks[0].Text, briefResult) {
		problems = append(problems, fmt.Sprintf("the run's chat does not hold the prompt, its step and its result: %+v", c.Messages))
	}
	var list turn.ListReply
	if err := call(w, token, "GET", "chats", nil, http.StatusOK, &list); err != nil {
		stop()
		return err
	}
	inList := 0
	for _, ch := range list.Chats {
		if ch.ID == run.ChatID {
			inList++
		}
	}
	if inList != 1 {
		problems = append(problems, "the run's chat is not in the list of chats")
	}
	// Recorded as the schedule's, under the run's own request id.
	var requestID string
	if err := s.DB().QueryRow("SELECT request_id FROM jobs WHERE id = ?", run.TaskID).Scan(&requestID); err != nil {
		stop()
		return err
	}
	entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID})
	if err != nil {
		stop()
		return err
	}
	var modelEnds, toolEnds int
	for _, e := range entries {
		if e.Actor != schedules.ActorSchedule || e.Outcome == audit.OutcomeStarted {
			continue
		}
		switch e.Kind {
		case audit.KindModel:
			modelEnds++
		case audit.KindTool:
			toolEnds++
		}
	}
	if modelEnds != 2 || toolEnds != 1 {
		problems = append(problems, fmt.Sprintf("the run's audit log holds %d model calls and %d tool calls as the schedule's, and must hold two and one", modelEnds, toolEnds))
	}
	if records, err := usage.List(context.Background(), s, usage.Filter{RequestID: requestID}); err != nil || len(records) != 2 {
		problems = append(problems, fmt.Sprintf("the run's model calls have %d usage records (%v), and must have two", len(records), err))
	}
	if got, err := runsOf(w, token, paused.ID); err != nil || len(got) != 0 {
		problems = append(problems, fmt.Sprintf("the paused schedule ran %d times (%v)", len(got), err))
	}

	// 3. Once, and only once.
	looks()
	if got, err := runsOf(w, token, brief.ID); err != nil || len(got) != 1 {
		problems = append(problems, fmt.Sprintf("after more looks the schedule has %d runs (%v)", len(got), err))
	}
	stop()
	w, stop, token, err = start()
	if err != nil {
		return err
	}
	looks()
	if got, err := runsOf(w, token, brief.ID); err != nil || len(got) != 1 {
		problems = append(problems, fmt.Sprintf("after a third start the schedule has %d runs (%v)", len(got), err))
	}

	// 4. The switch holds every schedule over another stop.
	var held schedules.SchedulesReply
	if err := call(w, token, "POST", "schedules/pause-all", nil, http.StatusOK, &held); err != nil {
		stop()
		return err
	}
	stop()
	clock.set("2026-11-29T08:12:00Z")
	w, stop, token, err = start()
	if err != nil {
		return err
	}
	defer func() { stop() }()
	looks()
	if got, err := runsOf(w, token, brief.ID); err != nil || len(got) != 1 {
		problems = append(problems, fmt.Sprintf("with the switch on, two mornings later, the schedule has %d runs (%v)", len(got), err))
	}
	if err := call(w, token, "POST", "schedules/resume-all", nil, http.StatusOK, &held); err != nil {
		return err
	}
	looks()
	if got, err := runsOf(w, token, brief.ID); err != nil || len(got) != 1 {
		problems = append(problems, fmt.Sprintf("after the switch went off a held time was made up: %d runs (%v)", len(got), err))
	}
	if len(held.Schedules) == 0 || held.Paused || held.Schedules[0].Next == nil || held.Schedules[0].Next.Format(time.RFC3339) != "2026-11-30T07:00:00Z" {
		problems = append(problems, fmt.Sprintf("after the switch went off the schedules read %+v", held))
	}

	// 5. Across a change of the clocks: alone, and through the route.
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		return err
	}
	daily := schedules.Recurrence{Kind: schedules.KindDaily, Time: "07:00"}
	for _, x := range []struct{ after, want string }{
		{"2026-10-30T12:00:00Z", "2026-10-31T11:00:00Z"}, // 07:00 EDT
		{"2026-10-31T11:00:00Z", "2026-11-01T12:00:00Z"}, // 07:00 EST, 25 hours later
		{"2027-03-13T12:00:00Z", "2027-03-14T11:00:00Z"}, // 07:00 EDT, 23 hours later
	} {
		from, _ := time.Parse(time.RFC3339, x.after)
		if got := schedules.Next(daily, from, ny); got.UTC().Format(time.RFC3339) != x.want {
			problems = append(problems, fmt.Sprintf("daily at 07:00 in New York after %s is next due at %s, and must be %s", x.after, got.UTC().Format(time.RFC3339), x.want))
		}
	}
	if got := schedules.Next(schedules.Recurrence{Kind: schedules.KindDaily, Time: "02:30"}, mustTime("2027-03-13T12:00:00Z"), ny); got.UTC().Format(time.RFC3339) != "2027-03-14T07:30:00Z" {
		problems = append(problems, fmt.Sprintf("02:30 in New York on the night the clocks skip it is due at %s, and must be 03:30 EDT", got))
	}
	otherToken, rec := w.signIn(other.ID, walkPasswordTwo)
	if otherToken == "" {
		return fmt.Errorf("the ordinary tenant cannot sign in: status %d", rec.Code)
	}
	clock.set("2026-10-31T12:00:00Z")
	if err := call(w, otherToken, "PUT", "settings/"+schedules.KeyTimeZone, map[string]string{"value": "America/New_York"}, http.StatusOK, nil); err != nil {
		return err
	}
	var across schedules.Schedule
	if err := call(w, otherToken, "POST", "schedules", schedules.ScheduleBody{Name: "Ferry check", Prompt: "Check the ferry.", Recurrence: daily}, http.StatusCreated, &across); err != nil {
		return err
	}
	if across.Next == nil || across.Next.Format(time.RFC3339) != "2026-11-01T07:00:00-05:00" {
		problems = append(problems, fmt.Sprintf("a daily schedule at 07:00 made in New York on 31 October is next due at %v, and must be 07:00 EST on 1 November", across.Next))
	}

	// 6. Another tenant reaches none of the test tenant's.
	var theirs schedules.SchedulesReply
	if err := call(w, otherToken, "GET", "schedules", nil, http.StatusOK, &theirs); err != nil {
		return err
	}
	if len(theirs.Schedules) != 1 || theirs.Schedules[0].ID != across.ID {
		problems = append(problems, fmt.Sprintf("another tenant lists %d schedules", len(theirs.Schedules)))
	}
	base := api.Prefix + "schedules/" + brief.ID
	for _, x := range []struct{ method, path string }{
		{"GET", base}, {"PUT", base}, {"DELETE", base}, {"POST", base + "/pause"}, {"POST", base + "/resume"}, {"POST", base + "/run"},
	} {
		body := ""
		if x.method == "PUT" {
			raw, _ := json.Marshal(schedules.ScheduleBody{Name: "Taken", Prompt: "x", Recurrence: daily})
			body = string(raw)
		}
		if rec := w.request(x.method, x.path, otherToken, body, nil); rec.Code != http.StatusNotFound {
			problems = append(problems, fmt.Sprintf("another tenant's %s %s answered %d", x.method, x.path, rec.Code))
		}
	}
	if got, err := runsOf(w, token, brief.ID); err != nil || len(got) != 1 {
		problems = append(problems, fmt.Sprintf("after another tenant's calls the schedule has %d runs (%v)", len(got), err))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

func mustTime(text string) time.Time {
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		panic(err)
	}
	return at
}
