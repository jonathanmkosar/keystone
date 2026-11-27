package isolationdrill

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/shareapi"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/wikijobs"
)

var param = regexp.MustCompile(`\{([a-z_]+)\}`)

// skipped are routes the walk with the first person's ids leaves out, and
// why. Each takes no id of a person's anywhere, and acts on the caller's
// own data only, which the tenant isolation check already proves for every
// route.
var skipped = map[string]string{
	"DELETE /api/v1/session":            "signs the caller out; takes no id",
	"POST /api/v1/agents/pause":         "pauses the caller's own agents; takes no id",
	"POST /api/v1/agents/resume":        "takes no id",
	"POST /api/v1/schedules/pause-all":  "holds the caller's own schedules; takes no id",
	"POST /api/v1/schedules/resume-all": "takes no id",
	"POST /api/v1/wiki-jobs/{job}/run":  "a job's name is the install's, not a person's; the jobs part runs one",
	"POST /api/v1/wiki-jobs/{job}/on":   "a job's name is the install's, not a person's",
	"POST /api/v1/wiki-jobs/{job}/off":  "a job's name is the install's, not a person's",
}

// pathValue is the first person's id for one part of a route's path.
func (g *rig) pathValue(path, name string) string {
	f := g.first
	approvals := strings.Contains(path, "/approvals")
	switch name {
	case "id":
		switch {
		case strings.HasPrefix(path, api.Prefix+"projects/"):
			return f.project
		case strings.HasPrefix(path, api.Prefix+"schedules/"):
			return f.schedule
		case strings.HasPrefix(path, api.Prefix+"styles/"):
			return f.style
		case approvals:
			return f.chatD
		case strings.HasSuffix(path, "/close"), strings.HasSuffix(path, "/incognito"):
			return f.incognito
		}
		return f.chatA
	case "turn":
		if approvals {
			return f.turnD
		}
		return f.turnA
	case "approval":
		return f.approval
	case "task":
		return f.task
	case "upload":
		return f.upload
	case "message":
		return strconv.FormatInt(f.messageA, 10)
	case "other", "chat":
		if name == "other" {
			return f.chatB
		}
		return f.chatA
	case "artifact":
		return f.artifact
	case "version":
		return "2"
	case "file":
		return f.projectFile
	case "kind":
		return "page"
	case "ref":
		return f.page
	case "key":
		return "agents.time_zone"
	case "name":
		switch {
		case strings.HasPrefix(path, api.Prefix+"models/"):
			return modelOne.Name
		case strings.HasPrefix(path, api.Prefix+"secrets/"):
			return secretName
		case strings.HasPrefix(path, api.Prefix+"tools/"):
			return "current_time"
		}
		return connectorName
	case "job":
		return wikijobs.JobBuiltinCompile
	case "share":
		return f.shareLink
	case "sub":
		return f.subagent
	case "research":
		return f.research
	}
	return ""
}

// fill puts ids into a route's path: the first person's, or with own the
// second person's own for the first part and the first person's for the
// rest.
func (g *rig) fill(path string, own bool) string {
	n := 0
	return param.ReplaceAllStringFunc(path, func(m string) string {
		name := m[1 : len(m)-1]
		n++
		if own && n == 1 && name == "id" {
			switch {
			case strings.HasPrefix(path, api.Prefix+"projects/"):
				return g.second.project
			case strings.HasPrefix(path, api.Prefix+"chats/"):
				return g.second.chat
			}
		}
		v := g.pathValue(path, name)
		if v == "" {
			// A new route's part the drill has no id for would be tried
			// with nothing, and refused for the wrong reason.
			g.report.reached("the drill has no id of the first person's for {%s} in %s: add it to pathValue", name, path)
		}
		return url.PathEscape(v)
	})
}

