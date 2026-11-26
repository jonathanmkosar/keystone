package operations

import (
	"context"
	"net/http"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// Options are what the routes run with.
type Options struct {
	// Schedules lists a person's schedules: the scheduler's List. Nil
	// shows none.
	Schedules func(ctx context.Context, s *tenant.Store) (schedules.SchedulesReply, error)
	// Log is the server's own log. Nil means none.
	Log *api.Log
	// Now is the clock. Nil means the machine's.
	Now func() time.Time
}

// Routes returns Operations' two routes. Each acts for the signed-in
// person, on the store the shell hands it, so a person sees their own
// tenant and no one else's (fixed rule 1).
func Routes(o Options) []api.Route {
	return []api.Route{
		{
			Method: "GET", Path: api.Prefix + "operations", Handle: o.report,
			Summary: "Operations and Monitor: your background tasks and scheduled runs by state, with the newest, the stuck and the failed; " +
				"your schedules with their next and last runs; each model's calls, errors, refusals and median time over operations.health_window, with its last error; " +
				"your newest traces, each turn or run with its calls, tool steps, tokens, cost and outcome; " +
				"and every limit (the monthly cap, the memory reserve, the disk, your database, files and artifacts, and the budget of each run under way) " +
				"with its share used, its watch level and its trend.",
			Reply: Report{},
		},
		{
			Method: "GET", Path: api.Prefix + "operations/watch", Handle: o.watch,
			Summary: "The limits at or past their watch level, which the home screen shows a notice for. They are read as the Operations report reads them.",
			Reply:   WatchReply{},
		},
	}
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o Options) report(w http.ResponseWriter, r *http.Request, c api.Call) {
	reg, err := settings.Registry()
	if err != nil {
		o.fail(w, r, c, err)
		return
	}
	rep, err := Read(r.Context(), c.Store, reg, Sources{Schedules: o.Schedules}, o.now())
	if err != nil {
		o.fail(w, r, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, rep)
}

func (o Options) watch(w http.ResponseWriter, r *http.Request, c api.Call) {
	reg, err := settings.Registry()
	if err != nil {
		o.fail(w, r, c, err)
		return
	}
	rep, err := Watch(r.Context(), c.Store, reg, o.now())
	if err != nil {
		o.fail(w, r, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, rep)
}

func (o Options) fail(w http.ResponseWriter, r *http.Request, c api.Call, err error) {
	o.Log.Printf("%s %s %s: %v", c.RequestID, r.Method, r.URL.Path, err) // a nil log writes nothing
	api.Error(w, r, http.StatusInternalServerError, "failed", "the operations report could not be read")
}
