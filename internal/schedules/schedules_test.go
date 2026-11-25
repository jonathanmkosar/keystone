package schedules

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// The item's proof: a daily schedule whose due time passes while the
// server is stopped fires once, and only once, when it starts again, marked
// late, and its run is a chat with the result.
func TestAMissedRunHappensOnceAfterAnOutage(t *testing.T) {
	g := newRig(t, utc(t, "2026-11-24T09:00:00Z"))
	g.set(g.test.ID, KeyTimeZone, "Europe/Lisbon")
	lisbon := zone(t, "Europe/Lisbon")
	provider := harbourScripts(t)

	sv := g.serve(provider)
	made := sv.make(g.test.ID, daily("Harbour note", harbourPrompt, "07:00"))
	if made.Next == nil || !made.Next.Equal(at(t, lisbon, "2026-11-25 07:00")) || made.Says != "Every day at 07:00" {
		t.Fatalf("a new daily schedule is next due at %v, saying %q", made.Next, made.Says)
	}
	if made.Next.Format(time.RFC3339) != "2026-11-25T07:00:00Z" {
		t.Fatalf("the next due time is sent as %s, in the person's zone", made.Next.Format(time.RFC3339))
	}
	paused := sv.make(g.test.ID, ScheduleBody{Name: "Paused note", Prompt: harbourPrompt, Recurrence: Recurrence{Kind: KindDaily, Time: "07:00"}, Enabled: new(bool)})
	sv.ticks(3)
	if runs := sv.read(g.test.ID, made.ID).Runs; len(runs) != 0 {
		t.Fatalf("a schedule ran %d times before it was due", len(runs))
	}
	sv.stop()

	// Three mornings pass while the server is stopped.
	g.clock.set(utc(t, "2026-11-27T08:12:00Z"))
	sv = g.serve(provider)
	got := sv.settle(g.test.ID, made.ID, 1)
	run := got.Runs[0]
	switch {
	case run.Trigger != CauseSchedule || !run.Late || run.LateReason != LateStopped || run.Missed != 3:
		t.Errorf("the missed run is %s, late %v (%q), standing for %d due times", run.Trigger, run.Late, run.LateReason, run.Missed)
	case !run.Due.Equal(at(t, lisbon, "2026-11-27 07:00")) || !run.Started.Equal(utc(t, "2026-11-27T08:12:00Z")):
		t.Errorf("the missed run was for %s and started at %s", run.Due, run.Started)
	case run.Status != jobs.StatusDone || run.ChatID == "" || run.TaskID == "" || run.Steps != 1:
		t.Errorf("the missed run ended as %s in chat %q with task %q after %d steps", run.Status, run.ChatID, run.TaskID, run.Steps)
	}
	if got.Schedule.Next == nil || !got.Schedule.Next.Equal(at(t, lisbon, "2026-11-28 07:00")) {
		t.Errorf("after the missed run the schedule is next due at %v", got.Schedule.Next)
	}
	if got.Schedule.LastRun == nil || got.Schedule.LastRun.ID != run.ID {
		t.Errorf("the schedule's last run is %+v", got.Schedule.LastRun)
	}

	// The run is a chat, titled by the schedule and the date, with the
	// prompt and the reply.
	c, msgs := sv.messages(g.test.ID, run.ChatID)
	if c.Title != "Harbour note, Fri 27 Nov 2026" {
		t.Errorf("the run's chat is titled %q", c.Title)
	}
	if len(msgs) != 2 || text(msgs[0]) != harbourPrompt || text(msgs[1]) != harbourReply || msgs[1].StopReason == turn.StopTask {
		t.Errorf("the run's chat holds %d messages: %+v", len(msgs), msgs)
	}
	var task jobs.Task
	g.with(g.test.ID, func(s *tenant.Store) {
		var err error
		if task, err = jobs.Read(context.Background(), s, run.TaskID); err != nil {
			t.Fatal(err)
		}
	})
	if task.ChatID != run.ChatID || task.WorkChatID != run.ChatID || task.Result != harbourReply || task.NoteMessageID != msgs[1].ID {
		t.Errorf("the run's task is %+v, and its result is the chat's reply %d", task, msgs[1].ID)
	}
	var listed []turn.Chat
	var reply turn.ListReply
	sv.call(g.test.ID, "GET", "chats", nil, http.StatusOK, &reply)
	for _, ch := range reply.Chats {
		if strings.HasPrefix(ch.Title, "Harbour note") {
			listed = append(listed, ch)
		}
	}
	if len(listed) != 1 {
		t.Errorf("the chats list holds %d chats of the schedule's", len(listed))
	}

	// Recorded as the schedule's, under the task's own request id.
	g.with(g.test.ID, func(s *tenant.Store) {
		var requestID string
		if err := s.DB().QueryRow("SELECT request_id FROM jobs WHERE id = ?", run.TaskID).Scan(&requestID); err != nil {
			t.Fatal(err)
		}
		entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID})
		if err != nil {
			t.Fatal(err)
		}
		models := 0
		for _, e := range entries {
			if e.Kind == audit.KindModel && e.Outcome != audit.OutcomeStarted && e.Actor == ActorSchedule {
				models++
			}
		}
		if models != 1 {
			t.Errorf("the run's model call is in the audit log %d times as the schedule's: %+v", models, entries)
		}
	})

	// Once, and only once: more looks, and another start, run nothing more.
	sv.ticks(5)
	if runs := sv.read(g.test.ID, made.ID).Runs; len(runs) != 1 {
		t.Fatalf("after more looks the schedule has %d runs", len(runs))
	}
	sv.stop()
	sv = g.serve(provider)
	sv.ticks(5)
	if runs := sv.read(g.test.ID, made.ID).Runs; len(runs) != 1 {
		t.Fatalf("after another start the schedule has %d runs", len(runs))
	}
	// A paused schedule did not fire.
	if got := sv.read(g.test.ID, paused.ID); len(got.Runs) != 0 || got.Schedule.Next != nil || got.Schedule.Enabled {
		t.Fatalf("a paused schedule has %d runs and is next due at %v", len(got.Runs), got.Schedule.Next)
	}

	// The next morning comes while it runs: one run, on time.
	g.clock.set(utc(t, "2026-11-28T07:00:20Z"))
	sv.sched.Wake()
	got = sv.settle(g.test.ID, made.ID, 2)
	if r := got.Runs[0]; r.Late || r.Missed != 1 || !r.Due.Equal(at(t, lisbon, "2026-11-28 07:00")) {
		t.Fatalf("the run on time is late %v, for %d, at %s", r.Late, r.Missed, r.Due)
	}
}

