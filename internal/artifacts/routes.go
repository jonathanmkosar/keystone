package artifacts

import (
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/api"
)

// The artifact routes. Each acts for the signed-in person on their own
// tenant, with the store the API shell hands it, so an artifact of one
// tenant is never served to another: another tenant's store has another
// record and another artifacts folder, and answers 404.

// ListReply answers GET chats/{id}/artifacts and GET artifacts.
type ListReply struct {
	Artifacts []Artifact `json:"artifacts"`
}

// OneReply answers GET artifacts/{artifact}: the artifact, every version
// kept, and how its frame is sandboxed.
type OneReply struct {
	Artifact Artifact  `json:"artifact"`
	Versions []Version `json:"versions"`
	// Sandbox is the frame's sandbox attribute: the permissions
	// artifacts.sandbox gives it, never allow-same-origin.
	Sandbox string `json:"sandbox"`
	// Notice is what the person is told about a sensitive artifact, on the
	// panel and on the publish link. Empty for any other.
	Notice string `json:"notice,omitempty"`
}

// VersionReply answers GET artifacts/{artifact}/versions/{version}.
type VersionReply struct {
	Artifact Artifact `json:"artifact"`
	Version  Version  `json:"version"`
	Content  string   `json:"content"`
}

// CompareReply answers GET artifacts/{artifact}/compare: the lines of two
// versions, as Diff gives them.
type CompareReply struct {
	From    int        `json:"from"`
	To      int        `json:"to"`
	Lines   []DiffLine `json:"lines"`
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
}

// SensitiveNotice is what a sensitive artifact's panel and publish link
// say.
const SensitiveNotice = "Sensitive: this artifact was made in a chat that holds content from a sensitive space. Its link opens only for you, signed in; treat what it shows as sensitive."

// Routes returns the artifact routes.
func Routes(o Options) []api.Route {
	const (
		list    = api.Prefix + "artifacts"
		one     = list + "/{artifact}"
		version = one + "/versions/{version}"
	)
	h := handlers{o: o, rt: FindRuntime(o.Runtime)}
	return []api.Route{
		{Method: "GET", Path: api.Prefix + "chats/{id}/artifacts", Handle: h.chat, Reply: ListReply{},
			Summary: "A chat's artifacts, the oldest first, each as its newest version has it."},
		{Method: "GET", Path: list, Handle: h.all, Reply: ListReply{},
			Summary: "Every artifact of the person, the one changed last first."},
		{Method: "GET", Path: one, Handle: h.one, Reply: OneReply{},
			Summary: "One artifact, with every version kept, how its frame is sandboxed, and what the person is told when it is sensitive."},
		{Method: "GET", Path: one + "/compare", Handle: h.compare, Reply: CompareReply{},
			Query: []Param{
				{Name: "from", Description: "The older version's number."},
				{Name: "to", Description: "The newer version's number. Leave it out for the newest."},
			},
			Summary: "Two versions of an artifact compared line by line: the lines both hold, those put in and those taken out."},
		{Method: "GET", Path: version, Handle: h.version, Reply: VersionReply{},
			Summary: "One version of an artifact, with its content."},
		{Method: "POST", Path: version + "/restore", Handle: h.restore, Reply: VersionReply{},
			Summary: "Restore an older version of an artifact: it is written again as the newest version, and every version stays."},
		{Method: "GET", Path: version + "/download", Handle: h.download,
			Summary: "One version of an artifact as a file to save, named by its title, with the file type its type has."},
		{Method: "GET", Path: version + "/sandbox", Handle: h.sandbox,
			Query:   []Param{{Name: "theme", Description: "dark or light: the theme of the page around the artifact."}},
			Summary: "The page one version of an artifact is shown in, inside a sandboxed frame: served with a policy of its own that fetches and sends nothing and gives the page no origin."},
	}
}

// Param is api.Param, named here so the route list reads.
type Param = api.Param

type handlers struct {
	o  Options
	rt Runtime
}

// answer writes an error from this package as the API's error body.
func answer(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		api.Error(w, r, http.StatusNotFound, "not_found", "there is no such chat, artifact or version")
	case errors.Is(err, ErrRefused):
		api.Error(w, r, http.StatusBadRequest, "refused", strings.TrimPrefix(err.Error(), ErrRefused.Error()+": "))
	default:
		api.Error(w, r, http.StatusInternalServerError, "failed", "the call failed")
	}
}

func (h handlers) chat(w http.ResponseWriter, r *http.Request, c api.Call) {
	list, err := List(r.Context(), c.Store, r.PathValue("id"))
	if err != nil {
		answer(w, r, err)
		return
	}
	if list == nil {
		list = []Artifact{}
	}
	api.WriteJSON(w, http.StatusOK, ListReply{Artifacts: list})
}

func (h handlers) all(w http.ResponseWriter, r *http.Request, c api.Call) {
	list, err := ListAll(r.Context(), c.Store)
	if err != nil {
		answer(w, r, err)
		return
	}
	if list == nil {
		list = []Artifact{}
	}
	api.WriteJSON(w, http.StatusOK, ListReply{Artifacts: list})
}

