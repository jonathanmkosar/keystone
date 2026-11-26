package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// The "projects" check (P9-01): a chat in a project carries the project's
// instructions and knowledge files in every call, and no other chat does.
//
// It runs the real server, with the shipped settings, on a throwaway data
// folder. The test tenant's recorded turns answer every model call. The
// check proves:
//
//  1. A chat started in a project is sent the project's instructions, in the
//     standing start that the call's first cache point covers, and its
//     file's text, with the attachments.
//  2. A chat outside the project is sent neither; moved in, it is sent both;
//     moved out again, neither.
//  3. Fixed rule 1: another tenant reaches the project on no route, and no
//     chat crosses between two tenants' projects.
//  4. Fixed rule 3, on an invented tenant with a hosted and a local model: a
//     file marked sensitive reaches no call to the hosted model, which is
//     told only that one was left out; a call to the local model carries
//     it, and its chat is held.

// Projects (P9-01) decide whether a knowledge file may reach a call:
// projects.MayShow takes the plain answer. The package keeps its rows and
// files inside one tenant's store, and reads no setting.
func init() {
	fixedRuleGuards["rules.sensitive_content"] = append(fixedRuleGuards["rules.sensitive_content"], "internal/projects")
	fixedRuleGuards["rules.tenant_isolation"] = append(fixedRuleGuards["rules.tenant_isolation"], "internal/projects")
}

// The check's project, invented, and the words it looks for.
const (
	checkProjectName = "Harbour museum guide"
	checkProjectText = "Answer as the museum's volunteer guide. Marker: TEAL-HERON."
	checkFileName    = "opening-hours.md"
	checkFileText    = "# Opening hours\n\nTuesday to Sunday, ten to five. Marker: AMBER-OTTER.\n"
	checkSecretText  = "Notes the person keeps to themselves. Marker: VIOLET-WREN."
	checkTextMark    = "TEAL-HERON"
	checkFileMark    = "AMBER-OTTER"
	checkSecretMark  = "VIOLET-WREN"
	checkLeftOut     = "knowledge file of this chat's project is left out"
)

// projectsPassword is invented, and guards a folder that is removed.
const projectsPassword = "an invented phrase for the projects check"

