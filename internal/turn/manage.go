package turn

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/imported"
	"github.com/jonathanmkosar/keystone/internal/recap"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// Managing many chats (P4-06): the table of every chat, with keyword
// search, filters and sorting, and one request that deletes many. The
// Chats screen is drawn from these two routes.
// docs/architecture/manage-chats.md is the whole of it.
//
// The table reads the marks a chat wears from the tables of the parts that
// keep them (projects, tasks, sub-agents, share links, imports, recall and
// the memory flag), in one query, and changes nothing in them. The delete
// is chat.Delete, once for each chat, with nothing new removed and nothing
// new kept.

// The settings of the table and the batch delete, in settings/chats.json.
const (
	// KeyTablePageRows is how many rows one page of the table holds.
	KeyTablePageRows = "chats.table_page_rows"
	// KeyTableSelectMax is the most ids "select all that match" is given.
	KeyTableSelectMax = "chats.table_select_max"
	// KeyDeleteBatchMax is the most chats one request deletes.
	KeyDeleteBatchMax = "chats.delete_batch_max"
)

// The bounds of those settings. A stored value outside them is not used.
const (
	tablePageRowsMost  = 500
	tableSelectMost    = 100000
	deleteBatchMost    = 1000
	manageIDMaxLength  = 64
	manageIDCharacters = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-_"
)

// The orders the table can be put in, and the marks it can be narrowed to.
const (
	SortActive   = "active"   // the time of the last message
	SortTitle    = "title"    // the title, without regard to capitals
	SortMessages = "messages" // how many messages the chat holds
	SortMatch    = "match"    // how well the chat matches the search

	OrderAsc  = "asc"
	OrderDesc = "desc"

	FilterStarred  = "starred"
	FilterImported = "imported"
	FilterProject  = "project"
	FilterTask     = "task"
	FilterSubagent = "subagent"
)

// Why a chat was left by POST chats/delete.
const (
	// SkipTurnRunning: a turn is queued or running in the chat.
	SkipTurnRunning = "turn_running"
	// SkipTaskRunning: a background task or a sub-agent that has not ended
	// works in the chat or was started from it.
	SkipTaskRunning = "task_running"
	// SkipNotFound: no chat of this person's has the id.
	SkipNotFound = "not_found"
	// SkipFailed: the delete failed. The reason is in the server's log under
	// the call's request id.
	SkipFailed = "failed"
)

var skipWhy = map[string]string{
	SkipTurnRunning: "A reply is still being written in it, or is waiting its turn. Stop it or let it end, then delete the chat.",
	SkipTaskRunning: "A background task or a sub-agent is still at work in it or for it. Stop it or let it end, then delete the chat.",
	SkipNotFound:    "It is not among your chats. It may have been deleted already.",
	SkipFailed:      "It could not be deleted. It is still here, and can be tried again.",
}

// TableProject names the project a chat is in.
type TableProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TableMatch is what the search found in one chat.
type TableMatch struct {
	// Title says the chat's title holds the words. TitleParts is the title
	// cut into parts, the matching words marked.
	Title      bool        `json:"title"`
	TitleParts []chat.Part `json:"title_parts,omitempty"`
	// Messages is how many of the chat's messages hold the words, on every
	// branch.
	Messages int `json:"messages"`
	// MessageID is the best of them and Role who wrote it. Snippet is the
	// words around the match in it, the matching words marked. POST
	// chats/{id}/select opens the chat at that message.
	MessageID int64       `json:"message_id,omitempty"`
	Role      seam.Role   `json:"role,omitempty"`
	Snippet   []chat.Part `json:"snippet,omitempty"`
}

