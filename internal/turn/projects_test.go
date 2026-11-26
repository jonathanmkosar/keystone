package turn

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/projects"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
)

// Projects (P9-01). Every project, file and word here is invented.

// The words the proof looks for in what a model is sent.
const (
	guideName         = "Harbour museum guide"
	guideInstructions = "Answer as the museum's volunteer guide, in short paragraphs. Marker: TEAL-HERON."
	hoursName         = "opening-hours.md"
	hoursText         = "# Opening hours\n\nTuesday to Sunday, ten to five. Marker: AMBER-OTTER.\n"
	instructionMark   = "TEAL-HERON"
	fileMark          = "AMBER-OTTER"
)

// serveProjects starts a server as serve does, with the project and memory
// routes beside the chat and turn routes.
func (g *rig) serveProjects() *served {
	g.t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	cfg.StreamPoll = 50 * time.Millisecond
	provider, err := testtenant.Provider(g.fx, fake.Options{DeltaSize: 12})
	if err != nil {
		g.t.Fatal(err)
	}
	set, err := adapters.Fake(g.fx.Scripts, fake.Options{DeltaSize: 12})
	if err != nil {
		g.t.Fatal(err)
	}
	for name, build := range g.extra {
		set[name] = build
	}
	runner := NewRunner(Options{Install: g.install, Config: cfg, PageLimit: 50, TestProvider: provider, Adapters: set})
	runner.Recover()
	routes := append(Routes(runner), MemoryRoutes(runner)...)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, append(routes, ProjectRoutes(runner)...))
	if err != nil {
		g.t.Fatal(err)
	}
	sv := &served{g: g, runner: runner, server: httptest.NewServer(h), tokens: map[string]string{}}
	g.t.Cleanup(sv.stop)
	for id, password := range map[string]string{g.test.ID: g.fx.Password, g.ordinary.ID: ordinaryPassword} {
		g.with(id, func(s *tenant.Store) {
			token, _, err := signin.SignIn(s, password, g.signIn)
			if err != nil {
				g.t.Fatal(err)
			}
			sv.tokens[id] = token
		})
	}
	return sv
}

// newProject makes a project through the route, and adds a file to it.
func (sv *served) newProject(tenantID, name, instructions, fileName, fileText string) ProjectReply {
	sv.g.t.Helper()
	body, _ := json.Marshal(ProjectBody{Name: &name, Instructions: &instructions})
	var made ProjectReply
	sv.call(tenantID, "POST", "projects", string(body), http.StatusCreated, &made)
	if fileName != "" {
		var f ProjectFileReply
		sv.call(tenantID, "POST", "projects/"+made.Project.ID+"/files?name="+url.QueryEscape(fileName), fileText, http.StatusCreated, &f)
		if !f.New || f.Text != fileText || f.Added.IsZero() {
			sv.g.t.Fatalf("adding the file answered %+v", f)
		}
	}
	return made
}

// sentSystem is the system text of a turn's first chat call, as the audit
// log holds it, and the call's request.
func sentSystem(t *testing.T, g *rig, tenantID, requestID string) (string, seam.Request) {
	t.Helper()
	sent := chatSent(t, g, tenantID, requestID)
	if len(sent) == 0 {
		t.Fatal("the turn made no chat call")
	}
	req, err := seam.DecodeRequest([]byte(sent[0]))
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, b := range req.System {
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "\n"), req
}

// firstCall is what a turn's first call was built from.
func firstCall(t *testing.T, g *rig, tenantID, turnID string) contextbuild.Record {
	t.Helper()
	var rec contextbuild.Record
	g.with(tenantID, func(s *tenant.Store) {
		calls, err := Contexts(context.Background(), s, turnID)
		if err != nil || len(calls) == 0 {
			t.Fatalf("the turn's stored contexts are %+v (%v)", calls, err)
		}
		rec = calls[0].Record
	})
	return rec
}

