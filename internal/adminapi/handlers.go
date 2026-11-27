package adminapi

import (
	"net/http"

	"github.com/jonathanmkosar/keystone/internal/api"
)

// The contract's first commit: every admin route answers 501 until its
// handler is written.

func notBuilt(w http.ResponseWriter, r *http.Request) {
	api.Error(w, r, http.StatusNotImplemented, "not_built", "this admin route is not built yet")
}

func (h handlers) stepUp(w http.ResponseWriter, r *http.Request, c api.Call)        { notBuilt(w, r) }
func (h handlers) passkeyPrompt(w http.ResponseWriter, r *http.Request, c api.Call) { notBuilt(w, r) }
func (h handlers) endStepUp(w http.ResponseWriter, r *http.Request, c api.Call)     { notBuilt(w, r) }
func (h handlers) people(w http.ResponseWriter, r *http.Request, c api.Call)        { notBuilt(w, r) }
