package turn

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/projects"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// This file joins projects (P9-01) to the API and to each model call. A
// project has instructions, which every call of a chat in it carries in
// the standing start's project part, and knowledge files, which every call
// carries with the attachments; its chats keep their facts in the
// project's memory space, and recall in them searches that space first.
// internal/projects keeps the rows and the files and holds fixed rule 3's
// answer for a file; it reads no setting, so the settings are read here.
// docs/architecture/projects.md is the whole of it.

// ProjectLimits are the limits a project screen needs to know.
type ProjectLimits struct {
	// MaxProjects is projects.max_projects.
	MaxProjects int `json:"max_projects"`
	// NameMaxChars is projects.name_max_chars.
	NameMaxChars int `json:"name_max_chars"`
	// InstructionsMaxChars is projects.instructions_max_chars.
	InstructionsMaxChars int `json:"instructions_max_chars"`
	// MaxFiles is projects.max_files.
	MaxFiles int `json:"max_files"`
	// FileMaxBytes is the largest file a project takes: the smaller of
	// projects.file_max_bytes and the API's own limit on a body.
	FileMaxBytes int64 `json:"file_max_bytes"`
	// TextExtensions are the endings of the files a project takes.
	TextExtensions []string `json:"text_extensions"`
}

// ProjectSummary is one project in the list.
type ProjectSummary struct {
	projects.Project
	// Files and Chats count the project's files and chats.
	Files int `json:"files"`
	Chats int `json:"chats"`
}

// ProjectsReply answers GET projects.
type ProjectsReply struct {
	// Projects are every project of the person's, archived ones included,
	// the one changed last first.
	Projects []ProjectSummary `json:"projects"`
	Limits   ProjectLimits    `json:"limits"`
}

// ProjectReply is one project with its files and its chats.
type ProjectReply struct {
	Project projects.Project `json:"project"`
	// Files are its knowledge files, in the order they were added, without
	// their text.
	Files []projects.File `json:"files"`
	// Chats are its chats, the one with the newest message first.
	Chats  []Chat        `json:"chats"`
	Limits ProjectLimits `json:"limits"`
}

// ProjectBody makes or changes a project. A field that is absent is left
// as it is; making a project needs a name.
type ProjectBody struct {
	Name         *string `json:"name,omitempty"`
	Instructions *string `json:"instructions,omitempty"`
	// Archived sets the project aside, or brings it back.
	Archived *bool `json:"archived,omitempty"`
	// MemorySpace is the knowledge store space the project's chats keep
	// their facts in from now on. Empty keeps them with the person's other
	// facts.
	MemorySpace *string `json:"memory_space,omitempty"`
}

// ProjectFileReply is one knowledge file, with the text read from it.
type ProjectFileReply struct {
	projects.File
	// Text is the text read from the file, as a call is given it before it
	// is cut to projects.file_context_max_chars.
	Text string `json:"text"`
	// New is false when the project had the same file already. (The file's
	// own "added" is when it was added.)
	New bool `json:"new"`
}

// ProjectFileBody marks a knowledge file.
type ProjectFileBody struct {
	// Sensitive keeps the file to calls whose every model may read
	// sensitive content.
	Sensitive *bool `json:"sensitive,omitempty"`
}

// ChatProjectReply says which project a chat is in.
type ChatProjectReply struct {
	ChatID string `json:"chat_id"`
	// Project is absent when the chat is in none.
	Project *projects.Project `json:"project,omitempty"`
}

// RemovedReply answers the removal of a project's file.
type RemovedReply struct {
	Removed bool `json:"removed"`
}