// The item's proof: a project chat's every call carries the project's
// instructions, in the standing start where a provider caches them, and its
// files, with the attachments; a chat outside the project carries neither;
// a chat moved in starts carrying them, and moved out stops.
func TestAProjectChatCarriesItsInstructionsAndFiles(t *testing.T) {
	g := newRig(t)
	sv := g.serveProjects()
	made := sv.newProject(g.test.ID, guideName, guideInstructions, hoursName, hoursText)
	pid := made.Project.ID
	if made.Project.Name != guideName || made.Project.MemorySpace != "project-harbour-museum-guide" || made.Limits.MaxFiles != 20 {
		t.Fatalf("the project was made as %+v", made)
	}

	var inside ChatReply
	sv.call(g.test.ID, "POST", "projects/"+pid+"/chats", "", http.StatusCreated, &inside)
	events, turnID, requestID := sv.send(g.test.ID, inside.Chat.ID, ask("When is the museum open?", ""))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	system, req := sentSystem(t, g, g.test.ID, requestID)
	if !strings.Contains(system, instructionMark) || !strings.Contains(system, fileMark) || !strings.Contains(system, `project "`+guideName+`"`) {
		t.Fatalf("the project chat's call does not carry the instructions and the file:\n%s", system)
	}
	// The instructions are in the standing start, which the call's first
	// cache point covers, and the file after it.
	if req.Cache == nil {
		t.Fatal("the call has no cache points")
	}
	at := func(mark string) int {
		for i, b := range req.System {
			if strings.Contains(b.Text, mark) {
				return i
			}
		}
		return -1
	}
	if i, j := at(instructionMark), at(fileMark); i < 0 || i >= req.Cache.Start || j < req.Cache.Start {
		t.Errorf("the instructions are system block %d and the file block %d, with the standing start ending at %d", i, j, req.Cache.Start)
	}
	rec := firstCall(t, g, g.test.ID, turnID)
	if p := rec.Part(contextbuild.PartProject); p.State != contextbuild.StateKept {
		t.Errorf("the record's project part is %+v", p)
	}
	att := rec.Part(contextbuild.PartAttached)
	if len(att.Items) != 1 || att.Items[0].Label != `project "`+guideName+`": `+hoursName || !att.Items[0].Kept {
		t.Errorf("the record's attached part is %+v", att)
	}
	var where ChatProjectReply
	sv.call(g.test.ID, "GET", "chats/"+inside.Chat.ID+"/project", "", http.StatusOK, &where)
	if where.Project == nil || where.Project.ID != pid {
		t.Errorf("the chat's project is %+v", where)
	}

	// A chat outside the project carries neither.
	outside := sv.newChat(g.test.ID)
	_, turnID, requestID = sv.send(g.test.ID, outside, ask("When is the museum open?", ""))
	if system, _ := sentSystem(t, g, g.test.ID, requestID); strings.Contains(system, instructionMark) || strings.Contains(system, fileMark) {
		t.Fatalf("a chat outside the project carries it:\n%s", system)
	}
	if p := firstCall(t, g, g.test.ID, turnID).Part(contextbuild.PartProject); p.State != contextbuild.StateEmpty {
		t.Errorf("outside the project the project part is %+v", p)
	}
	where = ChatProjectReply{}
	sv.call(g.test.ID, "GET", "chats/"+outside+"/project", "", http.StatusOK, &where)
	if where.Project != nil {
		t.Errorf("the chat outside is in %+v", where.Project)
	}

	// Moved in, it carries them; moved out, it does not.
	sv.call(g.test.ID, "PUT", "projects/"+pid+"/chats/"+outside, "", http.StatusOK, &where)
	if where.Project == nil || where.Project.ID != pid {
		t.Fatalf("moving the chat in answered %+v", where)
	}
	_, _, requestID = sv.send(g.test.ID, outside, ask("And on Mondays?", ""))
	if system, _ := sentSystem(t, g, g.test.ID, requestID); !strings.Contains(system, instructionMark) || !strings.Contains(system, fileMark) {
		t.Fatalf("a chat moved into the project does not carry it:\n%s", system)
	}
	var read ProjectReply
	sv.call(g.test.ID, "GET", "projects/"+pid, "", http.StatusOK, &read)
	if len(read.Chats) != 2 || len(read.Files) != 1 || read.Files[0].Name != hoursName {
		t.Errorf("the project reads %+v", read)
	}
	where = ChatProjectReply{}
	sv.call(g.test.ID, "DELETE", "projects/"+pid+"/chats/"+outside, "", http.StatusOK, &where)
	if where.Project != nil {
		t.Fatalf("taking the chat out answered %+v", where)
	}
	_, _, requestID = sv.send(g.test.ID, outside, ask("And on Mondays?", ""))
	if system, _ := sentSystem(t, g, g.test.ID, requestID); strings.Contains(system, instructionMark) || strings.Contains(system, fileMark) {
		t.Fatalf("a chat taken out of the project still carries it:\n%s", system)
	}

	// New instructions reach the next call; a file taken off does not.
	changed := "Answer in one sentence. Marker: GREY-PLOVER."
	body, _ := json.Marshal(ProjectBody{Instructions: &changed})
	sv.call(g.test.ID, "PATCH", "projects/"+pid, string(body), http.StatusOK, &read)
	var removed RemovedReply
	sv.call(g.test.ID, "DELETE", "projects/"+pid+"/files/"+read.Files[0].ID, "", http.StatusOK, &removed)
	_, _, requestID = sv.send(g.test.ID, inside.Chat.ID, ask("Anything else?", ""))
	if system, _ := sentSystem(t, g, g.test.ID, requestID); !strings.Contains(system, "GREY-PLOVER") || strings.Contains(system, instructionMark) || strings.Contains(system, fileMark) {
		t.Fatalf("after the change the call carries:\n%s", system)
	}
}