// The switch holds every schedule, and the times that pass while it is on
// are not made up. A paused schedule resumed is not made up either.
func TestTheSwitchHoldsEveryScheduleAndNothingIsMadeUp(t *testing.T) {
	g := newRig(t, utc(t, "2026-11-24T09:00:00Z"))
	g.set(g.test.ID, KeyTimeZone, "Europe/Lisbon")
	lisbon := zone(t, "Europe/Lisbon")
	provider := harbourScripts(t)
	sv := g.serve(provider)
	one := sv.make(g.test.ID, daily("Harbour note", harbourPrompt, "07:00"))
	two := sv.make(g.test.ID, ScheduleBody{Name: "Every hour", Prompt: harbourPrompt, Recurrence: Recurrence{Kind: KindHourly, Minute: 30}})

	var list SchedulesReply
	sv.call(g.test.ID, "POST", "schedules/pause-all", nil, http.StatusOK, &list)
	if !list.Paused || len(list.Schedules) != 2 || !list.Schedules[0].Held || list.Schedules[0].Next != nil {
		t.Fatalf("with the switch on the list is %+v", list)
	}
	// Two days pass, held, and a restart besides.
	sv.stop()
	g.clock.set(utc(t, "2026-11-26T10:10:00Z"))
	sv = g.serve(provider)
	sv.ticks(3)
	for _, id := range []string{one.ID, two.ID} {
		if runs := sv.read(g.test.ID, id).Runs; len(runs) != 0 {
			t.Fatalf("a held schedule ran %d times", len(runs))
		}
	}
	sv.call(g.test.ID, "POST", "schedules/resume-all", nil, http.StatusOK, &list)
	if list.Paused || list.Schedules[0].Next == nil || !list.Schedules[0].Next.Equal(at(t, lisbon, "2026-11-27 07:00")) ||
		!list.Schedules[1].Next.Equal(at(t, lisbon, "2026-11-26 10:30")) {
		t.Fatalf("after the switch went off the list is %+v", list)
	}
	sv.ticks(3)
	for _, id := range []string{one.ID, two.ID} {
		if runs := sv.read(g.test.ID, id).Runs; len(runs) != 0 {
			t.Fatalf("a time that passed while held was made up: %d runs", len(runs))
		}
	}

	// Paused one by one, and resumed: the same.
	var s Schedule
	sv.call(g.test.ID, "POST", "schedules/"+one.ID+"/pause", nil, http.StatusOK, &s)
	if s.Enabled || s.Next != nil {
		t.Fatalf("a paused schedule is %+v", s)
	}
	g.clock.set(utc(t, "2026-11-28T09:00:00Z"))
	sv.ticks(3)
	sv.call(g.test.ID, "POST", "schedules/"+one.ID+"/resume", nil, http.StatusOK, &s)
	if !s.Enabled || s.Next == nil || !s.Next.Equal(at(t, lisbon, "2026-11-29 07:00")) {
		t.Fatalf("a resumed schedule is %+v", s)
	}
	sv.ticks(3)
	if runs := sv.read(g.test.ID, one.ID).Runs; len(runs) != 0 {
		t.Fatalf("a time that passed while paused was made up: %d runs", len(runs))
	}
}