// ProjectRoutes returns the project routes (P9-01), served by r.
func ProjectRoutes(r *Runner) []api.Route {
	const (
		list  = api.Prefix + "projects"
		one   = list + "/{id}"
		files = one + "/files"
		file  = files + "/{file}"
		chats = one + "/chats"
	)
	return []api.Route{
		{Method: "GET", Path: list, Handle: r.projectList, Reply: ProjectsReply{},
			Summary: "The person's projects, archived ones included, the one changed last first, with how many files and chats each has, and the limits a project screen needs."},
		{Method: "POST", Path: list, Handle: r.projectCreate, Body: ProjectBody{}, Reply: ProjectReply{},
			Summary: "Make a project, with a name and, if given, its instructions. Its memory space is named from projects.memory_space. Past projects.max_projects it is refused (409 too_many)."},
		{Method: "GET", Path: one, Handle: r.projectRead, Reply: ProjectReply{},
			Summary: "One project: its name, instructions and memory space, its knowledge files, and its chats."},
		{Method: "PATCH", Path: one, Handle: r.projectPatch, Body: ProjectBody{}, Reply: ProjectReply{},
			Summary: "Rename a project, change its instructions or its memory space, or archive it and bring it back. A field that is absent is left as it is."},
		{Method: "DELETE", Path: one, Handle: r.projectDelete, Reply: DeletedReply{},
			Summary: "Delete a project and its knowledge files. Its chats are kept, as ordinary chats."},
		{Method: "POST", Path: files, Handle: r.projectFileAdd, Reply: ProjectFileReply{},
			Query:   []api.Param{{Name: "name", Description: "The file's name. Its ending says what kind of text file it is."}},
			Summary: "Add a knowledge file to a project. The body is the file's bytes: a UTF-8 text file whose ending is in projects.text_extensions, no larger than projects.file_max_bytes. Its text is read once, and every call of the project's chats carries it with the attachments. It is kept under its SHA-256, which is its id. The same file twice is one file (200, new false); a new one answers 201."},
		{Method: "GET", Path: file, Handle: r.projectFileRead, Reply: ProjectFileReply{},
			Summary: "One knowledge file of a project, with the text read from it."},
		{Method: "PATCH", Path: file, Handle: r.projectFilePatch, Body: ProjectFileBody{}, Reply: ProjectFileReply{},
			Summary: "Mark a knowledge file sensitive, or ordinary again. A sensitive file reaches only calls whose every model may read sensitive content, and the chat it reaches is held to such models from then on."},
		{Method: "DELETE", Path: file, Handle: r.projectFileDelete, Reply: RemovedReply{},
			Summary: "Take a knowledge file off a project, and remove it when nothing else holds it."},
		{Method: "POST", Path: chats, Handle: r.projectChatNew, Reply: ChatReply{},
			Summary: "Start a chat in a project: a new chat with no messages, whose every call carries the project's instructions and files."},
		{Method: "PUT", Path: chats + "/{chat}", Handle: r.projectChatPut, Reply: ChatProjectReply{},
			Summary: "Move a chat into a project, out of any other it was in. Its next call carries the project's instructions and files. It takes no body."},
		{Method: "DELETE", Path: chats + "/{chat}", Handle: r.projectChatDelete, Reply: ChatProjectReply{},
			Summary: "Take a chat out of a project. A chat in another project, or in none, is left as it is."},
		{Method: "GET", Path: api.Prefix + "chats/{id}/project", Handle: r.chatProject, Reply: ChatProjectReply{},
			Summary: "The project a chat is in, if any."},
	}
}

// projectOptions reads what a project takes from the person's settings.
func (r *Runner) projectOptions(ctx context.Context, s *tenant.Store) (projects.Options, error) {
	var bad []string
	whole := func(key string) int {
		n, err := strconv.Atoi(r.setting(ctx, s, key))
		if err != nil {
			bad = append(bad, key)
		}
		return n
	}
	o := projects.Options{
		MaxProjects:          whole(projects.SettingMaxProjects),
		NameMaxChars:         whole(projects.SettingNameMaxChars),
		InstructionsMaxChars: whole(projects.SettingInstructionsMaxChars),
		MaxFiles:             whole(projects.SettingMaxFiles),
		FileMaxBytes:         int64(whole(projects.SettingFileMaxBytes)),
		TextExtensions:       projects.Extensions(r.setting(ctx, s, projects.SettingTextExtensions)),
		MemorySpace:          r.setting(ctx, s, projects.SettingMemorySpace),
	}
	if len(bad) > 0 {
		return projects.Options{}, errors.New("these settings do not read: " + strings.Join(bad, ", "))
	}
	return o, projects.CheckOptions(o)
}