// A file longer than projects.file_context_max_chars is cut, with a note.
func TestALongFileIsCut(t *testing.T) {
	g := newRig(t)
	g.setFor(g.test.ID, tenantsettings.Person, projects.SettingFileContextMaxChars, "200")
	sv := g.serveProjects()
	long := strings.Repeat("harbour ", 100) + "Marker: LATE-WORD."
	made := sv.newProject(g.test.ID, guideName, "", "long.txt", long)
	var inside ChatReply
	sv.call(g.test.ID, "POST", "projects/"+made.Project.ID+"/chats", "", http.StatusCreated, &inside)
	_, turnID, requestID := sv.send(g.test.ID, inside.Chat.ID, ask("Hello.", ""))
	system, _ := sentSystem(t, g, g.test.ID, requestID)
	if strings.Contains(system, "LATE-WORD") || !strings.Contains(system, "[The rest of long.txt, 618 more characters, is left out.]") {
		t.Fatalf("the long file was not cut as it should be:\n%s", system)
	}
	// No instructions: the project part is empty.
	if p := firstCall(t, g, g.test.ID, turnID).Part(contextbuild.PartProject); p.State != contextbuild.StateEmpty {
		t.Errorf("a project with no instructions gives the project part %+v", p)
	}
}

// Fixed rule 1: another tenant's project is not reachable on any route,
// and no chat crosses between two tenants' projects.
func TestAnotherTenantsProjectIsUnreachable(t *testing.T) {
	g := newRig(t)
	sv := g.serveProjects()
	made := sv.newProject(g.test.ID, guideName, guideInstructions, hoursName, hoursText)
	pid := made.Project.ID
	var first ProjectReply
	sv.call(g.test.ID, "GET", "projects/"+pid, "", http.StatusOK, &first)
	fid := first.Files[0].ID
	theirs := sv.newChat(g.ordinary.ID)
	mine := sv.newChat(g.test.ID)
	for _, c := range []struct{ method, path, body string }{
		{"GET", "projects/" + pid, ""},
		{"PATCH", "projects/" + pid, `{"name":"Taken"}`},
		{"DELETE", "projects/" + pid, ""},
		{"POST", "projects/" + pid + "/files?name=a.txt", "words"},
		{"GET", "projects/" + pid + "/files/" + fid, ""},
		{"PATCH", "projects/" + pid + "/files/" + fid, `{"sensitive":false}`},
		{"DELETE", "projects/" + pid + "/files/" + fid, ""},
		{"POST", "projects/" + pid + "/chats", ""},
		{"PUT", "projects/" + pid + "/chats/" + theirs, ""},
		{"DELETE", "projects/" + pid + "/chats/" + theirs, ""},
		{"GET", "chats/" + mine + "/project", ""},
	} {
		resp := sv.request(g.ordinary.ID, c.method, c.path, c.body, nil)
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s as another tenant answered %d: %s", c.method, c.path, resp.StatusCode, raw)
		}
	}
	// The project's owner cannot move another tenant's chat into it.
	if code, _ := sv.status(g.test.ID, "PUT", "projects/"+pid+"/chats/"+theirs); code != http.StatusNotFound {
		t.Errorf("moving another tenant's chat in answered %d", code)
	}
	var list ProjectsReply
	sv.call(g.ordinary.ID, "GET", "projects", "", http.StatusOK, &list)
	if len(list.Projects) != 0 {
		t.Errorf("another tenant lists %+v", list.Projects)
	}
	// And the project is as it was.
	var read ProjectReply
	sv.call(g.test.ID, "GET", "projects/"+pid, "", http.StatusOK, &read)
	if read.Project.Name != guideName || len(read.Files) != 1 || len(read.Chats) != 0 {
		t.Errorf("after another tenant's calls the project reads %+v", read)
	}
}

