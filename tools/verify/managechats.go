package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/uploads"
	"github.com/jonathanmkosar/keystone/settings"
)

// Managing many chats (P4-06): the table of every chat and the batch
// delete are files of the turn package and of the web interface with a doc
// of their own.
func init() {
	componentDocs["internal/turn/manage.go"] = "docs/architecture/manage-chats.md"
	componentDocs["internal/chat/bychat.go"] = "docs/architecture/manage-chats.md"
	componentDocs["web/app/src/ManageChats.tsx"] = "docs/architecture/manage-chats.md"
}

// The "manage chats" check (P4-06), the item's proof as a standing check.
// It runs the real server, with the shipped settings, on a throwaway
// folder, with two invented people and one local model the check answers
// for, which writes the start of a reply and then waits. It proves:
//
//  1. A search of the table finds a chat by a word that is only in one of
//     its messages, once, with the word marked in the snippet.
//  2. More chats than chats.delete_batch_max in one call is refused, and
//     nothing is deleted.
//  3. One call deletes the selected chats and nothing else: their rows and
//     their uploaded files go, a file a kept chat also holds stays, and the
//     chats that were not named are as they were.
//  4. Fixed rule 1: another person's chat id in the batch is refused, and
//     nothing of theirs changes: not their chats, their file or their audit
//     log.
//  5. A chat with a turn running is skipped and said so, its turn still
//     running afterwards.
//  6. Every chat of the batch is in the caller's audit log under the call's
//     request id.
//  7. The screen keeps its promises to a phone and to the person: no box
//     takes the focus by itself, its text boxes are 16px, and the
//     confirmation says there is no undo.

// Invented, for a throwaway folder only.
const (
	managePassword      = "an invented phrase for the manage chats check"
	manageOtherPassword = "another invented phrase for the manage chats check"
	manageMark          = "COBALT-PLOVER"
)

var manageModel = models.Model{
	Name: "manage-local", Label: "Manage Local", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8084/v1",
	ProviderModel: "small-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal,
}

// manageSlow writes the start of a reply and then waits until its call is
// cancelled, as a model does in the middle of a long answer.
type manageSlow struct {
	writing chan struct{}
}

func (p *manageSlow) build(models.Model, string) (seam.Provider, error) { return p, nil }