// Run now runs a schedule in a chat of its own, paused or not, and is
// refused while a run of it is under way. A due time that comes while a run
// is under way waits for it, and then runs once, late.
func TestADueTimeWaitsForTheRunBeforeAndRunNowIsRefusedMeanwhile(t *testing.T) {
	g := newRig(t, utc(t, "2026-11-25T10:00:00Z"))
	g.set(g.test.ID, KeyTimeZone, "Europe/Lisbon")
	lisbon := zone(t, "Europe/Lisbon")
	provider := newGated(t)
	sv := g.serve(provider)
	made := sv.make(g.test.ID, ScheduleBody{Name: "Tide gauge", Prompt: gatedPrompt, Recurrence: Recurrence{Kind: KindHourly, Minute: 5}, Enabled: new(bool)})

	var run Run
	sv.call(g.test.ID, "POST", "schedules/"+made.ID+"/run", nil, http.StatusCreated, &run)
	if run.Trigger != CauseNow || run.Missed != 0 || run.Late || run.ChatID == "" {
		t.Fatalf("a run now is %+v", run)
	}
	select {
	case <-provider.arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the run's model call never came")
	}
	resp := sv.request(g.test.ID, "POST", "schedules/"+made.ID+"/run", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a second run now while one is under way answered %d", resp.StatusCode)
	}
	if c, _ := sv.messages(g.test.ID, run.ChatID); c.Title != "Tide gauge, Wed 25 Nov 2026 10:00" {
		t.Fatalf("a run now's chat is titled %q", c.Title)
	}

	// Resumed, it is due at 10:05; two due times pass while the run goes on.
	sv.call(g.test.ID, "POST", "schedules/"+made.ID+"/resume", nil, http.StatusOK, nil)
	g.clock.set(utc(t, "2026-11-25T11:06:00Z"))
	sv.ticks(3)
	if runs := sv.read(g.test.ID, made.ID).Runs; len(runs) != 1 {
		t.Fatalf("a due time while a run was under way started another: %d runs", len(runs))
	}
	close(provider.open)
	got := sv.settle(g.test.ID, made.ID, 2)
	late := got.Runs[0]
	if late.Trigger != CauseSchedule || !late.Late || late.LateReason != LateBusy || late.Missed != 2 || !late.Due.Equal(at(t, lisbon, "2026-11-25 11:05")) {
		t.Fatalf("the run that waited is %+v", late)
	}
	if prev := got.Runs[1]; prev.Status != jobs.StatusDone || prev.Began == nil || prev.Ended == nil {
		t.Fatalf("the run now ended as %+v", prev)
	}
}

