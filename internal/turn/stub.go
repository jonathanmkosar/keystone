package turn

import (
	"net/http"

	"github.com/jonathanmkosar/keystone/internal/api"
)

// Runner serves the chat and turn routes. In this commit the routes are a
// contract: each answers 501 until the build that follows.
type Runner struct{}

func notBuilt(w http.ResponseWriter, r *http.Request, _ api.Call) {
	api.Error(w, r, http.StatusNotImplemented, "not_built", "this route is not built yet")
}

func (*Runner) list(w http.ResponseWriter, r *http.Request, c api.Call)         { notBuilt(w, r, c) }
func (*Runner) create(w http.ResponseWriter, r *http.Request, c api.Call)       { notBuilt(w, r, c) }
func (*Runner) search(w http.ResponseWriter, r *http.Request, c api.Call)       { notBuilt(w, r, c) }
func (*Runner) read(w http.ResponseWriter, r *http.Request, c api.Call)         { notBuilt(w, r, c) }
func (*Runner) patch(w http.ResponseWriter, r *http.Request, c api.Call)        { notBuilt(w, r, c) }
func (*Runner) remove(w http.ResponseWriter, r *http.Request, c api.Call)       { notBuilt(w, r, c) }
func (*Runner) selectBranch(w http.ResponseWriter, r *http.Request, c api.Call) { notBuilt(w, r, c) }
func (*Runner) model(w http.ResponseWriter, r *http.Request, c api.Call)        { notBuilt(w, r, c) }
func (*Runner) send(w http.ResponseWriter, r *http.Request, c api.Call)         { notBuilt(w, r, c) }
func (*Runner) turn(w http.ResponseWriter, r *http.Request, c api.Call)         { notBuilt(w, r, c) }
func (*Runner) events(w http.ResponseWriter, r *http.Request, c api.Call)       { notBuilt(w, r, c) }
func (*Runner) stop(w http.ResponseWriter, r *http.Request, c api.Call)         { notBuilt(w, r, c) }