func (h handlers) one(w http.ResponseWriter, r *http.Request, c api.Call) {
	id := r.PathValue("artifact")
	a, err := Get(r.Context(), c.Store, id)
	if err != nil {
		answer(w, r, err)
		return
	}
	versions, err := Versions(r.Context(), c.Store, id)
	if err != nil {
		answer(w, r, err)
		return
	}
	reply := OneReply{Artifact: a, Versions: versions, Sandbox: h.o.SandboxTokens()}
	if a.Sensitive {
		reply.Notice = SensitiveNotice
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

// versionOf reads the {version} part of a path. Anything that is not a
// number names no version.
func versionOf(r *http.Request) (int, error) {
	n, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || n < 1 {
		return 0, ErrNotFound
	}
	return n, nil
}

func (h handlers) version(w http.ResponseWriter, r *http.Request, c api.Call) {
	n, err := versionOf(r)
	if err != nil {
		answer(w, r, err)
		return
	}
	id := r.PathValue("artifact")
	v, content, err := Content(r.Context(), c.Store, id, n)
	if err != nil {
		answer(w, r, err)
		return
	}
	a, err := Get(r.Context(), c.Store, id)
	if err != nil {
		answer(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, VersionReply{Artifact: a, Version: v, Content: content})
}

func (h handlers) restore(w http.ResponseWriter, r *http.Request, c api.Call) {
	n, err := versionOf(r)
	if err != nil {
		answer(w, r, err)
		return
	}
	a, v, err := Restore(r.Context(), c.Store, h.o, r.PathValue("artifact"), n, c.RequestID)
	if err != nil {
		answer(w, r, err)
		return
	}
	_, content, err := Content(r.Context(), c.Store, a.ID, v.Version)
	if err != nil {
		answer(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, VersionReply{Artifact: a, Version: v, Content: content})
}

func (h handlers) compare(w http.ResponseWriter, r *http.Request, c api.Call) {
	id := r.PathValue("artifact")
	a, err := Get(r.Context(), c.Store, id)
	if err != nil {
		answer(w, r, err)
		return
	}
	q := r.URL.Query()
	from, ferr := strconv.Atoi(q.Get("from"))
	to := a.Latest
	var terr error
	if q.Get("to") != "" {
		to, terr = strconv.Atoi(q.Get("to"))
	}
	if ferr != nil || terr != nil || from < 1 || to < 1 {
		api.Error(w, r, http.StatusBadRequest, "bad_request", "from and to are the numbers of two versions")
		return
	}
	_, older, err := Content(r.Context(), c.Store, id, from)
	if err != nil {
		answer(w, r, err)
		return
	}
	_, newer, err := Content(r.Context(), c.Store, id, to)
	if err != nil {
		answer(w, r, err)
		return
	}
	diff := Diff(older, newer)
	added, removed := Counts(diff)
	api.WriteJSON(w, http.StatusOK, CompareReply{From: from, To: to, Lines: diff, Added: added, Removed: removed})
}

// download serves one version as a file to save. Every answer is an
// attachment under a policy that allows nothing, so not even an HTML or an
// SVG artifact is ever drawn from it.
func (h handlers) download(w http.ResponseWriter, r *http.Request, c api.Call) {
	n, err := versionOf(r)
	if err != nil {
		answer(w, r, err)
		return
	}
	id := r.PathValue("artifact")
	a, err := Get(r.Context(), c.Store, id)
	if err != nil {
		answer(w, r, err)
		return
	}
	v, content, err := Content(r.Context(), c.Store, id, n)
	if err != nil {
		answer(w, r, err)
		return
	}
	name, media := FileOf(a, v)
	head := w.Header()
	head.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	head.Set("Content-Type", media)
	head.Set("Content-Length", strconv.Itoa(len(content)))
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	if disposition == "" {
		disposition = "attachment" // a name the header cannot carry is left out
	}
	head.Set("Content-Disposition", disposition)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(content))
}

// sandbox serves the page one version is shown in. Its policy replaces the
// one the server gives every page, for this answer alone, and it may be
// framed by Keystone's own pages and by nothing else.
func (h handlers) sandbox(w http.ResponseWriter, r *http.Request, c api.Call) {
	n, err := versionOf(r)
	if err != nil {
		answer(w, r, err)
		return
	}
	id := r.PathValue("artifact")
	a, err := Get(r.Context(), c.Store, id)
	if err != nil {
		answer(w, r, err)
		return
	}
	v, content, err := Content(r.Context(), c.Store, id, n)
	if err != nil {
		answer(w, r, err)
		return
	}
	page := SandboxPage(a.Type, v.Title, content, r.URL.Query().Get("theme"), h.rt)
	head := w.Header()
	head.Set("Content-Security-Policy", Policy(a.Type, r.Host, h.o.SandboxTokens()))
	head.Set("X-Frame-Options", "SAMEORIGIN")
	head.Set("Cross-Origin-Resource-Policy", "same-origin")
	head.Set("Content-Type", "text/html; charset=utf-8")
	head.Set("Content-Length", strconv.Itoa(len(page)))
	w.WriteHeader(http.StatusOK)
	w.Write(page)
}