// projectWords reads what shapes a call's project parts.
func (r *Runner) projectWords(ctx context.Context, s *tenant.Store) (projects.Words, error) {
	n, err := strconv.Atoi(r.setting(ctx, s, projects.SettingFileContextMaxChars))
	if err != nil {
		return projects.Words{}, errors.New("the setting does not read: " + projects.SettingFileContextMaxChars)
	}
	w := projects.Words{
		InstructionsHeading: r.setting(ctx, s, projects.SettingInstructionsHeading),
		FileHeading:         r.setting(ctx, s, projects.SettingFileHeading),
		CutNote:             r.setting(ctx, s, projects.SettingFileCutNote),
		FileMaxChars:        n,
	}
	return w, projects.CheckWords(w)
}

// settingMaxBody is the API's own limit on a request's body, in
// settings/core.json. Until a route can have a larger one (P9-02), it
// bounds a project's file too.
const settingMaxBody = "server.max_body_bytes"

func (r *Runner) projectLimits(ctx context.Context, s *tenant.Store, o projects.Options) ProjectLimits {
	most := o.FileMaxBytes
	if body, err := strconv.ParseInt(r.setting(ctx, s, settingMaxBody), 10, 64); err == nil && body > 0 && body < most {
		most = body
	}
	exts := o.TextExtensions
	if exts == nil {
		exts = []string{}
	}
	return ProjectLimits{
		MaxProjects: o.MaxProjects, NameMaxChars: o.NameMaxChars, InstructionsMaxChars: o.InstructionsMaxChars,
		MaxFiles: o.MaxFiles, FileMaxBytes: most, TextExtensions: exts,
	}
}

// projectFailed answers an error of internal/projects, or of the chat
// record.
func (r *Runner) projectFailed(w http.ResponseWriter, req *http.Request, c api.Call, err error) {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.Is(err, projects.ErrNotFound):
		api.Error(w, req, http.StatusNotFound, "not_found", "there is no such project, file or chat")
	case errors.Is(err, projects.ErrInvalid):
		refused(w, req, projects.Reason(err))
	case errors.Is(err, projects.ErrFull):
		api.Error(w, req, http.StatusConflict, "too_many", projects.Reason(err))
	case errors.As(err, &tooLarge):
		api.Error(w, req, http.StatusRequestEntityTooLarge, "too_large", "the file is larger than the most a request may carry, "+projects.Size(tooLarge.Limit))
	default:
		r.answer(w, req, c, err)
	}
}

// projectReady makes the runner ready and reads the project options, or
// answers why not.
func (r *Runner) projectReady(w http.ResponseWriter, req *http.Request, c api.Call) (projects.Options, bool) {
	if err := r.setup(); err != nil {
		r.answer(w, req, c, err)
		return projects.Options{}, false
	}
	o, err := r.projectOptions(req.Context(), c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return projects.Options{}, false
	}
	return o, true
}