// TableRow is one chat as the table of every chat shows it.
type TableRow struct {
	ID string `json:"id"`
	// Title is empty until the chat has one.
	Title string `json:"title"`
	// Summary is the summary of the chat's recap. Absent until it has one.
	Summary string    `json:"summary,omitempty"`
	Created time.Time `json:"created"`
	// Active is when the last message was written.
	Active time.Time `json:"active"`
	// Messages is how many messages the chat holds, on every branch, tool
	// results included.
	Messages int `json:"messages"`
	// Busy says a turn is queued or running in the chat. Working says a
	// background task or a sub-agent that has not ended works in it or was
	// started from it. A delete leaves a chat that is either.
	Busy    bool `json:"busy"`
	Working bool `json:"working"`

	// The marks.
	Starred bool `json:"starred"`
	// Imported is the mark of a chat brought in from another app.
	Imported *imported.Mark `json:"imported,omitempty"`
	// Project is the project the chat is in.
	Project *TableProject `json:"project,omitempty"`
	// Task says a background task or a scheduled run worked in this chat.
	Task bool `json:"task,omitempty"`
	// Subagent says a sub-agent worked in this chat.
	Subagent bool `json:"subagent,omitempty"`
	// Held says the chat was sent content from a sensitive space, so only a
	// local or trusted model answers in it.
	Held bool `json:"held,omitempty"`
	// Shared is how many share links to the chat are open.
	Shared int `json:"shared,omitempty"`
	// Incognito says no memory pass reads the chat.
	Incognito bool `json:"incognito,omitempty"`

	// Match is what the search found in the chat. Absent with no search.
	Match *TableMatch `json:"match,omitempty"`
}

// TableReply answers GET chats/table.
type TableReply struct {
	// Rows are one page of the chats that match, in the order asked for.
	Rows []TableRow `json:"rows"`
	// Total is how many chats match the search and the filters.
	Total int `json:"total"`
	// More says there are rows after these. Ask again with a larger offset.
	More bool `json:"more"`
	// Query, Sort, Order and Filters are what the table was made with.
	Query   string   `json:"query"`
	Sort    string   `json:"sort"`
	Order   string   `json:"order"`
	Filters []string `json:"filters"`
	// PageRows is the most rows a page holds, the chats.table_page_rows
	// setting, and DeleteMax the most chats one POST chats/delete takes,
	// chats.delete_batch_max.
	PageRows  int `json:"page_rows"`
	DeleteMax int `json:"delete_max"`
	// IDs are the ids of the chats that match, in the same order, when the
	// call asked for them with ids=true: at most chats.table_select_max.
	// IDsCut says more matched than that.
	IDs    []string `json:"ids,omitempty"`
	IDsCut bool     `json:"ids_cut,omitempty"`
}

// DeleteManyBody names the chats POST chats/delete deletes.
type DeleteManyBody struct {
	// IDs are the chats' ids, at most chats.delete_batch_max of them.
	IDs []string `json:"ids"`
}

// SkippedChat is a chat POST chats/delete left, and why.
type SkippedChat struct {
	ID string `json:"id"`
	// Title is the chat's title. Absent when there is no such chat.
	Title string `json:"title,omitempty"`
	// Reason is turn_running, task_running, not_found or failed.
	Reason string `json:"reason"`
	// Why says the same in words, for a person.
	Why string `json:"why"`
}

// DeleteManyReply answers POST chats/delete.
type DeleteManyReply struct {
	// Deleted are the ids of the chats that are gone, with their messages,
	// turns, files, artifacts and share links.
	Deleted []string `json:"deleted"`
	// Skipped are the chats that were left, each with its reason.
	Skipped []SkippedChat `json:"skipped"`
}

