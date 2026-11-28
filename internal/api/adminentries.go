package api

import (
	"net/http"
	"strconv"

	"github.com/jonathanmkosar/keystone/internal/audit"
)

// AdminEntriesRoute is a person's route to the entries admin accounts' calls
// left in their own audit log (P15-01, decision 0654): which admin read or
// changed what of theirs, through which route, and when. Each admin call is
// two entries with the same request id, written before and after; sent
// holds what was read or changed, and the admin account's name as it was.
// An admin cannot read these, or anything else of a person's log.
func AdminEntriesRoute(pageLimit int, log *Log) Route {
	handle := func(w http.ResponseWriter, r *http.Request, c Call) {
		q := r.URL.Query()
		after, limit := int64(0), pageLimit
		if v := q.Get("after"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				Error(w, r, http.StatusBadRequest, "bad_request", "after is a whole number, zero or more")
				return
			}
			after = n
		}
		if v := q.Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				Error(w, r, http.StatusBadRequest, "bad_request", "limit is a whole number, one or more")
				return
			}
			limit = min(n, pageLimit)
		}
		entries, err := audit.ListByAdmin(r.Context(), c.Store, after, limit)
		if err != nil {
			log.Printf("%s reading the admin entries of the audit log failed: %v", c.RequestID, err)
			Error(w, r, http.StatusInternalServerError, "failed", "the audit log could not be read")
			return
		}
		reply := AuditReply{Entries: entries}
		if reply.Entries == nil {
			reply.Entries = []audit.Entry{}
		}
		if len(entries) == limit {
			reply.NextAfter = entries[len(entries)-1].ID
		}
		WriteJSON(w, http.StatusOK, reply)
	}
	return Route{
		Method: "GET", Path: Prefix + "audit/admin", Handle: handle,
		Summary: "The entries of the signed-in person's own audit log that admin accounts' calls left: who read or changed what of theirs, through which route, and when. Oldest first, a page at a time.",
		Query: []Param{
			{"after", "Return the entries written after the entry with this id."},
			{"limit", "The most entries to return. Never more than the server.page_limit setting."},
		},
		Reply: AuditReply{},
	}
}