// The routes' refusals.
func TestTheProjectRoutes(t *testing.T) {
	g := newRig(t)
	g.setFor(g.test.ID, tenantsettings.Person, projects.SettingMaxFiles, "1")
	sv := g.serveProjects()
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "projects", `{}`, http.StatusBadRequest},
		{"POST", "projects", `{"name":"  "}`, http.StatusBadRequest},
		{"POST", "projects", `{"name":"A","archived":true}`, http.StatusBadRequest},
		{"POST", "projects", `{"name":"A","colour":"teal"}`, http.StatusBadRequest},
		{"GET", "projects/nothere", "", http.StatusNotFound},
		{"POST", "projects/nothere/chats", "", http.StatusNotFound},
	} {
		if code, body := sv.statusBody(g.test.ID, c.method, c.path, c.body); code != c.want {
			t.Errorf("%s %s %s answered %d, want %d: %s", c.method, c.path, c.body, code, c.want, body)
		}
	}
	made := sv.newProject(g.test.ID, guideName, "", hoursName, hoursText)
	pid := made.Project.ID
	var again ProjectFileReply
	sv.call(g.test.ID, "POST", "projects/"+pid+"/files?name=copy.md", hoursText, http.StatusOK, &again)
	if again.New || again.Name != hoursName {
		t.Errorf("the same file again answered %+v", again)
	}
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "projects/" + pid + "/files?name=second.txt", "Another file.", http.StatusConflict},
		{"POST", "projects/" + pid + "/files?name=photo.png", "\x89PNG\r\n", http.StatusBadRequest},
		{"PATCH", "projects/" + pid, `{}`, http.StatusBadRequest},
		{"PATCH", "projects/" + pid + "/files/" + again.ID, `{}`, http.StatusBadRequest},
		{"GET", "projects/" + pid + "/files/" + strings.Repeat("0", 64), "", http.StatusNotFound},
		{"DELETE", "projects/" + pid + "/files/" + strings.Repeat("0", 64), "", http.StatusNotFound},
		{"PUT", "projects/" + pid + "/chats/nothere", "", http.StatusNotFound},
	} {
		if code, body := sv.statusBody(g.test.ID, c.method, c.path, c.body); code != c.want {
			t.Errorf("%s %s answered %d, want %d: %s", c.method, c.path, code, c.want, body)
		}
	}
	var f ProjectFileReply
	sv.call(g.test.ID, "PATCH", "projects/"+pid+"/files/"+again.ID, `{"sensitive":true}`, http.StatusOK, &f)
	if !f.Sensitive || f.Text != hoursText {
		t.Errorf("marking the file answered %+v", f)
	}
	var read ProjectReply
	sv.call(g.test.ID, "PATCH", "projects/"+pid, `{"archived":true,"name":"Old guide"}`, http.StatusOK, &read)
	if !read.Project.Archived || read.Project.Name != "Old guide" || read.Project.MemorySpace != made.Project.MemorySpace {
		t.Errorf("archiving and renaming answered %+v", read.Project)
	}
	var list ProjectsReply
	sv.call(g.test.ID, "GET", "projects", "", http.StatusOK, &list)
	if len(list.Projects) != 1 || !list.Projects[0].Archived || list.Projects[0].Files != 1 || list.Limits.FileMaxBytes != 1048576 || len(list.Limits.TextExtensions) == 0 {
		t.Errorf("the list answered %+v", list)
	}
	// Deleting the project keeps its chats.
	var inside ChatReply
	sv.call(g.test.ID, "POST", "projects/"+pid+"/chats", "", http.StatusCreated, &inside)
	var gone DeletedReply
	sv.call(g.test.ID, "DELETE", "projects/"+pid, "", http.StatusOK, &gone)
	var still ChatReply
	sv.call(g.test.ID, "GET", "chats/"+inside.Chat.ID, "", http.StatusOK, &still)
	var where ChatProjectReply
	sv.call(g.test.ID, "GET", "chats/"+inside.Chat.ID+"/project", "", http.StatusOK, &where)
	if where.Project != nil {
		t.Errorf("after the project was deleted its chat is in %+v", where.Project)
	}
}