// bodyFor is the body the walk sends a route: the first person's ids and
// names wherever the route takes one.
func (g *rig) bodyFor(method, path string) (string, []byte) {
	f := g.first
	route := method + " " + path
	switch route {
	case "PATCH /api/v1/chats/{id}":
		return `{"title":"Taken","starred":false}`, nil
	case "PUT /api/v1/chats/{id}/connectors/{name}":
		return `{"on":false}`, nil
	case "POST /api/v1/chats/{id}/select":
		return jsonOf(turn.SelectBody{MessageID: f.messageA}), nil
	case "PUT /api/v1/chats/{id}/style":
		return jsonOf(turn.ChatStyleBody{Style: f.style}), nil
	case "POST /api/v1/chats/{id}/tasks":
		return `{"text":"drill:noop","title":"Taken"}`, nil
	case "POST /api/v1/chats/{id}/turns":
		return jsonOf(turn.SendBody{Text: "drill:noop", EditOf: f.messageA}), nil
	case "POST /api/v1/chats/{id}/uploads", "POST /api/v1/projects/{id}/files":
		return "", []byte("The second person's invented file.\n")
	case "POST /api/v1/connectors", "PUT /api/v1/connectors/{name}":
		return fmt.Sprintf(`{"name":%q,"url":%q,"local":true,"secret":%q}`, connectorName, g.notesURL, tokenName), nil
	case "POST /api/v1/google/finish":
		return jsonOf(map[string]string{"state": f.googleState, "code": "a guessed code"}), nil
	case "POST /api/v1/google/start":
		return `{}`, nil
	case "POST /api/v1/memory/correct":
		return jsonOf(map[string]string{"source_id": f.memorySource, "fact": "A guessed fact.", "text": "A changed fact."}), nil
	case "POST /api/v1/memory/forget":
		return jsonOf(map[string]string{"source_id": f.memorySource, "fact": "A guessed fact."}), nil
	case "POST /api/v1/models", "PUT /api/v1/models/{name}":
		return jsonOf(modelOne), nil
	case "PUT /api/v1/profile":
		return `{"text":"The second person's own profile."}`, nil
	case "POST /api/v1/projects":
		return jsonOf(map[string]string{"name": projectName}), nil
	case "PATCH /api/v1/projects/{id}":
		return `{"name":"Taken"}`, nil
	case "PATCH /api/v1/projects/{id}/files/{file}":
		return `{"sensitive":true}`, nil
	case "POST /api/v1/schedules", "PUT /api/v1/schedules/{id}":
		off := false
		return jsonOf(schedules.ScheduleBody{Name: scheduleName, Prompt: "drill:noop", Recurrence: schedules.Recurrence{Kind: schedules.KindDaily, Time: "03:18"}, Enabled: &off}), nil
	case "PUT /api/v1/secrets/{name}":
		return `{"value":"the second person's own invented value"}`, nil
	case "PUT /api/v1/settings/{key}":
		return `{"value":"America/Chicago"}`, nil
	case "POST /api/v1/styles":
		return jsonOf(map[string]string{"name": styleName, "instructions": "The second person's own style."}), nil
	case "PATCH /api/v1/styles/{id}":
		return `{"name":"Taken"}`, nil
	case "PUT /api/v1/tools/{name}/policy":
		return `{"policy":"always"}`, nil
	case "POST /api/v1/chats/{id}/shares":
		return `{"view":"live"}`, nil
	case "POST /api/v1/chats/delete": // the batch delete (P4-06), with the first person's chats
		return jsonOf(turn.DeleteManyBody{IDs: []string{f.chatA, f.chatB, f.chatD, f.incognito}}), nil
	}
	if method == "POST" || method == "PUT" || method == "PATCH" {
		return `{}`, nil
	}
	return "", nil
}

// queryFor is the query string the walk sends a route: the first person's
// ids where the route reads one, and the first person's tenant id always,
// which no route reads.
func (g *rig) queryFor(method, path string) string {
	f := g.first
	q := url.Values{"tenant": {g.one.id}}
	switch method + " " + path {
	case "GET /api/v1/audit":
		q.Set("request_id", f.requestA)
	case "GET /api/v1/chats/search":
		q.Set("q", "Lantern Accounts")
	case "GET /api/v1/chats/table": // the table of every chat (P4-06), searched with the first person's words
		q.Set("q", "Lantern Accounts")
		q.Set("ids", "true")
	case "GET /api/v1/artifacts/{artifact}/compare":
		q.Set("from", "1")
		q.Set("to", "2")
	case "GET /api/v1/chats/{id}/turns/{turn}/events", "GET /api/v1/chats/{id}/tasks/{task}/events":
		q.Set("after", "0")
	case "GET /api/v1/stream/test":
		q.Set("count", "1")
	case "POST /api/v1/chats/{id}/uploads", "POST /api/v1/projects/{id}/files":
		q.Set("name", "../../"+g.one.id+"/files/"+f.upload)
	}
	return "?" + q.Encode()
}

