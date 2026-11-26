package main

import (
	"errors"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/isolationdrill"
)

// The tenant isolation drill (P13-02). The standing check "tenant
// isolation" walks every route with no session, a re-addressed one and a
// signed-out one; the drill adds a second person who is signed in and holds
// the first person's real ids, and tries the API, the tools, the jobs and
// agents, and the files on disk. See docs/architecture/isolation-drill.md.
func init() {
	componentDocs["internal/isolationdrill"] = "docs/architecture/isolation-drill.md"
	networkAllowed["internal/isolationdrill"] = "the drill calls the API's handlers in memory and serves a test MCP server and a stand-in Google on this machine; it calls nothing else"
}

// checkIsolationDrill is the standing check "isolation drill": every way
// in the drill tries is refused or answered with nothing of the first
// person's, the first person's folder, database and audit log do not move,
// and every try is in the second person's own audit log.
func checkIsolationDrill(string) error {
	r, err := isolationdrill.Run()
	if err != nil {
		return err
	}
	if len(r.Attempts) == 0 {
		return errors.New("the isolation drill tried nothing")
	}
	if len(r.Reached) > 0 {
		return errors.New(strings.Join(r.Reached, "\n"))
	}
	return nil
}