// statusBody makes a call with a body and returns its status and body.
func (sv *served) statusBody(tenantID, method, path, body string) (int, string) {
	sv.g.t.Helper()
	resp := sv.request(tenantID, method, path, body, nil)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// Fixed rule 3: a file marked sensitive is held back from a call that may
// reach a hosted model, which is told only that one was left out; a call
// whose every model may read it carries it, and the chat is held from then
// on. A project with a sensitive file keeps a local chat's route to models
// that may read it, so the hosted fallback is left out.
func TestASensitiveProjectFileIsHeldBack(t *testing.T) {
	g := newRig(t)
	h := &recapEars{sent: map[string][]string{}, p: scripted(t, `{"test_data":true,"scripts":[
		{"name":"anything","when":{},"steps":[{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"A plain answer."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]}
	]}`)}
	g.extra = adapters.Set{recapHouse.Adapter: h.builder, recapPaid.Adapter: h.builder}
	g.addModels(recapHouse, recapPaid)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	g.set(tenantsettings.Person, KeyRecap, "false")
	g.set(tenantsettings.Person, KeyFallbackModels, recapPaid.Name)
	sv := g.serveProjects()
	const secret = "Dosage notes. Marker: VIOLET-WREN."
	made := sv.newProject(g.ordinary.ID, "Health", "Keep it gentle.", "dosage.txt", secret)
	pid := made.Project.ID
	var read ProjectReply
	sv.call(g.ordinary.ID, "GET", "projects/"+pid, "", http.StatusOK, &read)
	var f ProjectFileReply
	sv.call(g.ordinary.ID, "PATCH", "projects/"+pid+"/files/"+read.Files[0].ID, `{"sensitive":true}`, http.StatusOK, &f)

	// On the hosted model: left out, counted, not held.
	var hosted ChatReply
	sv.call(g.ordinary.ID, "POST", "projects/"+pid+"/chats", "", http.StatusCreated, &hosted)
	events, turnID, _ := sv.send(g.ordinary.ID, hosted.Chat.ID, ask("What do the notes say?", recapPaid.Name))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the hosted turn ended as %+v", end)
	}
	sent := h.all(recapPaid.Name)
	if strings.Contains(sent, "VIOLET-WREN") || !strings.Contains(sent, "1 knowledge file of this chat's project is left out") || !strings.Contains(sent, "Keep it gentle.") {
		t.Fatalf("the hosted model was sent the sensitive file, or not told one was left out, or not given the instructions:\n%s", sent)
	}
	att := firstCall(t, g, g.ordinary.ID, turnID).Part(contextbuild.PartAttached)
	if len(att.Items) != 1 || att.Items[0].Label != projects.LeftOutLabel {
		t.Errorf("the hosted call's attached part is %+v", att)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if held, _ := recall.Held(context.Background(), s, hosted.Chat.ID); held {
			t.Error("a chat that was given nothing sensitive is held")
		}
	})

	// On the local model, with a hosted fallback: carried, the chat held,
	// and the route kept to models that may read it.
	var local ChatReply
	sv.call(g.ordinary.ID, "POST", "projects/"+pid+"/chats", "", http.StatusCreated, &local)
	events, _, requestID := sv.send(g.ordinary.ID, local.Chat.ID, ask("What do the notes say?", recapHouse.Name))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the local turn ended as %+v", end)
	}
	if system, _ := sentSystem(t, g, g.ordinary.ID, requestID); !strings.Contains(system, "VIOLET-WREN") {
		t.Fatalf("the local model's call does not carry the sensitive file:\n%s", system)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if held, err := recall.Held(context.Background(), s, local.Chat.ID); err != nil || !held {
			t.Errorf("the chat given a sensitive file is not held (%v)", err)
		}
	})
	events, _, _ = sv.send(g.ordinary.ID, local.Chat.ID, ask("And again?", recapPaid.Name))
	if end := last(t, events); end.Error == nil || end.Error.Code != CodeSensitive {
		t.Fatalf("the chat given a sensitive file went to the hosted model: %+v", end)
	}
	if strings.Contains(h.all(recapPaid.Name), "VIOLET-WREN") {
		t.Fatal("the hosted model was ever sent the sensitive file")
	}
}