// ManageRoutes returns the routes of the table of every chat and the batch
// delete (P4-06), served by r.
func ManageRoutes(r *Runner) []api.Route {
	const chats = api.Prefix + "chats"
	return []api.Route{
		{
			Method: "GET", Path: chats + "/table", Handle: r.table,
			Summary: "Every chat of the person's as a row of a table: its title, its recap's summary, its last activity, how many messages it holds and the marks it wears. It can be searched by the words of titles and messages, narrowed to marks, sorted and paged.",
			Query: []api.Param{
				{Name: "q", Description: "The words to look for. Every word must be in the chat's title or in one of its messages, on any branch; a word also matches as the start of a longer one. Each row then has a match, with the matching words marked."},
				{Name: "sort", Description: "active (the last message, the default), title, messages, or match (the best match first, the default while searching)."},
				{Name: "order", Description: "asc or desc. The default is desc for active and messages, asc for title and match."},
				{Name: "filter", Description: "Marks a chat must wear, separated by commas: starred, imported, project, task, subagent. A chat must wear every one named."},
				{Name: "limit", Description: "The most rows to return. The chats.table_page_rows setting is the most and the default."},
				{Name: "offset", Description: "How many rows to skip from the top."},
				{Name: "ids", Description: "true also returns the ids of every chat that matches, at most chats.table_select_max, for selecting them all."},
			},
			Reply: TableReply{},
		},
		{
			Method: "POST", Path: chats + "/delete", Handle: r.removeMany,
			Summary: "Delete many chats in one call, each as DELETE chats/{id} does, with its messages, turns, files, artifacts and share links, and each in the audit log. Nothing is stopped: a chat with a turn queued or running, or with a background task or a sub-agent still at work in it or for it, is left and named in skipped with its reason, and so is an id that is not one of the person's chats. More ids than chats.delete_batch_max is refused. There is no undo.",
			Body:    DeleteManyBody{}, Reply: DeleteManyReply{},
		},
	}
}

// manageLimits are the three settings, as they stand for one person.
type manageLimits struct {
	pageRows  int
	selectMax int
	deleteMax int
}

// manageLimitsOf reads the settings for a tenant. A stored value that does
// not read as a whole number within its bounds is not used: the shipped
// default is, with a line in the log.
func (r *Runner) manageLimitsOf(ctx context.Context, s *tenant.Store) (manageLimits, error) {
	if err := r.setup(); err != nil {
		return manageLimits{}, err
	}
	read := func(key string, most int) (int, error) {
		n, err := strconv.Atoi(r.setting(ctx, s, key))
		if err == nil && n >= 1 && n <= most {
			return n, nil
		}
		shipped, ok := settings.Lookup(r.reg, key)
		if n, err = strconv.Atoi(shipped.Default); !ok || err != nil || n < 1 || n > most {
			return 0, errors.New("the " + key + " setting does not read as a whole number from 1 to " + strconv.Itoa(most))
		}
		r.logf("turns: tenant %s: the setting %s does not read as a whole number from 1 to %d, so %d is used", s.TenantID(), key, most, n)
		return n, nil
	}
	var lim manageLimits
	var err error
	if lim.pageRows, err = read(KeyTablePageRows, tablePageRowsMost); err != nil {
		return manageLimits{}, err
	}
	if lim.selectMax, err = read(KeyTableSelectMax, tableSelectMost); err != nil {
		return manageLimits{}, err
	}
	if lim.deleteMax, err = read(KeyDeleteBatchMax, deleteBatchMost); err != nil {
		return manageLimits{}, err
	}
	return lim, nil
}

// tableAsk is what one call asks the table for.
type tableAsk struct {
	query   string
	sort    string
	order   string
	filters []string
	limit   int // zero means a whole page
	offset  int
	ids     bool
}