// api is the drill's part on the session and the API.
func (g *rig) api() error {
	two, err := g.in.Store(g.two.id)
	if err != nil {
		return err
	}
	defer two.Close()

	// 1. Every route, with the first person's ids wherever it takes one.
	for _, rt := range g.routes {
		name := rt.Method + " " + rt.Path
		if rt.Door {
			continue
		}
		if why, skip := skipped[name]; skip {
			g.report.tried(AreaAPI, name, "not tried: "+why)
			continue
		}
		body, raw := g.bodyFor(rt.Method, rt.Path)
		path := g.fill(rt.Path, false) + g.queryFor(rt.Method, rt.Path)
		var extra func(*http.Request)
		if strings.HasPrefix(rt.Path, api.Prefix+"google") {
			extra = asPage // as the page sends it, so the route reads the address Google returns to
		}
		g.try(two, call{method: rt.Method, path: path, token: g.two.token, body: body, raw: raw, extra: extra}, "the first person's ids")
	}

	// 2. The second person's own chat or project, with the first person's
	// ids for everything under it.
	for _, rt := range g.routes {
		if rt.Door || strings.Count(rt.Path, "{") < 2 || !strings.HasPrefix(rt.Path, api.Prefix+"chats/{id}") && !strings.HasPrefix(rt.Path, api.Prefix+"projects/{id}") {
			continue
		}
		body, raw := g.bodyFor(rt.Method, rt.Path)
		path := g.fill(rt.Path, true) + g.queryFor(rt.Method, rt.Path)
		g.try(two, call{method: rt.Method, path: path, token: g.two.token, body: body, raw: raw}, "the second person's own chat or project, the first person's ids under it")
	}

	// 3. The first person's ids in the bodies of the second person's own
	// chat's routes.
	f, x := g.first, api.Prefix+"chats/"+g.second.chat
	for _, b := range []turn.SendBody{
		{Text: "drill:noop", EditOf: f.messageA},
		{Text: "drill:noop", RetryOf: f.messageA + 1},
		{Blocks: []seam.Block{text("drill:noop"), {Type: seam.BlockFile, MediaType: "text/plain", Name: "ledger.txt", Ref: f.upload}}},
		{Blocks: []seam.Block{text("drill:noop"), {Type: seam.BlockFile, MediaType: "text/plain", Name: "ledger.txt", Ref: "../../" + g.one.id + "/files/" + f.upload}}},
		{Blocks: []seam.Block{text("drill:noop"), {Type: seam.BlockFile, MediaType: "text/markdown", Name: "harbour.md", Ref: f.projectFile}}},
		{Text: "drill:noop", Model: modelOne.Name},
	} {
		g.try(two, call{method: "POST", path: x + "/turns", token: g.two.token, body: jsonOf(b)}, "the first person's message, upload, file or model in a turn of the second person's own chat")
	}
	g.try(two, call{method: "POST", path: x + "/select", token: g.two.token, body: jsonOf(turn.SelectBody{MessageID: f.messageA})}, "the first person's message chosen in the second person's own chat")
	g.try(two, call{method: "PUT", path: x + "/style", token: g.two.token, body: jsonOf(turn.ChatStyleBody{Style: f.style})}, "the first person's style chosen for the second person's own chat")

	// 4. Path tricks: climbing out of a folder, encoded slashes, the first
	// person's tenant id and file hashes in the second person's file routes.
	esc := func(s string) string { return strings.ReplaceAll(url.PathEscape(s), ".", "%2E") }
	up := "../../" + g.one.id + "/files/" + f.upload
	for _, p := range []string{
		api.Prefix + "chats/" + esc("../../"+g.one.id+"/keystone.db"),
		api.Prefix + "chats/" + url.PathEscape("..\\..\\"+g.one.id+"\\secrets.bin"),
		x + "/uploads/" + url.PathEscape(up) + "/content",
		x + "/uploads/" + esc(up) + "/content",
		x + "/uploads/" + f.upload + "/content",
		x + "/uploads/" + strings.ToUpper(f.upload) + "/content",
		api.Prefix + "projects/" + g.second.project + "/files/" + f.projectFile,
		api.Prefix + "projects/" + g.second.project + "/files/" + url.PathEscape("../../"+g.one.id+"/files/"+f.projectFile),
		api.Prefix + "artifacts/" + url.PathEscape("../"+g.one.id+"/artifacts/"+f.chatB+"/"+f.artifact) + "/versions/1/download",
		api.Prefix + "artifacts/" + f.artifact + "/versions/1/sandbox?theme=dark",
		api.Prefix + "artifacts/" + g.second.artifact + "/versions/" + url.PathEscape("../../"+f.chatB+"/"+f.artifact+"/1"),
		api.Prefix + "chats/../../tenants/" + g.one.id + "/keystone.db",
		api.Prefix + "chats/%2e%2e/%2e%2e/" + g.one.id,
		api.Prefix + "chats/" + g.one.id,
		api.Prefix + "chats/" + g.one.id + "/uploads/" + f.upload + "/content",
	} {
		g.try(two, call{method: "GET", path: p, token: g.two.token}, "a path trick")
	}
	// Each file's bytes are its own, so no answer is an earlier file's
	// record, whose name the second person typed.
	for i, name := range []string{"../../" + g.one.id + "/files/" + f.upload, "..\\..\\" + g.one.id + "\\secrets.bin", "/" + g.one.id + "/keystone.db"} {
		g.try(two, call{method: "POST", path: x + "/uploads?name=" + url.QueryEscape(name+".txt"), token: g.two.token, raw: []byte(fmt.Sprintf("A file named to climb out, %d.\n", i))}, "an upload named to climb into the first person's folder")
		g.try(two, call{method: "POST", path: api.Prefix + "projects/" + g.second.project + "/files?name=" + url.QueryEscape(name+".md"), token: g.two.token, raw: []byte(fmt.Sprintf("# A file named to climb out, %d\n", i))}, "a project file named to climb into the first person's folder")
	}

	// The share links' doors (P10-04), which take no session: a link of the
	// second person's own chat, and tokens forged from it and from the first
	// person's tenant id, asked for the first person's chat and upload.
	var link shareapi.CreateReply
	if err := g.as(g.two, "POST", "chats/"+g.second.chat+"/shares", `{"view":"live"}`, http.StatusCreated, &link); err != nil {
		return err
	}
	g.second.shareToken = link.Token
	reAddressed := strings.Replace(link.Token, g.two.id, g.one.id, 1)
	for _, d := range []struct{ path, what string }{
		{api.Prefix + "shared/" + link.Token, "the second person's own share link"},
		{api.Prefix + "shared/" + reAddressed, "the second person's share token re-addressed to the first person"},
		{api.Prefix + "shared/ksh_" + g.one.id + "_" + strings.Repeat("0", 64), "a guessed share token for the first person"},
		{api.Prefix + "shared/" + url.PathEscape("../"+g.one.id+"/"+link.Token), "a share token with a path to the first person"},
		{api.Prefix + "shared/" + link.Token + "/images/" + f.upload, "the second person's share link asked for the first person's upload"},
		{api.Prefix + "shared/" + reAddressed + "/images/" + f.upload, "a re-addressed share token asked for the first person's upload"},
		{api.Prefix + "shared/" + link.Token + "/images/" + url.PathEscape("../../"+g.one.id+"/files/"+f.upload), "the second person's share link with a path to the first person's file"},
	} {
		for _, cookie := range []string{"", g.two.token} {
			before, err := audit.List(context.Background(), two, audit.Filter{})
			if err != nil {
				return err
			}
			rec := g.do(call{method: "GET", path: d.path, token: cookie})
			after, err := audit.List(context.Background(), two, audit.Filter{})
			if err != nil {
				return err
			}
			logged := false
			for _, e := range after[len(before):] {
				logged = logged || e.Actor == "share:"+link.Link.ID
				if strings.Contains(e.Action+e.Sent+e.Received, link.Token) {
					g.report.reached("%s: a share door left the token in the second person's audit log", AreaAudit)
				}
			}
			if strings.HasPrefix(d.path, api.Prefix+"shared/"+link.Token) && !logged {
				g.report.reached("%s: GET %s, with the second person's own share token, is not in their audit log", AreaAudit, g.idText(d.path))
			}
			tried := "GET " + g.idText(d.path)
			result := g.judge(AreaAPI, tried, rec.Body.String()+fmt.Sprint(rec.Header()), d.path, true)
			for _, c := range rec.Result().Cookies() {
				if c.Name == api.CookieName {
					g.report.reached("%s: %s set a session cookie", AreaAPI, tried)
				}
			}
			own := strings.HasPrefix(d.path, api.Prefix+"shared/"+link.Token) && !strings.Contains(d.path, "/images/")
			switch {
			case own && rec.Code == http.StatusOK:
				result = "answered " + short(rec) + " with the second person's own chat; " + result + "; in the second person's log as the link's view"
			case rec.Code >= 400 && rec.Code < 500:
				result = "refused, " + short(rec) + "; " + result
				if strings.HasPrefix(d.path, api.Prefix+"shared/"+link.Token) {
					result += "; in the second person's log, under the link"
				} else {
					result += "; a token that finds no link is in no person's log (the server's log has it)"
				}
			default:
				g.report.reached("%s: %s answered %d", AreaAPI, tried, rec.Code)
				result = "answered " + short(rec) + "; " + result
			}
			with := map[bool]string{true: ", with the second person's session cookie", false: ", with no session"}[cookie != ""]
			g.report.tried(AreaAPI, tried+" ("+d.what+with+")", result)
		}
	}

	// 5. Forged and guessed ids.
	var b [8]byte
	rand.Read(b[:])
	for _, id := range []string{
		strings.ToUpper(f.chatA), f.chatA + " ", f.chatA + "\x00", f.chatA + "/../" + f.chatA, "x' OR '1'='1", hex.EncodeToString(b[:]), "%", "*",
	} {
		g.try(two, call{method: "GET", path: api.Prefix + "chats/" + url.PathEscape(id), token: g.two.token}, "a forged or guessed chat id")
	}
	g.try(two, call{method: "GET", path: api.Prefix + "chats/search?q=" + url.QueryEscape(knownWords), token: g.two.token}, "a search for the first person's words")
	g.try(two, call{method: "GET", path: api.Prefix + "chats/search?q=" + url.QueryEscape("lantern* OR blue"), token: g.two.token}, "a search with the index's own operators")
	g.try(two, call{method: "GET", path: api.Prefix + "audit?kind=model&request_id=" + f.requestA, token: g.two.token}, "the first person's request id in the audit route")
	g.try(two, call{method: "POST", path: api.Prefix + "chats/" + f.incognito + "/close", token: g.two.token}, "closing the first person's incognito chat")

	// 6. Cookie and session swaps.
	forged := strings.Replace(g.two.token, g.two.id, g.one.id, 1)
	target := api.Prefix + "chats/" + f.chatA
	for _, c := range []struct {
		what  string
		extra func(*http.Request)
	}{
		{"the second person's token re-addressed to the first person", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: api.CookieName, Value: forged}) }},
		{"two session cookies, the second person's first", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: api.CookieName, Value: g.two.token})
			r.AddCookie(&http.Cookie{Name: api.CookieName, Value: forged})
		}},
		{"two session cookies, the re-addressed one first", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: api.CookieName, Value: forged})
			r.AddCookie(&http.Cookie{Name: api.CookieName, Value: g.two.token})
		}},
		{"the second person's token in an Authorization header", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+g.two.token) }},
		{"the second person's token under another cookie name", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "Keystone_Session", Value: g.two.token}) }},
		{"the second person's session with the first person named in a header", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: api.CookieName, Value: g.two.token})
			r.Header.Set("X-Tenant", g.one.id)
			r.Header.Set("X-Request-Id", f.requestA)
		}},
	} {
		g.try(two, call{method: "GET", path: target, extra: c.extra}, "a cookie or session swap: "+c.what)
	}
	var me api.SessionReply
	rec := g.do(call{method: "GET", path: api.Prefix + "session?tenant=" + g.one.id, token: g.two.token, extra: func(r *http.Request) { r.Header.Set("X-Tenant", g.one.id) }})
	if jsonInto(rec.Body.Bytes(), &me) != nil || me.Person.ID != g.two.id {
		g.report.reached("with the first person named in a header and the query, the session route says the caller is %q", me.Person.ID)
	}
	g.report.tried(AreaAPI, "GET /api/v1/session with the second person's session and the first person named in a header and the query", "the second person, "+short(rec))

	// 7. A model whose certificate authority names a file in the first
	// person's folder (decision 0490).
	var folder string
	if err := g.with(g.one.id, func(s *tenant.Store) error { folder = s.Files().Name(); return nil }); err != nil {
		return err
	}
	for i, path := range []string{folder + "/" + f.upload, "data/tenants/" + g.one.id + "/files/" + f.upload, "../" + g.one.id + "/secrets.bin", `\\files.example\share\ca.pem`} {
		m := modelTwo
		m.Name, m.Endpoint, m.CACert = fmt.Sprintf("probe-gateway-%d", i), "https://gateway.home.arpa/v1", path
		rec := g.try(two, call{method: "POST", path: api.Prefix + "models", token: g.two.token, body: jsonOf(m)}, "a model whose ca_cert names a file of the first person's, or a share")
		if rec.Code < 400 {
			g.report.reached("a model whose ca_cert is the path %s was taken through the API, so Keystone would read that file when the model is opened", g.idText(path))
		}
	}
	return nil
}