// A run that cannot finish says why in its chat: the ordinary person has no
// model, so the run's turn fails and the note says so.
func TestARunThatFailsSaysWhyInItsChat(t *testing.T) {
	g := newRig(t, utc(t, "2026-11-25T10:00:00Z"))
	sv := g.serve(harbourScripts(t))
	made := sv.make(g.ordinary.ID, daily("No model note", harbourPrompt, "07:00"))
	var run Run
	sv.call(g.ordinary.ID, "POST", "schedules/"+made.ID+"/run", nil, http.StatusCreated, &run)
	got := sv.settle(g.ordinary.ID, made.ID, 1)
	if r := got.Runs[0]; r.Status != jobs.StatusFailed || r.Error == "" {
		t.Fatalf("a run with no model ended as %+v", r)
	}
	_, msgs := sv.messages(g.ordinary.ID, run.ChatID)
	last := msgs[len(msgs)-1]
	if last.StopReason != turn.StopTask || !strings.HasPrefix(text(last), "“No model note” did not finish.") {
		t.Fatalf("the failed run's chat ends with %q (%s)", text(last), last.StopReason)
	}
}

// Every route is the person's own: another person's session reaches none of
// a schedule, and their list is empty.
func TestAnotherPersonReachesNoneOfASchedule(t *testing.T) {
	g := newRig(t, utc(t, "2026-11-25T10:00:00Z"))
	sv := g.serve(harbourScripts(t))
	made := sv.make(g.test.ID, daily("Harbour note", harbourPrompt, "07:00"))
	var list SchedulesReply
	sv.call(g.ordinary.ID, "GET", "schedules", nil, http.StatusOK, &list)
	if len(list.Schedules) != 0 {
		t.Fatalf("another person lists %d schedules", len(list.Schedules))
	}
	body := `{"name": "Taken", "prompt": "x", "recurrence": {"kind": "daily", "time": "07:00"}}`
	for _, c := range []struct{ method, path, body string }{
		{"GET", "schedules/" + made.ID, ""},
		{"PUT", "schedules/" + made.ID, body},
		{"DELETE", "schedules/" + made.ID, ""},
		{"POST", "schedules/" + made.ID + "/pause", ""},
		{"POST", "schedules/" + made.ID + "/resume", ""},
		{"POST", "schedules/" + made.ID + "/run", ""},
	} {
		resp := sv.request(g.ordinary.ID, c.method, c.path, c.body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("another person's %s %s answered %d", c.method, c.path, resp.StatusCode)
		}
	}
	got := sv.read(g.test.ID, made.ID)
	if got.Schedule.Name != "Harbour note" || !got.Schedule.Enabled || len(got.Runs) != 0 {
		t.Fatalf("after another person's calls the schedule is %+v", got)
	}
}