// readTableAsk reads a call's query string. The second value says why it
// was refused, and is empty when it was not.
func readTableAsk(q url.Values) (tableAsk, string) {
	ask := tableAsk{query: strings.TrimSpace(q.Get("q")), sort: q.Get("sort"), order: q.Get("order"), ids: q.Get("ids") == "true"}
	searching := ask.query != ""
	switch ask.sort {
	case "":
		ask.sort = SortActive
		if searching {
			ask.sort = SortMatch
		}
	case SortActive, SortTitle, SortMessages:
	case SortMatch:
		if !searching {
			ask.sort = SortActive // there is nothing to match
			ask.order = ""
		}
	default:
		return tableAsk{}, "sort is active, title, messages or match"
	}
	switch ask.order {
	case "":
		ask.order = OrderDesc
		if ask.sort == SortTitle || ask.sort == SortMatch {
			ask.order = OrderAsc
		}
	case OrderAsc, OrderDesc:
	default:
		return tableAsk{}, "order is asc or desc"
	}
	ask.filters = []string{}
	for _, given := range q["filter"] {
		for _, f := range strings.Split(given, ",") {
			f = strings.TrimSpace(f)
			switch f {
			case "":
			case FilterStarred, FilterImported, FilterProject, FilterTask, FilterSubagent:
				if !slices.Contains(ask.filters, f) {
					ask.filters = append(ask.filters, f)
				}
			default:
				return tableAsk{}, "filter is any of starred, imported, project, task and subagent, separated by commas"
			}
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return tableAsk{}, "limit is a whole number, 1 or more"
		}
		ask.limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return tableAsk{}, "offset is a whole number, 0 or more"
		}
		ask.offset = n
	}
	return ask, ""
}

// tableLine is a row before its page is known, with what it is sorted by.
type tableLine struct {
	TableRow
	imported bool
	rank     int // its place in the search's order, best first
}