// try makes one call as the second person and judges it: refused, or
// answered with nothing of the first person's; and in the second person's
// audit log under its request id, with the method and path tried.
func (g *rig) try(two *tenant.Store, c call, what string) *httptest.ResponseRecorder {
	rec := g.do(c)
	tried := c.method + " " + g.idText(c.path)
	result := g.judge(AreaAPI, tried, rec.Body.String()+fmt.Sprint(rec.Header()), c.path+c.body+string(c.raw), c.path != "" && !strings.HasPrefix(c.path, api.Prefix+"audit"))
	switch {
	case rec.Code >= 500:
		g.report.note("%s (%s) failed with %s where it could have refused", tried, what, short(rec))
		result = "failed, " + short(rec) + "; " + result
	case rec.Code >= 400:
		result = "refused, " + short(rec)
	case rec.Code >= 300:
		result = "redirected, " + short(rec) + ", nothing served"
	default:
		result = "answered " + short(rec) + "; " + result
	}
	if rec.Code != http.StatusUnauthorized {
		result += g.auditedAPI(two, rec, c, tried)
	} else {
		result += "; no session, so in no person's log (the server's log has it)"
	}
	g.report.tried(AreaAPI, tried+" ("+what+")", result)
	return rec
}

// judge looks for the first person's marks, and their long ids that the
// request did not carry, in what came back, and reports any as reached.
func (g *rig) judge(area, tried, answer, request string, ids bool) string {
	var found []string
	for _, m := range g.allMarks() {
		if strings.Contains(answer, m) || strings.Contains(strings.ToLower(answer), strings.ToLower(m)) {
			found = append(found, "the mark of "+g.markName(m))
		}
	}
	if ids {
		for _, id := range g.longIDs() {
			if strings.Contains(answer, id) && !strings.Contains(request, id) && !strings.Contains(request, url.PathEscape(id)) {
				found = append(found, g.idText(id))
			}
		}
	}
	if len(found) > 0 {
		g.report.reached("%s: %s gave the second person %s", area, tried, strings.Join(found, ", "))
		return "REACHED " + strings.Join(found, ", ")
	}
	return "nothing of the first person's"
}