func checkProjects(root string) error {
	shipped, err := testtenant.Shipped()
	if err != nil {
		return err
	}
	p, err := testtenant.Provider(shipped, fake.Options{})
	if err != nil {
		return err
	}
	c := &counted{sent: map[string][]string{}, p: p}
	g, err := openKnowledgeRig(root, "keystone-projects-", adapters.Set{pastHosted.Adapter: c.builder, pastLocal.Adapter: c.builder})
	if err != nil {
		return err
	}
	defer g.done()
	problems, err := projectProblems(g, c)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

// projectRoute calls a project route and reads its answer.
func (g *knowledgeRig) projectRoute(method, token, path, body string, want int, out any) error {
	rec := g.w.request(method, api.Prefix+path, token, body, nil)
	if rec.Code != want {
		return fmt.Errorf("%s %s answered %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	if out != nil {
		return json.Unmarshal(rec.Body.Bytes(), out)
	}
	return nil
}

// makeProject makes a project with one file, through the routes, and
// returns its id and the file's.
func (g *knowledgeRig) makeProject(token, name, instructions, fileName, fileText string) (string, string, error) {
	body, _ := json.Marshal(turn.ProjectBody{Name: &name, Instructions: &instructions})
	var made turn.ProjectReply
	if err := g.projectRoute("POST", token, "projects", string(body), http.StatusCreated, &made); err != nil {
		return "", "", err
	}
	var f turn.ProjectFileReply
	if err := g.projectRoute("POST", token, "projects/"+made.Project.ID+"/files?name="+url.QueryEscape(fileName), fileText, http.StatusCreated, &f); err != nil {
		return "", "", err
	}
	return made.Project.ID, f.ID, nil
}

// inProject starts a chat in a project, through its route.
func (g *knowledgeRig) inProject(token, projectID string) (string, error) {
	var made turn.ChatReply
	if err := g.projectRoute("POST", token, "projects/"+projectID+"/chats", "", http.StatusCreated, &made); err != nil {
		return "", err
	}
	return made.Chat.ID, nil
}

// standingStart is the system text of one request the audit log holds
// that its first cache point covers, and the rest of its system text.
func standingStart(sent string) (start, rest string) {
	req, err := seam.DecodeRequest([]byte(sent))
	if err != nil {
		return "", ""
	}
	n := 0
	if req.Cache != nil {
		n = req.Cache.Start
	}
	var a, b []string
	for i, blk := range req.System {
		if i < n {
			a = append(a, blk.Text)
		} else {
			b = append(b, blk.Text)
		}
	}
	return strings.Join(a, "\n"), strings.Join(b, "\n")
}

func projectProblems(g *knowledgeRig, c *counted) ([]string, error) {
	ctx := context.Background()
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	token, rec := g.w.signIn(g.test.ID, g.testPassword)
	if token == "" {
		return nil, fmt.Errorf("the test tenant cannot sign in through the API: status %d", rec.Code)
	}
	firstCall := func(tenantID, requestID string) (string, error) {
		calls, err := g.chatCalls(tenantID, requestID)
		if err != nil || len(calls) == 0 {
			return "", fmt.Errorf("a turn made no chat call (%v)", err)
		}
		return calls[0], nil
	}

	// 1. A chat started in a project.
	pid, fid, err := g.makeProject(token, checkProjectName, checkProjectText, checkFileName, checkFileText)
	if err != nil {
		return nil, err
	}
	inside, err := g.inProject(token, pid)
	if err != nil {
		return nil, err
	}
	t, err := g.send(token, inside, "When is the museum open?", "")
	if err != nil {
		return nil, err
	}
	sent, err := firstCall(g.test.ID, t.requestID)
	if err != nil {
		return nil, err
	}
	start, rest := standingStart(sent)
	if !strings.Contains(start, checkTextMark) {
		say("a chat in the project is not sent the project's instructions in the standing start")
	}
	if !strings.Contains(rest, checkFileMark) {
		say("a chat in the project is not sent the project's file with the attachments")
	}

	// 2. A chat outside, moved in, moved out.
	t, err = g.send(token, "", "When is the museum open?", "")
	if err != nil {
		return nil, err
	}
	outside := t.chatID
	carries := func(chatID, what string) (bool, bool, error) {
		t, err := g.send(token, chatID, what, "")
		if err != nil {
			return false, false, err
		}
		sent, err := firstCall(g.test.ID, t.requestID)
		if err != nil {
			return false, false, err
		}
		sys := systemOf(sent)
		return strings.Contains(sys, checkTextMark), strings.Contains(sys, checkFileMark), nil
	}
	if sent, err = firstCall(g.test.ID, t.requestID); err != nil {
		return nil, err
	} else if sys := systemOf(sent); strings.Contains(sys, checkTextMark) || strings.Contains(sys, checkFileMark) {
		say("a chat outside the project is sent the project's instructions or file")
	}
	if err := g.projectRoute("PUT", token, "projects/"+pid+"/chats/"+outside, "", http.StatusOK, nil); err != nil {
		return nil, err
	}
	if text, file, err := carries(outside, "And on Mondays?"); err != nil {
		return nil, err
	} else if !text || !file {
		say("a chat moved into the project is not sent its instructions (%v) or its file (%v)", text, file)
	}
	if err := g.projectRoute("DELETE", token, "projects/"+pid+"/chats/"+outside, "", http.StatusOK, nil); err != nil {
		return nil, err
	}
	if text, file, err := carries(outside, "And on Mondays?"); err != nil {
		return nil, err
	} else if text || file {
		say("a chat taken out of the project is still sent its instructions (%v) or its file (%v)", text, file)
	}

	// 3. Fixed rule 1.
	person, err := g.in.Create("Projects Check Person")
	if err != nil {
		return nil, err
	}
	err = g.with(person.ID, func(s *tenant.Store) error {
		o, err := signin.Defaults()
		if err != nil {
			return err
		}
		if err := signin.SetPassword(s, projectsPassword, o); err != nil {
			return err
		}
		lim, err := adapters.Limits(g.reg)
		if err != nil {
			return err
		}
		for _, m := range []models.Model{pastHosted, pastLocal} {
			if err := models.Add(ctx, s, lim, m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	other, rec := g.w.signIn(person.ID, projectsPassword)
	if other == "" {
		return nil, fmt.Errorf("the invented tenant cannot sign in through the API: status %d", rec.Code)
	}
	if err := g.set(person.ID, memory.SettingOn, "false", turn.KeyTitleAuto, "false", turn.KeyRecap, "false"); err != nil {
		return nil, err
	}
	theirs, err := g.send(other, "", "A chat of my own.", pastLocal.Name)
	if err != nil {
		return nil, err
	}
	for _, x := range []struct{ method, path, body string }{
		{"GET", "projects/" + pid, ""},
		{"PATCH", "projects/" + pid, `{"name":"Taken"}`},
		{"DELETE", "projects/" + pid, ""},
		{"POST", "projects/" + pid + "/files?name=a.txt", "words"},
		{"GET", "projects/" + pid + "/files/" + fid, ""},
		{"PATCH", "projects/" + pid + "/files/" + fid, `{"sensitive":true}`},
		{"DELETE", "projects/" + pid + "/files/" + fid, ""},
		{"POST", "projects/" + pid + "/chats", ""},
		{"PUT", "projects/" + pid + "/chats/" + theirs.chatID, ""},
		{"GET", "chats/" + inside + "/project", ""},
	} {
		if r := g.w.request(x.method, api.Prefix+x.path, other, x.body, nil); r.Code != http.StatusNotFound {
			say("%s %s as another tenant answered %d, want 404", x.method, x.path, r.Code)
		}
	}
	if r := g.w.request("PUT", api.Prefix+"projects/"+pid+"/chats/"+theirs.chatID, token, "", nil); r.Code != http.StatusNotFound {
		say("moving another tenant's chat into a project answered %d, want 404", r.Code)
	}
	var read turn.ProjectReply
	if err := g.projectRoute("GET", token, "projects/"+pid, "", http.StatusOK, &read); err != nil {
		return nil, err
	}
	if read.Project.Name != checkProjectName || len(read.Files) != 1 || read.Files[0].Sensitive {
		say("after another tenant's calls the project reads %+v", read.Project)
	}

	// 4. Fixed rule 3, on the invented tenant.
	spid, sfid, err := g.makeProject(other, "Private notes", "Keep it short.", "notes.txt", checkSecretText)
	if err != nil {
		return nil, err
	}
	if err := g.projectRoute("PATCH", other, "projects/"+spid+"/files/"+sfid, `{"sensitive":true}`, http.StatusOK, nil); err != nil {
		return nil, err
	}
	onHosted, err := g.inProject(other, spid)
	if err != nil {
		return nil, err
	}
	hosted, err := g.send(other, onHosted, "What do my notes say?", pastHosted.Name)
	if err != nil {
		return nil, err
	}
	if n := len(c.sentWith(pastHosted.Name, checkSecretMark)); n > 0 {
		say("the hosted model was sent the sensitive file in %d requests", n)
	}
	if all, _, err := g.calls(person.ID, hosted.requestID); err != nil {
		return nil, err
	} else if strings.Contains(all, checkSecretMark) || !strings.Contains(all, checkLeftOut) {
		say("the hosted model's call holds the sensitive file, or does not say one was left out")
	}
	if h, err := g.held(person.ID, onHosted); err != nil {
		return nil, err
	} else if h {
		say("the chat whose hosted call left the sensitive file out is held")
	}
	onLocal, err := g.inProject(other, spid)
	if err != nil {
		return nil, err
	}
	local, err := g.send(other, onLocal, "What do my notes say?", pastLocal.Name)
	if err != nil {
		return nil, err
	}
	if sent, err := firstCall(person.ID, local.requestID); err != nil {
		return nil, err
	} else if !strings.Contains(systemOf(sent), checkSecretMark) {
		say("the local model's call does not carry the sensitive file")
	}
	if h, err := g.held(person.ID, onLocal); err != nil {
		return nil, err
	} else if !h {
		say("the chat whose call carried a sensitive file is not held")
	}
	return problems, nil
}