// tableLines reads every chat of the tenant with its marks, the one with
// the newest message first. It reads the tables of the parts that keep the
// marks and changes nothing in them: project_chats and projects
// (internal/projects), jobs (internal/jobs), subagents (internal/subagents),
// share_links (internal/share), imported_chats (internal/imported),
// turn_recall (internal/knowledge/recall) and memory_chats
// (internal/knowledge/memory).
func tableLines(ctx context.Context, s *tenant.Store, now time.Time) ([]tableLine, error) {
	rows, err := s.DB().QueryContext(ctx, `
		SELECT c.id, c.title, c.starred, c.created_at, c.active_at,
			(SELECT count(*) FROM messages m WHERE m.chat_id = c.id),
			EXISTS (SELECT 1 FROM turns t WHERE t.status IN ('queued', 'running') AND t.chat_id = c.id),
			EXISTS (SELECT 1 FROM jobs j WHERE j.status IN ('queued', 'running') AND (j.chat_id = c.id OR j.work_chat_id = c.id))
				OR EXISTS (SELECT 1 FROM subagents a WHERE a.status IN ('queued', 'running') AND (a.chat_id = c.id OR a.work_chat_id = c.id)),
			EXISTS (SELECT 1 FROM imported_chats i WHERE i.chat_id = c.id),
			COALESCE(p.id, ''), COALESCE(p.name, ''),
			EXISTS (SELECT 1 FROM jobs j WHERE j.work_chat_id = c.id),
			EXISTS (SELECT 1 FROM subagents a WHERE a.work_chat_id = c.id),
			EXISTS (SELECT 1 FROM turn_recall r WHERE r.chat_id = c.id AND r.sensitive = 1),
			(SELECT count(*) FROM share_links l WHERE l.chat_id = c.id AND l.revoked_at = '' AND (l.expires_at = '' OR l.expires_at > ?)),
			EXISTS (SELECT 1 FROM memory_chats mc WHERE mc.chat_id = c.id AND mc.incognito = 1)
		FROM chats c
		LEFT JOIN project_chats pc ON pc.chat_id = c.id
		LEFT JOIN projects p ON p.id = pc.project_id
		ORDER BY c.active_at DESC, c.seq DESC`, now.UTC().Format(timeLayout))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tableLine
	for rows.Next() {
		var l tableLine
		var created, active, projectID, projectName string
		if err := rows.Scan(&l.ID, &l.Title, &l.Starred, &created, &active, &l.Messages, &l.Busy, &l.Working,
			&l.imported, &projectID, &projectName, &l.Task, &l.Subagent, &l.Held, &l.Shared, &l.Incognito); err != nil {
			return nil, err
		}
		if l.Created, err = time.Parse(timeLayout, created); err != nil {
			return nil, err
		}
		if l.Active, err = time.Parse(timeLayout, active); err != nil {
			return nil, err
		}
		if projectID != "" {
			l.Project = &TableProject{ID: projectID, Name: projectName}
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// wears reports whether a row wears every mark named.
func (l tableLine) wears(filters []string) bool {
	for _, f := range filters {
		var ok bool
		switch f {
		case FilterStarred:
			ok = l.Starred
		case FilterImported:
			ok = l.imported
		case FilterProject:
			ok = l.Project != nil
		case FilterTask:
			ok = l.Task
		case FilterSubagent:
			ok = l.Subagent
		}
		if !ok {
			return false
		}
	}
	return true
}

// narrowTable keeps the lines that wear every filter and, when found is not
// nil, that the search found, each with its place in the search's order. It
// keeps the order the lines came in.
func narrowTable(lines []tableLine, filters []string, found []chat.Found) []tableLine {
	var rank map[string]int
	if found != nil {
		rank = make(map[string]int, len(found))
		for i, f := range found {
			rank[f.ChatID] = i
		}
	}
	out := lines[:0:0]
	for _, l := range lines {
		if !l.wears(filters) {
			continue
		}
		if rank != nil {
			at, ok := rank[l.ID]
			if !ok {
				continue
			}
			l.rank = at
		}
		out = append(out, l)
	}
	return out
}

// sortTable puts lines, which come with the newest message first, in the
// order asked for. Lines that are equal by it stay newest first. A chat
// with no title comes after every chat with one, whichever way titles run.
func sortTable(lines []tableLine, by, order string) {
	desc := order == OrderDesc
	var less func(a, b tableLine) int
	switch by {
	case SortTitle:
		less = func(a, b tableLine) int {
			at, bt := strings.ToLower(a.Title), strings.ToLower(b.Title)
			if (at == "") != (bt == "") {
				if (at == "") != desc { // untitled last, also when the order is turned
					return 1
				}
				return -1
			}
			return strings.Compare(at, bt)
		}
	case SortMessages:
		less = func(a, b tableLine) int { return a.Messages - b.Messages }
	case SortMatch:
		less = func(a, b tableLine) int { return a.rank - b.rank }
	default: // SortActive: the lines come newest first
		if !desc {
			slices.Reverse(lines)
		}
		return
	}
	sort.SliceStable(lines, func(i, j int) bool {
		c := less(lines[i], lines[j])
		if desc {
			c = -c
		}
		return c < 0
	})
}

func (r *Runner) table(w http.ResponseWriter, req *http.Request, c api.Call) {
	ask, why := readTableAsk(req.URL.Query())
	if why != "" {
		refused(w, req, why)
		return
	}
	ctx := req.Context()
	lim, err := r.manageLimitsOf(ctx, c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	limit := lim.pageRows
	if ask.limit > 0 {
		limit = min(ask.limit, lim.pageRows)
	}
	lines, err := tableLines(ctx, c.Store, time.Now())
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	var found []chat.Found
	if ask.query != "" {
		if found, err = chat.SearchByChat(c.Store, ask.query); err != nil {
			r.answer(w, req, c, err)
			return
		}
		if found == nil {
			found = []chat.Found{} // words with no letter or digit match nothing
		}
	}
	lines = narrowTable(lines, ask.filters, found)
	sortTable(lines, ask.sort, ask.order)

	reply := TableReply{
		Rows: []TableRow{}, Total: len(lines), Query: ask.query, Sort: ask.sort, Order: ask.order, Filters: ask.filters,
		PageRows: lim.pageRows, DeleteMax: lim.deleteMax,
	}
	if ask.ids {
		reply.IDs = make([]string, 0, min(len(lines), lim.selectMax))
		for _, l := range lines[:min(len(lines), lim.selectMax)] {
			reply.IDs = append(reply.IDs, l.ID)
		}
		reply.IDsCut = len(lines) > lim.selectMax
	}
	page := lines[min(ask.offset, len(lines)):]
	page = page[:min(limit, len(page))]
	reply.More = ask.offset+len(page) < len(lines)
	r.fillTable(ctx, c, ask.query, found, page)
	for _, l := range page {
		reply.Rows = append(reply.Rows, l.TableRow)
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

// fillTable gives one page of rows what is read only for the rows shown:
// each chat's summary, its imported mark, and what the search found in it.
// A summary or a mark that cannot be read is left out and logged; the table
// is not failed for it.
func (r *Runner) fillTable(ctx context.Context, c api.Call, query string, found []chat.Found, page []tableLine) {
	if len(page) == 0 {
		return
	}
	s := c.Store
	ids := make([]string, len(page))
	shown := map[string]bool{}
	for i, l := range page {
		ids[i] = l.ID
		shown[l.ID] = true
	}
	sums, err := recap.Summaries(ctx, s, ids)
	if err != nil {
		r.logf("turns: %s reading the chats' summaries of tenant %s, so the table has none: %v", c.RequestID, s.TenantID(), err)
	}
	marks, err := imported.Marks(ctx, s, ids)
	if err != nil {
		r.logf("turns: %s reading the imported chats' marks of tenant %s, so the table has none: %v", c.RequestID, s.TenantID(), err)
	}
	var excerpts map[string]chat.Excerpt
	byChat := map[string]chat.Found{}
	if query != "" {
		var here []chat.Found
		for _, f := range found {
			if shown[f.ChatID] {
				here = append(here, f)
				byChat[f.ChatID] = f
			}
		}
		if excerpts, err = chat.Excerpts(s, r.o.Config.Limits, query, here); err != nil {
			r.logf("turns: %s reading what the search found in the chats of tenant %s, so the table shows no snippet: %v", c.RequestID, s.TenantID(), err)
		}
	}
	for i := range page {
		l := &page[i]
		l.Summary = sums[l.ID]
		if m, ok := marks[l.ID]; ok {
			l.Imported = &m
		}
		if f, ok := byChat[l.ID]; ok {
			e := excerpts[l.ID]
			l.Match = &TableMatch{Title: f.Title, TitleParts: e.Title, Messages: f.Messages, MessageID: f.MessageID, Role: e.Role, Snippet: e.Snippet}
		}
	}
}

// plainID reports whether id could be a chat's: a short run of letters,
// digits, hyphens and underscores. Anything else is refused before it is
// looked for or written in the audit log.
func plainID(id string) bool {
	if id == "" || len(id) > manageIDMaxLength {
		return false
	}
	for _, ch := range id {
		if !strings.ContainsRune(manageIDCharacters, ch) {
			return false
		}
	}
	return true
}

func (r *Runner) removeMany(w http.ResponseWriter, req *http.Request, c api.Call) {
	var body DeleteManyBody
	if !api.ReadJSON(w, req, &body) {
		return
	}
	ctx := req.Context()
	lim, err := r.manageLimitsOf(ctx, c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	var ids []string
	for _, id := range body.IDs {
		if !plainID(id) {
			refused(w, req, "ids are the ids of chats")
			return
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	switch {
	case len(ids) == 0:
		refused(w, req, "give ids, the chats to delete")
		return
	case len(ids) > lim.deleteMax:
		api.Error(w, req, http.StatusBadRequest, "too_many", "one call deletes at most "+strconv.Itoa(lim.deleteMax)+" chats; send the rest in another")
		return
	}
	reply := DeleteManyReply{Deleted: []string{}, Skipped: []SkippedChat{}}
	for _, id := range ids {
		title, reason := r.removeIdle(ctx, c, id)
		if reason == "" {
			reply.Deleted = append(reply.Deleted, id)
			continue
		}
		reply.Skipped = append(reply.Skipped, SkippedChat{ID: id, Title: title, Reason: reason, Why: skipWhy[reason]})
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

// removeIdle deletes one chat of a batch unless something is at work in it,
// and returns its title and why it was left, which is empty when it is
// gone. Nothing is stopped. The chat's queue is held while it looks and
// deletes, so no turn can start in between: a turn sent meanwhile either
// goes with the chat or finds it gone.
//
// Each chat is in the audit log under the call's request id, as its own
// DELETE chats/{id} would be: an entry before it is deleted and one for how
// that ended, or one entry for a chat that was left. A delete that cannot
// be recorded first is not made.
func (r *Runner) removeIdle(ctx context.Context, c api.Call, id string) (title, reason string) {
	s := c.Store
	record := func(outcome audit.Outcome, status int, why string) error {
		_, err := audit.Append(context.WithoutCancel(ctx), s, audit.Entry{
			Kind: audit.KindAPI, RequestID: c.RequestID, Actor: api.ActorPerson,
			Action: "DELETE " + api.Prefix + "chats/" + id, Outcome: outcome, Status: status, Received: why,
		})
		if err != nil {
			r.logf("%s deleting chat %s of a batch: the audit log could not be written: %v", c.RequestID, id, err)
		}
		return err
	}
	left := func(reason string, status int) (string, string) {
		outcome := audit.OutcomeRefused
		if reason == SkipFailed {
			outcome = audit.OutcomeError
		}
		record(outcome, status, reason)
		return title, reason
	}
	got, err := chat.Get(s, id)
	if errors.Is(err, chat.ErrNotFound) {
		return left(SkipNotFound, http.StatusNotFound)
	}
	if err != nil {
		r.logf("%s deleting chat %s of a batch: %v", c.RequestID, id, err)
		return left(SkipFailed, http.StatusInternalServerError)
	}
	title = got.Title

	// Hold the chat's queue, as the goroutine that works its turns does. If
	// one is working it, a turn is running.
	key := s.TenantID() + "/" + id
	r.mu.Lock()
	switch {
	case r.closing:
		r.mu.Unlock()
		return left(SkipFailed, http.StatusServiceUnavailable)
	case r.working[key]:
		r.mu.Unlock()
		return left(SkipTurnRunning, http.StatusConflict)
	}
	r.working[key] = true
	r.wg.Add(1)
	r.mu.Unlock()
	gone := false
	defer func() {
		r.mu.Lock()
		delete(r.working, key)
		r.mu.Unlock()
		r.wg.Done()
		if !gone {
			r.kick(s.TenantID(), id) // a turn sent while the queue was held
		}
	}()

	open, err := openTurns(ctx, s, id)
	if err != nil {
		r.logf("%s deleting chat %s of a batch: %v", c.RequestID, id, err)
		return left(SkipFailed, http.StatusInternalServerError)
	}
	if len(open) > 0 {
		return left(SkipTurnRunning, http.StatusConflict)
	}
	working, err := atWork(ctx, s, id)
	if err != nil {
		r.logf("%s deleting chat %s of a batch: %v", c.RequestID, id, err)
		return left(SkipFailed, http.StatusInternalServerError)
	}
	if working {
		return left(SkipTaskRunning, http.StatusConflict)
	}
	if record(audit.OutcomeStarted, 0, "") != nil {
		return title, SkipFailed
	}
	if err := chat.Delete(s, id); err != nil {
		if errors.Is(err, chat.ErrNotFound) {
			return left(SkipNotFound, http.StatusNotFound)
		}
		r.logf("%s deleting chat %s of a batch: %v", c.RequestID, id, err)
		return left(SkipFailed, http.StatusInternalServerError)
	}
	gone = true
	record(audit.OutcomeOK, http.StatusOK, "")
	return title, ""
}

// atWork reports whether a background task or a sub-agent that has not
// ended works in the chat or was started from it. It reads the jobs table
// (internal/jobs) and the subagents table (internal/subagents) and changes
// nothing in them.
func atWork(ctx context.Context, s *tenant.Store, chatID string) (bool, error) {
	if busy, err := taskWorking(ctx, s, chatID); err != nil || busy {
		return busy, err
	}
	var n int
	err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM subagents WHERE status IN ('queued', 'running') AND (chat_id = ?1 OR work_chat_id = ?1)", chatID).Scan(&n)
	return n > 0, err
}
