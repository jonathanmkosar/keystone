package usagereport

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/settings"
)

// Options are what the route runs with. The caller reads them from the
// settings.
type Options struct {
	// PageLimit is the most rows a breakdown may be asked to show, which
	// is server.page_limit.
	PageLimit int
	// Log is the server's own log. Nil means none.
	Log *api.Log
	// Now is the clock. Nil means the machine's.
	Now func() time.Time
}

// Routes returns the usage report's route. It acts for the signed-in
// person, on the store the shell hands it, so a person sees their own
// tenant's usage and no one else's.
func Routes(o Options) []api.Route {
	return []api.Route{{
		Method: "GET", Path: api.Prefix + "usage", Handle: o.report,
		Summary: "Your usage for a period: what your model calls cost and their tokens, in total and by model, by chat and by kind of work, " +
			"each split into local, hosted and trusted models, with this month against the monthly cap and the memory pass's reserve. " +
			"Summed from the usage records, so the lines of each breakdown add up to the total. With no query, the period is your usage.report_period.",
		Query: []api.Param{
			{Name: "period", Description: "this_month, last_month, last_7_days or last_30_days."},
			{Name: "month", Description: "A calendar month in UTC, such as 2026-11."},
			{Name: "from", Description: "The first day of a range in UTC, such as 2026-11-01, with to."},
			{Name: "to", Description: "The last day of a range, included, with from. A range covers at most usage.report_max_days days."},
			{Name: "rows", Description: "The most lines each breakdown shows before the rest are added up in one more line, up to server.page_limit. Without it, your usage.report_rows."},
		},
		Reply: Report{},
	}}
}

func (o Options) report(w http.ResponseWriter, r *http.Request, c api.Call) {
	q := r.URL.Query()
	a := Ask{Period: q.Get("period"), Month: q.Get("month"), From: q.Get("from"), To: q.Get("to")}
	if v := q.Get("rows"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > o.PageLimit {
			api.Error(w, r, http.StatusBadRequest, "bad_request", fmt.Sprintf("rows is a whole number from 1 to %d", o.PageLimit))
			return
		}
		a.Rows = n
	}
	reg, err := settings.Registry()
	if err != nil {
		o.fail(w, r, c, err)
		return
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	rep, err := Read(r.Context(), c.Store, reg, a, now())
	switch {
	case errors.Is(err, ErrPeriod):
		api.Error(w, r, http.StatusBadRequest, "bad_period", err.Error())
	case err != nil:
		o.fail(w, r, c, err)
	default:
		api.WriteJSON(w, http.StatusOK, rep)
	}
}

func (o Options) fail(w http.ResponseWriter, r *http.Request, c api.Call, err error) {
	o.Log.Printf("%s %s %s: %v", c.RequestID, r.Method, r.URL.Path, err) // a nil log writes nothing
	api.Error(w, r, http.StatusInternalServerError, "failed", "the usage report could not be read")
}