func TestASchedulesBodyIsChecked(t *testing.T) {
	g := newRig(t, utc(t, "2026-11-25T10:00:00Z"))
	g.config.MaxSchedules = 2
	g.set(g.test.ID, KeyTimeZone, "Europe/Lisbon")
	sv := g.serve(harbourScripts(t))
	for _, c := range []struct {
		why  string
		body ScheduleBody
	}{
		{"no name", ScheduleBody{Prompt: "x", Recurrence: Recurrence{Kind: KindDaily, Time: "07:00"}}},
		{"two lines of name", ScheduleBody{Name: "a\nb", Prompt: "x", Recurrence: Recurrence{Kind: KindDaily, Time: "07:00"}}},
		{"no prompt", ScheduleBody{Name: "a", Recurrence: Recurrence{Kind: KindDaily, Time: "07:00"}}},
		{"no time", ScheduleBody{Name: "a", Prompt: "x", Recurrence: Recurrence{Kind: KindDaily}}},
		{"a weekly one with no day", ScheduleBody{Name: "a", Prompt: "x", Recurrence: Recurrence{Kind: KindWeekly, Time: "07:00"}}},
		{"a time that has passed", ScheduleBody{Name: "a", Prompt: "x", Recurrence: Recurrence{Kind: KindOnce, Time: "09:00", Date: "2026-11-25"}}},
		{"a model not in the registry", ScheduleBody{Name: "a", Prompt: "x", Model: "no-such-model", Recurrence: Recurrence{Kind: KindDaily, Time: "07:00"}}},
		{"a prompt too long", ScheduleBody{Name: "a", Prompt: strings.Repeat("x", g.config.PromptMaxChars+1), Recurrence: Recurrence{Kind: KindDaily, Time: "07:00"}}},
	} {
		resp := sv.request(g.test.ID, "POST", "schedules", mustJSON(t, c.body))
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("a schedule with %s answered %d", c.why, resp.StatusCode)
		}
	}
	once := sv.make(g.test.ID, ScheduleBody{Name: "Once", Prompt: "x", Recurrence: Recurrence{Kind: KindOnce, Time: "11:30", Date: "2026-11-25"}})
	if once.Next == nil || !once.Next.Equal(utc(t, "2026-11-25T11:30:00Z")) || once.Says != "Once, on Wed 25 Nov 2026 at 11:30" {
		t.Fatalf("a one-time schedule is %+v", once)
	}
	sv.make(g.test.ID, daily("Second", "x", "07:00"))
	resp := sv.request(g.test.ID, "POST", "schedules", mustJSON(t, daily("Third", "x", "07:00")))
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("one schedule too many answered %d", resp.StatusCode)
	}

	// A change replaces every field, and works out the next due time again.
	var changed Schedule
	sv.call(g.test.ID, "PUT", "schedules/"+once.ID, ScheduleBody{Name: "Weekly now", Prompt: "y",
		Recurrence: Recurrence{Kind: KindWeekly, Time: "08:00", Days: []string{"sat"}}, Incognito: true}, http.StatusOK, &changed)
	if changed.Name != "Weekly now" || changed.Prompt != "y" || !changed.Incognito || changed.Says != "Saturdays at 08:00" ||
		changed.Next == nil || !changed.Next.Equal(utc(t, "2026-11-28T08:00:00Z")) {
		t.Fatalf("a changed schedule is %+v", changed)
	}
	var deleted DeletedReply
	sv.call(g.test.ID, "DELETE", "schedules/"+once.ID, nil, http.StatusOK, &deleted)
	if resp := sv.request(g.test.ID, "GET", "schedules/"+once.ID, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a deleted schedule answered %d", resp.StatusCode)
	}
}