// Project memory: the memory pass writes a project chat's facts to the
// project's space, and recall in a project chat gives what it finds there
// first.
func TestAProjectChatsFactsGoToItsSpaceAndRecallPrefersIt(t *testing.T) {
	g := newRig(t)
	g.setFor(g.test.ID, tenantsettings.Person, knowledge.SettingAdapter, builtin.Name)
	// A page elsewhere that matches the later question better than the
	// facts will.
	g.with(g.test.ID, func(s *tenant.Store) {
		if _, err := builtin.WritePage(context.Background(), s, builtin.NewPage{
			Space: "home", Path: "allotment", Title: "Allotment beehives",
			Body: "Beehives at the allotment: the allotment beehives, allotment beehives, beehives and the allotment.",
		}); err != nil {
			t.Fatal(err)
		}
	})
	sv := g.serveProjects()
	made := sv.newProject(g.test.ID, "Allotment", "", "", "")
	space := made.Project.MemorySpace
	var inside ChatReply
	sv.call(g.test.ID, "POST", "projects/"+made.Project.ID+"/chats", "", http.StatusCreated, &inside)
	_, turnID, _ := sv.send(g.test.ID, inside.Chat.ID, ask(beesAsk, ""))
	p := waitPass(g, g.test.ID, turnID)
	if p.Status != memory.StatusDone || p.Space != space || space != "project-allotment" || len(p.Facts) != 2 {
		t.Fatalf("the project chat's memory pass ended as %+v, want its facts in %q", p, space)
	}
	if src := sourceOf(g, g.test.ID, p.SourceID); src.Space != space {
		t.Errorf("the source is in %q", src.Space)
	}
	// A chat outside the project keeps its facts in the memory space.
	outside := sv.newChat(g.test.ID)
	_, turnID, _ = sv.send(g.test.ID, outside, ask(beesAsk, ""))
	if p := waitPass(g, g.test.ID, turnID); p.Space != "memory" {
		t.Errorf("a chat outside the project kept its facts in %q", p.Space)
	}

	const question = "Remind me about the beehives at the allotment."
	firstHit := func(chatID string) recall.HitRecord {
		t.Helper()
		_, turnID, _ := sv.send(g.test.ID, chatID, ask(question, ""))
		var rec recall.Record
		g.with(g.test.ID, func(s *tenant.Store) {
			var ok bool
			var err error
			if rec, ok, err = recall.Saved(context.Background(), s, turnID); err != nil || !ok {
				t.Fatalf("the turn's recall record is missing (%v)", err)
			}
		})
		if len(rec.Hits) == 0 {
			t.Fatalf("recall found nothing for %q", question)
		}
		return rec.Hits[0]
	}
	if hit := firstHit(inside.Chat.ID); hit.Space != space {
		t.Errorf("in the project chat recall gave first a hit in %q, want %q", hit.Space, space)
	}
	if hit := firstHit(outside); hit.Space == space {
		t.Errorf("outside the project recall gave the project's space first")
	}
	g.setFor(g.test.ID, tenantsettings.Person, projects.SettingRecallPrefer, "false")
	if hit := firstHit(inside.Chat.ID); hit.Space == space {
		t.Errorf("with %s off the project chat still gave the project's space first", projects.SettingRecallPrefer)
	}
}
