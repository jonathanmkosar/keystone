package main

import (
	"errors"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/trickdrill"
)

// The tricked-agent drill (P13-01). The standing checks "approval",
// "sensitive content" and "browser use" each prove one guard with one kind
// of outside content; the drill plays an agent that obeys whatever it
// reads, from every kind at once (a web search result, a web page, a
// fetched file, an email, a calendar event, a connector's result and its
// tools' descriptions, a knowledge source), against the real server, and
// proves that every side effect it tries is refused or asks, that nothing
// it aimed at happens, that no hosted model is sent sensitive content, and
// that every call is in the audit log. See
// docs/architecture/tricked-agent-drill.md.
func init() {
	componentDocs["internal/trickdrill"] = "docs/architecture/tricked-agent-drill.md"
	networkAllowed["internal/trickdrill"] = "the drill calls the API's handlers in memory and serves a test MCP server, a stand-in Google and a stand-in for Keystone's own address on this machine; it calls nothing else"
}

// checkTrickDrill is the standing check "tricked-agent drill".
func checkTrickDrill(string) error {
	r, err := trickdrill.Run()
	if err != nil {
		return err
	}
	if len(r.Attempts) == 0 {
		return errors.New("the tricked-agent drill tried nothing")
	}
	if len(r.GotThrough) > 0 {
		return errors.New(strings.Join(r.GotThrough, "\n"))
	}
	return nil
}