// A change of the person's time zone moves every next due time to the same
// time of day in the new zone, and runs nothing. A name that is no zone is
// said, and the machine's zone is used.
func TestAChangeOfTimeZoneMovesTheNextDueTime(t *testing.T) {
	g := newRig(t, utc(t, "2026-11-25T10:00:00Z"))
	g.set(g.test.ID, KeyTimeZone, "Europe/Lisbon")
	sv := g.serve(harbourScripts(t))
	made := sv.make(g.test.ID, daily("Harbour note", harbourPrompt, "07:00"))
	g.set(g.test.ID, KeyTimeZone, "America/Chicago")
	got := sv.read(g.test.ID, made.ID)
	if got.Zone.Name != "America/Chicago" || got.Schedule.Next == nil || !got.Schedule.Next.Equal(utc(t, "2026-11-25T13:00:00Z")) {
		t.Fatalf("after a change of zone the schedule is next due at %v in %+v", got.Schedule.Next, got.Zone)
	}
	if got.Schedule.Next.Format("15:04") != "07:00" {
		t.Fatalf("the next due time is sent as %s on Chicago's clock", got.Schedule.Next.Format("15:04"))
	}
	sv.ticks(3)
	g.with(g.test.ID, func(s *tenant.Store) {
		var next, z string
		if err := s.DB().QueryRow("SELECT next_at, zone FROM schedules WHERE id = ?", made.ID).Scan(&next, &z); err != nil {
			t.Fatal(err)
		}
		if next != "2026-11-25T13:00:00.000Z" || z != "America/Chicago" {
			t.Fatalf("the stored next due time is %s in %q", next, z)
		}
	})
	if runs := sv.read(g.test.ID, made.ID).Runs; len(runs) != 0 {
		t.Fatalf("a change of zone ran the schedule %d times", len(runs))
	}
	g.set(g.test.ID, KeyTimeZone, "Atlantis/Harbour")
	var list SchedulesReply
	sv.call(g.test.ID, "GET", "schedules", nil, http.StatusOK, &list)
	if list.Zone.Name != "" || list.Zone.Problem == "" || !strings.HasPrefix(list.Zone.Label, "This computer's time zone") {
		t.Fatalf("a zone that is no zone reads as %+v", list.Zone)
	}
}

// An incognito schedule's runs are incognito chats, and deleting a schedule
// keeps its runs' chats.
func TestAnIncognitoSchedulesRunsAreIncognitoAndOutliveIt(t *testing.T) {
	g := newRig(t, utc(t, "2026-11-25T10:00:00Z"))
	sv := g.serve(harbourScripts(t))
	made := sv.make(g.test.ID, ScheduleBody{Name: "Quiet note", Prompt: harbourPrompt, Recurrence: Recurrence{Kind: KindDaily, Time: "07:00"}, Incognito: true})
	var run Run
	sv.call(g.test.ID, "POST", "schedules/"+made.ID+"/run", nil, http.StatusCreated, &run)
	sv.settle(g.test.ID, made.ID, 1)
	g.with(g.test.ID, func(s *tenant.Store) {
		on, err := memory.Incognito(context.Background(), s, run.ChatID)
		if err != nil || !on {
			t.Fatalf("an incognito schedule's run has a chat that is incognito %v (%v)", on, err)
		}
	})
	sv.call(g.test.ID, "DELETE", "schedules/"+made.ID, nil, http.StatusOK, nil)
	g.with(g.test.ID, func(s *tenant.Store) {
		if _, err := chat.Get(s, run.ChatID); err != nil {
			t.Fatalf("deleting a schedule took its run's chat: %v", err)
		}
		var n int
		if err := s.DB().QueryRow("SELECT count(*) FROM schedule_runs").Scan(&n); err != nil || n != 0 {
			t.Fatalf("deleting a schedule left %d runs (%v)", n, err)
		}
	})
}

// A due time is run once even when the scheduler is asked twice: the
// database refuses a second run for it.
func TestTheDatabaseRefusesASecondRunForOneDueTime(t *testing.T) {
	g := newRig(t, utc(t, "2026-11-25T10:00:00Z"))
	g.with(g.test.ID, func(s *tenant.Store) {
		for i, id := range []string{"aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"} {
			if i == 0 {
				if _, err := s.DB().Exec(`INSERT INTO schedules(id, name, prompt, kind, created_at, updated_at) VALUES('s1', 'n', 'p', 'daily', 'x', 'x')`); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.DB().Exec(`INSERT INTO schedule_runs(id, schedule_id, cause, due_at, state, request_id, actor, created_at)
				VALUES(?, 's1', 'schedule', '2026-11-25T07:00:00.000Z', 'starting', 'r', 'schedule', 'x')`, id)
			if (err == nil) != (i == 0) {
				t.Fatalf("run %d for the same due time: %v", i+1, err)
			}
		}
	})
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := jsonMarshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