func (p *manageSlow) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	for _, e := range []seam.Event{
		{Type: seam.EventStart, Model: req.Model},
		{Type: seam.EventBlockStart, Index: 0, Block: &seam.Block{Type: seam.BlockText}},
		{Type: seam.EventBlockDelta, Index: 0, Text: "Half an ans"},
	} {
		if err := emit(e); err != nil {
			return err
		}
	}
	select {
	case p.writing <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func checkManageChats(root string) error {
	problems, err := manageProblems()
	if err != nil {
		return err
	}
	problems = append(problems, manageScreenProblems(root)...)
	problems = append(problems, manageSettingsProblems()...)
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

// manageSettingsProblems: the table and the batch delete take their limits
// from settings a person can find, each a whole number of 1 or more.
func manageSettingsProblems() []string {
	reg, err := settings.Registry()
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	for _, key := range []string{turn.KeyTablePageRows, turn.KeyTableSelectMax, turn.KeyDeleteBatchMax} {
		s, ok := settings.Lookup(reg, key)
		if n, err := strconv.Atoi(s.Default); !ok || s.Type != "int" || err != nil || n < 1 {
			problems = append(problems, "the setting "+key+" is missing from settings/chats.json, or its default is not a whole number of 1 or more")
		}
	}
	return problems
}

var (
	manageAutoFocus = regexp.MustCompile(`(?i)autofocus`)
	manageBoxRule   = regexp.MustCompile(`(?s)\.manage-(search|select)\s*\{[^}]*\}`)
)

// manageScreenProblems reads the screen's source for the promises a test in
// a browser would otherwise have to keep: on a touch screen no text box
// takes the focus by itself (that raises the keyboard, which once squashed
// a page), a text box is 16px (smaller, and a phone's browser zooms in),
// and the confirmation says plainly that there is no undo.
func manageScreenProblems(root string) []string {
	var problems []string
	src := filepath.Join(root, "web", "app", "src")
	screen, err := os.ReadFile(filepath.Join(src, "ManageChats.tsx"))
	if err != nil {
		return []string{"web/app/src/ManageChats.tsx, the Chats screen, cannot be read: " + err.Error()}
	}
	css, err := os.ReadFile(filepath.Join(src, "manage-chats.css"))
	if err != nil {
		return []string{"web/app/src/manage-chats.css, the Chats screen's styles, cannot be read: " + err.Error()}
	}
	// Comments may say the word; code may not.
	code := regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAll(screen, nil)
	if manageAutoFocus.Match(code) {
		problems = append(problems, "web/app/src/ManageChats.tsx gives a box the focus by itself (autoFocus): on a phone that raises the keyboard over the table")
	}
	if !bytes.Contains(screen, []byte("There is no undo")) {
		problems = append(problems, "the delete confirmation in web/app/src/ManageChats.tsx no longer says \"There is no undo\": no undo is built, and it must say so")
	}
	if !bytes.Contains(screen, []byte("visualViewport")) {
		problems = append(problems, "web/app/src/ManageChats.tsx no longer places its confirmation by the visible part of the screen (visualViewport): a phone's keyboard would cover it")
	}
	boxes := manageBoxRule.FindAll(css, -1)
	if len(boxes) < 2 {
		problems = append(problems, "web/app/src/manage-chats.css has no rule for .manage-search and .manage-select, the screen's text boxes")
	}
	for _, rule := range boxes {
		if !bytes.Contains(rule, []byte("16px")) {
			problems = append(problems, "a text box of the Chats screen is not 16px in web/app/src/manage-chats.css, so a phone's browser zooms the page when it takes the focus: "+strings.Join(strings.Fields(string(rule)), " "))
		}
	}
	return problems
}

func manageProblems() ([]string, error) {
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	ctx := context.Background()

	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "keystone-manage-chats-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	o, err := signin.Defaults()
	if err != nil {
		return nil, err
	}
	person, err := in.Create("Manage Check Person")
	if err != nil {
		return nil, err
	}
	other, err := in.Create("Manage Check Other Person")
	if err != nil {
		return nil, err
	}
	with := func(id string, f func(s *tenant.Store) error) error {
		s, err := in.Store(id)
		if err != nil {
			return err
		}
		defer s.Close()
		return f(s)
	}
	set := func(key, value string) error {
		return with(person.ID, func(s *tenant.Store) error {
			return tenantsettings.Set(ctx, s, reg, tenantsettings.Person, key, value)
		})
	}
	err = with(person.ID, func(s *tenant.Store) error {
		if err := signin.SetPassword(s, managePassword, o); err != nil {
			return err
		}
		lim, err := adapters.Limits(reg)
		if err != nil {
			return err
		}
		return models.Add(ctx, s, lim, manageModel)
	})
	if err != nil {
		return nil, err
	}
	for key, value := range map[string]string{turn.KeyTitleAuto: "false", turn.KeyRecap: "false", memory.SettingOn: "false"} {
		if err := set(key, value); err != nil {
			return nil, err
		}
	}
	if err := with(other.ID, func(s *tenant.Store) error { return signin.SetPassword(s, manageOtherPassword, o) }); err != nil {
		return nil, err
	}

	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	slow := &manageSlow{writing: make(chan struct{}, 1)}
	c.Install, c.Log = in, w.log
	c.Adapters = adapters.Set{models.AdapterChatCompletions: slow.build}
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	defer stop()
	token, rec := w.signIn(person.ID, managePassword)
	if token == "" {
		return nil, fmt.Errorf("the invented person cannot sign in through the API: status %d", rec.Code)
	}
	otherToken, rec := w.signIn(other.ID, manageOtherPassword)
	if otherToken == "" {
		return nil, fmt.Errorf("the other invented person cannot sign in through the API: status %d", rec.Code)
	}
	route := func(method, tok, path, body string, want int, out any) error {
		rec := w.request(method, api.Prefix+path, tok, body, nil)
		if rec.Code != want {
			return fmt.Errorf("%s %s answered %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		if out != nil {
			return json.Unmarshal(rec.Body.Bytes(), out)
		}
		return nil
	}
	lim, err := chat.DefaultLimits()
	if err != nil {
		return nil, err
	}
	// newChat makes a chat through the API and writes its title and its
	// messages in the chat record, a person's and a reply by turns.
	newChat := func(tenantID, tok, title string, messages ...string) (string, error) {
		var made turn.ChatReply
		if err := route("POST", tok, "chats", "", http.StatusCreated, &made); err != nil {
			return "", err
		}
		return made.Chat.ID, with(tenantID, func(s *tenant.Store) error {
			if _, err := chat.Rename(s, lim, made.Chat.ID, title); err != nil {
				return err
			}
			var parent int64
			for i, words := range messages {
				role := seam.RoleUser
				if i%2 == 1 {
					role = seam.RoleAssistant
				}
				m, err := chat.Append(s, made.Chat.ID, chat.NewMessage{ParentID: parent, Role: role, Blocks: []seam.Block{{Type: seam.BlockText, Text: words}}})
				if err != nil {
					return err
				}
				parent = m.ID
			}
			return nil
		})
	}
	upload := func(tok, chatID, name, words string) (string, error) {
		var up uploads.AddReply
		rec := w.request("POST", api.Prefix+"chats/"+chatID+"/uploads?name="+name, tok, words, nil)
		if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
			return "", fmt.Errorf("uploading %s answered %d: %s", name, rec.Code, rec.Body.String())
		}
		err := json.Unmarshal(rec.Body.Bytes(), &up)
		return up.ID, err
	}
	inFiles := func(tenantID, name string) bool {
		there := false
		with(tenantID, func(s *tenant.Store) error {
			_, err := s.Files().Stat(name)
			there = err == nil
			return nil
		})
		return there
	}
	table := func(tok, query string) (turn.TableReply, error) {
		var got turn.TableReply
		err := route("GET", tok, "chats/table"+query, "", http.StatusOK, &got)
		return got, err
	}
	idsOf := func(rows []turn.TableRow) []string {
		out := []string{}
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}

	// The person's chats: two to delete, two to keep, one with a turn that
	// will be running. The other person's: one, with a file.
	tides, err := newChat(person.ID, token, "Tide tables", "When is high water at the quay?", "At a quarter past nine. Marker: "+manageMark+".")
	if err != nil {
		return nil, err
	}
	boats, err := newChat(person.ID, token, "Boat names", "Suggest a name for a small boat.")
	if err != nil {
		return nil, err
	}
	kiln, err := newChat(person.ID, token, "The kiln", "How hot does a kiln run?")
	if err != nil {
		return nil, err
	}
	kayak, err := newChat(person.ID, token, "The kayak", "How long is a sea kayak?")
	if err != nil {
		return nil, err
	}
	story, err := newChat(person.ID, token, "A long story", "A chat whose reply is still being written.")
	if err != nil {
		return nil, err
	}
	theirs, err := newChat(other.ID, otherToken, "The other person's chat", "Nobody else may delete this. Marker: AMBER-GANNET.")
	if err != nil {
		return nil, err
	}
	own, err := upload(token, tides, "tides.txt", "Invented tide notes, held by one chat.\n")
	if err != nil {
		return nil, err
	}
	shared, err := upload(token, boats, "boats.txt", "Invented boat notes, held by two chats.\n")
	if err != nil {
		return nil, err
	}
	if again, err := upload(token, kiln, "boats.txt", "Invented boat notes, held by two chats.\n"); err != nil || again != shared {
		return nil, fmt.Errorf("the same file uploaded to a second chat is %q, not %q (%v)", again, shared, err)
	}
	theirFile, err := upload(otherToken, theirs, "theirs.txt", "The other person's invented notes.\n")
	if err != nil {
		return nil, err
	}

	// 1. A word that is only in a message finds its chat, marked.
	found, err := table(token, "?q=plover")
	if err != nil {
		return nil, err
	}
	switch {
	case len(found.Rows) != 1 || found.Rows[0].ID != tides || found.Total != 1:
		say("a search for a word in one message of one chat found %v", idsOf(found.Rows))
	case found.Rows[0].Match == nil || found.Rows[0].Match.Title || found.Rows[0].Match.Messages != 1:
		say("the chat a message's word found says of its match: %+v", found.Rows[0].Match)
	default:
		var marked []string
		for _, p := range found.Rows[0].Match.Snippet {
			if p.Match {
				marked = append(marked, strings.ToLower(p.Text))
			}
		}
		if !slices.Equal(marked, []string{"plover"}) {
			say("the snippet of the chat found marks %v, want the word searched for", marked)
		}
	}
	if got, err := table(otherToken, "?q=plover"); err != nil || got.Total != 0 {
		say("another person's search found the person's chat (%d rows, %v)", got.Total, err)
	}

	// The other person's side, before.
	theirTable, err := table(otherToken, "?ids=true")
	if err != nil {
		return nil, err
	}
	theirRows, _ := json.Marshal(theirTable.Rows)
	var theirLog []audit.Entry
	if err := with(other.ID, func(s *tenant.Store) (err error) { theirLog, err = logOf(s); return }); err != nil {
		return nil, err
	}

	// 2. More than the setting allows is refused whole.
	if err := set(turn.KeyDeleteBatchMax, "2"); err != nil {
		return nil, err
	}
	three, _ := json.Marshal(turn.DeleteManyBody{IDs: []string{tides, boats, kiln}})
	if rec := w.request("POST", api.Prefix+"chats/delete", token, string(three), nil); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"too_many"`) {
		say("three chats in one call with chats.delete_batch_max at 2 answered %d %s, want 400 too_many", rec.Code, rec.Body.String())
	}
	if all, err := table(token, ""); err != nil || all.Total != 5 || all.DeleteMax != 2 {
		say("after a call that was refused the person has %d chats and the table gives %d as the most one call deletes (%v)", all.Total, all.DeleteMax, err)
	}
	shippedMax, _ := settings.Lookup(reg, turn.KeyDeleteBatchMax)
	if err := set(turn.KeyDeleteBatchMax, shippedMax.Default); err != nil {
		return nil, err
	}

	// A turn that is running in one of the chats.
	sent := make(chan int, 1)
	go func() {
		body, _ := json.Marshal(turn.SendBody{Text: "Tell me a long story."})
		sent <- w.request("POST", api.Prefix+"chats/"+story+"/turns", token, string(body), nil).Code
	}()
	select {
	case <-slow.writing:
	case code := <-sent:
		return nil, fmt.Errorf("the turn that should be running ended at once, with status %d", code)
	case <-time.After(20 * time.Second):
		return nil, errors.New("the check's model was never called, so no turn is running")
	}

	// 3, 4, 5. One call: two of the person's own, the other person's, and
	// the chat with the turn running.
	batch, _ := json.Marshal(turn.DeleteManyBody{IDs: []string{tides, theirs, boats, story}})
	rec = w.request("POST", api.Prefix+"chats/delete", token, string(batch), nil)
	var got turn.DeleteManyReply
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		return nil, fmt.Errorf("the delete answered %d: %s", rec.Code, rec.Body.String())
	}
	requestID := rec.Header().Get("X-Request-Id")
	if !slices.Equal(got.Deleted, []string{tides, boats}) {
		say("the delete says it deleted %v, want the two selected chats of the person's", got.Deleted)
	}
	reasons := map[string]string{}
	for _, s := range got.Skipped {
		reasons[s.ID] = s.Reason
		if s.Why == "" {
			say("a skipped chat is given no reason in words: %+v", s)
		}
	}
	if len(got.Skipped) != 2 || reasons[theirs] != turn.SkipNotFound || reasons[story] != turn.SkipTurnRunning {
		say("the delete skipped %+v, want the other person's chat as not_found and the running chat as turn_running", got.Skipped)
	}
	for _, id := range []string{tides, boats} {
		if rec := w.request("GET", api.Prefix+"chats/"+id, token, "", nil); rec.Code != http.StatusNotFound {
			say("a deleted chat still opens: status %d", rec.Code)
		}
	}
	left, err := table(token, "?sort=title")
	if err != nil {
		return nil, err
	}
	if want := []string{story, kayak, kiln}; !slices.Equal(idsOf(left.Rows), want) {
		say("after the delete the person's chats are %v, want the three that were not deleted", idsOf(left.Rows))
	}
	if after, err := table(token, "?q=plover"); err != nil || after.Total != 0 {
		say("a deleted chat is still found by a word of its message (%d rows, %v)", after.Total, err)
	}
	if inFiles(person.ID, own) {
		say("the upload of a deleted chat is still in the person's files folder")
	}
	if !inFiles(person.ID, shared) {
		say("a file a kept chat holds went with the deleted chat that held it too")
	}
	var kept uploads.ListReply
	if err := route("GET", token, "chats/"+kiln+"/uploads", "", http.StatusOK, &kept); err != nil || len(kept.Uploads) != 1 || kept.Uploads[0].ID != shared {
		say("the kept chat's uploads are %+v (%v)", kept.Uploads, err)
	}
	// The running chat is as it was: there, with its turn running.
	var open turn.ChatReply
	if err := route("GET", token, "chats/"+story, "", http.StatusOK, &open); err != nil {
		say("the chat with a turn running is gone after the delete: %v", err)
	} else if len(open.Turns) != 1 || open.Turns[0].Status != turn.StatusRunning {
		say("the delete did not leave the running turn running: %+v", open.Turns)
	}

	// 4. Nothing of the other person's changed.
	theirAfter, err := table(otherToken, "?ids=true")
	if err != nil {
		return nil, err
	}
	if rows, _ := json.Marshal(theirAfter.Rows); !bytes.Equal(rows, theirRows) || !slices.Contains(theirAfter.IDs, theirs) {
		say("the other person's chats changed when their chat's id was in the person's delete")
	}
	if !inFiles(other.ID, theirFile) {
		say("the other person's file is gone after the person's delete")
	}
	err = with(other.ID, func(s *tenant.Store) error {
		log, err := logOf(s)
		if err != nil {
			return err
		}
		for _, e := range log[min(len(theirLog), len(log)):] {
			if !strings.HasPrefix(e.Action, "GET "+api.Prefix+"chats/table") {
				say("the other person's audit log gained an entry from the person's delete: %s %s", e.Action, e.Outcome)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 6. Each chat of the batch is in the person's audit log.
	err = with(person.ID, func(s *tenant.Store) error {
		log, err := audit.List(ctx, s, audit.Filter{RequestID: requestID})
		if err != nil {
			return err
		}
		var lines []string
		for _, e := range log {
			lines = append(lines, e.Action+" "+string(e.Outcome))
		}
		one := "DELETE " + api.Prefix + "chats/"
		want := []string{
			"POST " + api.Prefix + "chats/delete started",
			one + tides + " started", one + tides + " ok",
			one + theirs + " refused",
			one + boats + " started", one + boats + " ok",
			one + story + " refused",
			"POST " + api.Prefix + "chats/delete ok",
		}
		if !slices.Equal(lines, want) {
			say("the audit log holds, for the delete:\n\t%s\nwant:\n\t%s", strings.Join(lines, "\n\t"), strings.Join(want, "\n\t"))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// The running turn is stopped, so the server closes with nothing at work.
	if len(open.Turns) == 1 {
		if err := route("POST", token, "chats/"+story+"/turns/"+open.Turns[0].ID+"/stop", "", http.StatusOK, nil); err != nil {
			say("the running turn could not be stopped after the check: %v", err)
		}
	}
	select {
	case <-sent:
	case <-time.After(20 * time.Second):
		say("the stream of the running turn did not close after it was stopped")
	}
	return problems, nil
}