// markName says which of the first person's things a mark is in.
func (g *rig) markName(m string) string {
	for what, have := range g.marks {
		if have == m {
			return what
		}
	}
	return "an unknown thing"
}

// auditedAPI checks the second person's log for one call: an entry before
// it ran and one for its end, under its request id, with its method and
// path, and no token or key.
func (g *rig) auditedAPI(two *tenant.Store, rec *httptest.ResponseRecorder, c call, tried string) string {
	id := rec.Header().Get("X-Request-Id")
	if id == "" {
		g.report.reached("%s came back with no request id, so it cannot be found in an audit log", tried)
		return "; NOT AUDITED"
	}
	entries, err := audit.List(context.Background(), two, audit.Filter{RequestID: id})
	if err != nil {
		g.report.reached("%s: the second person's audit log could not be read: %v", tried, err)
		return "; NOT AUDITED"
	}
	path := c.path
	if i := strings.Index(path, "?"); i >= 0 {
		path = path[:i]
	}
	if len(entries) < 2 || entries[0].Outcome != audit.OutcomeStarted || !strings.HasPrefix(entries[0].Action, c.method+" "+api.Prefix) {
		g.report.reached("%s, answered %d, is not in the second person's audit log as a call before it ran and its end (%d entries)", tried, rec.Code, len(entries))
		return "; NOT AUDITED"
	}
	end := entries[len(entries)-1]
	if end.Status != rec.Code {
		g.report.reached("%s answered %d and its end is recorded as %d", tried, rec.Code, end.Status)
	}
	for _, e := range entries {
		for _, secret := range []string{g.two.token, g.one.token} {
			if strings.Contains(e.Action+e.Sent+e.Received, secret) {
				g.report.reached("%s left a session token in the second person's audit log", tried)
			}
		}
	}
	if unescaped, err := url.PathUnescape(path); err == nil && entries[0].Action == c.method+" "+unescaped || entries[0].Action == c.method+" "+path {
		return "; in the second person's log as " + string(end.Outcome) + ", with the path tried"
	}
	return "; in the second person's log as " + string(end.Outcome) + " " + g.idText(entries[0].Action)
}
