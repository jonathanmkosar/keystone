package jobs

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// A task that works in the chat it was started from (P8-03): its rounds are
// the chat's turns, its result is the chat's reply and no note is written;
// a second start returns the first task.
func TestATaskInItsOwnChatHasItsReplyAsItsResult(t *testing.T) {
	g := newRig(t)
	sv := g.serve(scripted(t, taskScripts(t)))
	chatID := sv.newChat(g.test.ID)
	var task, again Task
	g.with(g.test.ID, func(s *tenant.Store) {
		var err error
		if task, err = sv.jobs.StartIn(context.Background(), s, chatID, "req-start", "schedule", StartBody{Text: lighthouseTask, Title: "Lighthouse"}, ""); err != nil {
			t.Fatal(err)
		}
		if again, err = sv.jobs.StartIn(context.Background(), s, chatID, "req-again", "schedule", StartBody{Text: lighthouseTask}, ""); err != nil {
			t.Fatal(err)
		}
	})
	if task.ChatID != chatID || task.WorkChatID != chatID || again.ID != task.ID {
		t.Fatalf("a task in its own chat is %+v, and a second start gave %s", task, again.ID)
	}
	_, end := sv.follow(g.test.ID, task)
	if end.Status != StatusDone || end.Result != lighthouseDone {
		t.Fatalf("the task ended as %s with %q", end.Status, end.Result)
	}
	msgs := sv.messages(g.test.ID, chatID)
	last := msgs[len(msgs)-1]
	if text(msgs[0]) != lighthouseTask || last.StopReason == turn.StopTask || text(last) != lighthouseDone || end.NoteMessageID != last.ID {
		t.Fatalf("the chat ends with %q (%s), and the task's result is message %d of %d", text(last), last.StopReason, end.NoteMessageID, last.ID)
	}
	var title string
	g.with(g.test.ID, func(s *tenant.Store) {
		got, err := Read(context.Background(), s, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		title = got.Title
		if err := s.DB().QueryRow("SELECT title FROM chats WHERE id = ?", chatID).Scan(&title); err != nil {
			t.Fatal(err)
		}
	})
	if strings.HasPrefix(title, workTitlePrefix) {
		t.Fatalf("a task in its own chat renamed it %q", title)
	}
}

// Stopped, a task in its own chat writes a note there that says so.
func TestATaskInItsOwnChatThatIsStoppedSaysSo(t *testing.T) {
	g := newRig(t)
	p := newSlow()
	sv := g.serve(p)
	chatID := sv.newChat(g.test.ID)
	var task Task
	g.with(g.test.ID, func(s *tenant.Store) {
		var err error
		if task, err = sv.jobs.StartIn(context.Background(), s, chatID, "req-start", "schedule", StartBody{Text: "Read the whole lighthouse log.", Title: "Log"}, ""); err != nil {
			t.Fatal(err)
		}
	})
	select {
	case <-p.writing:
	case <-chanAfter(t):
		t.Fatal("the task's round never began")
	}
	var stopped Task
	g.with(g.test.ID, func(s *tenant.Store) {
		var err error
		if stopped, err = sv.jobs.Stop(context.Background(), s, chatID, task.ID); err != nil {
			t.Fatal(err)
		}
	})
	_, end := sv.follow(g.test.ID, stopped)
	if end.Status != StatusStopped {
		t.Fatalf("the stopped task ended as %s", end.Status)
	}
	msgs := sv.messages(g.test.ID, chatID)
	last := msgs[len(msgs)-1]
	if last.StopReason != turn.StopTask || last.ID != end.NoteMessageID || text(last) != "“Log” was stopped before it finished. The steps above have what it did." {
		t.Fatalf("the chat ends with %q (%s)", text(last), last.StopReason)
	}
}