func (r *Runner) projectList(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.projectReady(w, req, c)
	if !ok {
		return
	}
	all, err := projects.List(req.Context(), c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	files, chats, err := projects.Counts(req.Context(), c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply := ProjectsReply{Projects: []ProjectSummary{}, Limits: r.projectLimits(req.Context(), c.Store, o)}
	for _, p := range all {
		reply.Projects = append(reply.Projects, ProjectSummary{Project: p, Files: files[p.ID], Chats: chats[p.ID]})
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

// projectAnswer writes one project with its files and chats.
func (r *Runner) projectAnswer(w http.ResponseWriter, req *http.Request, c api.Call, o projects.Options, id string, status int) {
	ctx := req.Context()
	p, err := projects.Get(ctx, c.Store, id)
	if err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	files, err := projects.Files(ctx, c.Store, id)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	ids, err := projects.Chats(ctx, c.Store, id)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	busy, err := busyChats(ctx, c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply := ProjectReply{Project: p, Files: []projects.File{}, Chats: []Chat{}, Limits: r.projectLimits(req.Context(), c.Store, o)}
	reply.Files = append(reply.Files, files...)
	for _, cid := range ids {
		ch, err := chat.Get(c.Store, cid)
		if errors.Is(err, chat.ErrNotFound) {
			continue // deleted while this was read
		}
		if err != nil {
			r.answer(w, req, c, err)
			return
		}
		reply.Chats = append(reply.Chats, shown(ch, busy[ch.ID]))
	}
	sort.SliceStable(reply.Chats, func(i, j int) bool { return reply.Chats[i].Active.After(reply.Chats[j].Active) })
	r.withSummaries(ctx, c.Store, reply.Chats)
	api.WriteJSON(w, status, reply)
}

func (r *Runner) projectCreate(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.projectReady(w, req, c)
	if !ok {
		return
	}
	var body ProjectBody
	if !api.ReadJSON(w, req, &body) {
		return
	}
	if body.Name == nil {
		refused(w, req, "a project needs a name")
		return
	}
	if body.Archived != nil || body.MemorySpace != nil {
		refused(w, req, "a project is made with a name and instructions; archive it or change its memory space afterwards")
		return
	}
	text := ""
	if body.Instructions != nil {
		text = *body.Instructions
	}
	p, err := projects.Create(req.Context(), c.Store, o, *body.Name, text)
	if err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	r.projectAnswer(w, req, c, o, p.ID, http.StatusCreated)
}

func (r *Runner) projectRead(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.projectReady(w, req, c)
	if !ok {
		return
	}
	r.projectAnswer(w, req, c, o, req.PathValue("id"), http.StatusOK)
}

func (r *Runner) projectPatch(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.projectReady(w, req, c)
	if !ok {
		return
	}
	id := req.PathValue("id")
	if _, err := projects.Get(req.Context(), c.Store, id); err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	var body ProjectBody
	if !api.ReadJSON(w, req, &body) {
		return
	}
	if body.Name == nil && body.Instructions == nil && body.Archived == nil && body.MemorySpace == nil {
		refused(w, req, "give a name, instructions, archived or memory_space")
		return
	}
	change := projects.Change{Name: body.Name, Instructions: body.Instructions, Archived: body.Archived, MemorySpace: body.MemorySpace}
	if _, err := projects.Update(req.Context(), c.Store, o, id, change); err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	r.projectAnswer(w, req, c, o, id, http.StatusOK)
}

func (r *Runner) projectDelete(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.projectReady(w, req, c)
	if !ok {
		return
	}
	if err := projects.Delete(req.Context(), c.Store, o, req.PathValue("id")); err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, DeletedReply{Deleted: true})
}

func (r *Runner) projectFileAdd(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.projectReady(w, req, c)
	if !ok {
		return
	}
	id := req.PathValue("id")
	if _, err := projects.Get(req.Context(), c.Store, id); err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	data, err := io.ReadAll(req.Body)
	if err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	f, added, err := projects.AddFile(req.Context(), c.Store, o, id, req.URL.Query().Get("name"), data)
	if err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	status := http.StatusOK
	if added {
		status = http.StatusCreated
	}
	api.WriteJSON(w, status, ProjectFileReply{File: f, Text: f.Text(), New: added})
}

func (r *Runner) projectFileRead(w http.ResponseWriter, req *http.Request, c api.Call) {
	f, err := projects.GetFile(req.Context(), c.Store, req.PathValue("id"), req.PathValue("file"))
	if err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, ProjectFileReply{File: f, Text: f.Text()})
}

func (r *Runner) projectFilePatch(w http.ResponseWriter, req *http.Request, c api.Call) {
	id, file := req.PathValue("id"), req.PathValue("file")
	if _, err := projects.GetFile(req.Context(), c.Store, id, file); err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	var body ProjectFileBody
	if !api.ReadJSON(w, req, &body) {
		return
	}
	if body.Sensitive == nil {
		refused(w, req, "give sensitive, true or false")
		return
	}
	f, err := projects.MarkFile(req.Context(), c.Store, id, file, *body.Sensitive)
	if err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, ProjectFileReply{File: f, Text: f.Text()})
}

func (r *Runner) projectFileDelete(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.projectReady(w, req, c)
	if !ok {
		return
	}
	removed, err := projects.RemoveFile(req.Context(), c.Store, o, req.PathValue("id"), req.PathValue("file"))
	if err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	if !removed {
		r.projectFailed(w, req, c, projects.ErrNotFound)
		return
	}
	api.WriteJSON(w, http.StatusOK, RemovedReply{Removed: true})
}

func (r *Runner) projectChatNew(w http.ResponseWriter, req *http.Request, c api.Call) {
	ctx := req.Context()
	id := req.PathValue("id")
	if _, err := projects.Get(ctx, c.Store, id); err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	if err := r.setup(); err != nil {
		r.answer(w, req, c, err)
		return
	}
	made, err := chat.Create(c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	if _, err := projects.Move(ctx, c.Store, id, made.ID); err != nil {
		if derr := chat.Delete(c.Store, made.ID); derr != nil {
			r.logf("%s starting a chat in project %s: removing the new chat %s again: %v", c.RequestID, id, made.ID, derr)
		}
		r.projectFailed(w, req, c, err)
		return
	}
	reply, err := r.whole(ctx, c.Store, made)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, reply)
}

// chatProjectAnswer writes which project a chat is in.
func (r *Runner) chatProjectAnswer(w http.ResponseWriter, req *http.Request, c api.Call, chatID string) {
	p, ok, err := projects.Of(req.Context(), c.Store, chatID)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply := ChatProjectReply{ChatID: chatID}
	if ok {
		reply.Project = &p
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

func (r *Runner) projectChatPut(w http.ResponseWriter, req *http.Request, c api.Call) {
	id, chatID := req.PathValue("id"), req.PathValue("chat")
	if _, err := chat.Get(c.Store, chatID); err != nil {
		r.answer(w, req, c, err) // another tenant's chat is not in this store: not found
		return
	}
	if _, err := projects.Move(req.Context(), c.Store, id, chatID); err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	r.chatProjectAnswer(w, req, c, chatID)
}

func (r *Runner) projectChatDelete(w http.ResponseWriter, req *http.Request, c api.Call) {
	id, chatID := req.PathValue("id"), req.PathValue("chat")
	if _, err := chat.Get(c.Store, chatID); err != nil {
		r.answer(w, req, c, err)
		return
	}
	if _, err := projects.Leave(req.Context(), c.Store, id, chatID); err != nil {
		r.projectFailed(w, req, c, err)
		return
	}
	r.chatProjectAnswer(w, req, c, chatID)
}

func (r *Runner) chatProject(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	r.chatProjectAnswer(w, req, c, id)
}

// projectOf returns the project a turn's chat is in. A chat whose project
// cannot be read is taken to be in none, with a line in the log: it then
// carries nothing of a project, which no rule forbids.
func (r *Runner) projectOf(ctx context.Context, s *tenant.Store, t row) (projects.Project, bool) {
	p, ok, err := projects.Of(ctx, s, t.ChatID)
	if err != nil {
		r.logf("turns: %s turn %s: reading the chat's project, so it carries none: %v", t.requestID, t.ID, err)
		return projects.Project{}, false
	}
	return p, ok
}

// projectParts returns what one model call of a turn carries of the
// project its chat is in: the project part of the standing start (the
// heading and the instructions) and the knowledge files, as items of the
// attached part. A chat in no project carries neither. p and m are the
// call's provider and model, so the files are chosen for the least of the
// call's route: a file marked sensitive is given only when every model of
// the route may read it, and the chat is held first; otherwise it is left
// out, and a line says how many were. Nothing here stops a turn.
func (r *Runner) projectParts(ctx context.Context, s *tenant.Store, t row, p seam.Provider, m models.Model) (string, []contextbuild.Item) {
	pr, ok := r.projectOf(ctx, s, t)
	if !ok {
		return "", nil
	}
	w, err := r.projectWords(ctx, s)
	if err != nil {
		r.logf("turns: %s turn %s: the project's settings do not read, so the call carries nothing of project %s: %v", t.requestID, t.ID, pr.ID, err)
		return "", nil
	}
	instructions := projects.Instructions(w, pr)
	files, err := projects.Files(ctx, s, pr.ID)
	if err != nil {
		r.logf("turns: %s turn %s: reading project %s's files, so none is carried: %v", t.requestID, t.ID, pr.ID, err)
		return instructions, nil
	}
	mayRead := readsSensitive(p, m) // for the least of the route's models
	var items []contextbuild.Item
	var sensitive []int
	withheld := 0
	for _, f := range files {
		if !projects.MayShow(f, mayRead) {
			withheld++
			continue
		}
		if f.Sensitive {
			sensitive = append(sensitive, len(items))
		}
		label, text := projects.FileItem(w, pr, f)
		items = append(items, contextbuild.Item{Label: label, Text: text})
	}
	if len(sensitive) > 0 {
		// Held whatever happens to the call: what it carries stays in the
		// chat and is sent again with every later call of it.
		if err := recall.Hold(context.Background(), s, t.ID, t.ChatID); err != nil {
			r.logf("turns: %s turn %s: holding the chat for a sensitive project file, so it is left out: %v", t.requestID, t.ID, err)
			kept := items[:0:0]
			for i, it := range items {
				if !contains(sensitive, i) {
					kept = append(kept, it)
				}
			}
			items, withheld = kept, withheld+len(sensitive)
		}
	}
	if withheld > 0 {
		items = append(items, contextbuild.Item{Label: projects.LeftOutLabel, Text: projects.LeftOut(withheld), Pinned: true})
	}
	return instructions, items
}

// projectSensitive says whether the project a turn's chat is in holds a
// file marked sensitive. Runner.sensitive asks it for a chat whose own
// model may read such content, so the route keeps to models that may, as
// it does for an attached chat (decision 0403). A project whose files
// cannot be read counts as holding one.
func (r *Runner) projectSensitive(ctx context.Context, s *tenant.Store, t row) bool {
	p, ok, err := projects.Of(ctx, s, t.ChatID)
	if err != nil {
		r.logf("turns: %s turn %s: reading the chat's project, so it is taken that it holds a sensitive file: %v", t.requestID, t.ID, err)
		return true
	}
	if !ok {
		return false
	}
	yes, err := projects.Sensitive(ctx, s, p.ID)
	if err != nil {
		r.logf("turns: %s turn %s: reading whether project %s holds a sensitive file, so it is taken that it does: %v", t.requestID, t.ID, p.ID, err)
		return true
	}
	return yes
}

// projectMemorySpace is the space a turn's facts go to: the project's
// memory space when the chat is in a project that has one, and otherwise
// space, knowledge.memory_space (decision 0400). The memory pass asks it
// for a chat that was not sent sensitive content; a held chat's facts go
// to the confirmed sensitive space or nowhere, whatever its project.
func (r *Runner) projectMemorySpace(ctx context.Context, s *tenant.Store, t row, space string) string {
	if p, ok := r.projectOf(ctx, s, t); ok && p.MemorySpace != "" {
		return p.MemorySpace
	}
	return space
}

// preferProject gives recall a store that searches the project's memory
// space first, when the turn's chat is in a project that has one and the
// person has projects.recall_prefer on. Every hit keeps its label, so
// recall's answer for sensitive content is unchanged.
func (r *Runner) preferProject(ctx context.Context, s *tenant.Store, t row, ks knowledge.Store) knowledge.Store {
	if on, err := strconv.ParseBool(r.setting(ctx, s, projects.SettingRecallPrefer)); err != nil || !on {
		return ks
	}
	if p, ok := r.projectOf(ctx, s, t); ok {
		return projects.Prefer(ks, p.MemorySpace)
	}
	return ks
}
