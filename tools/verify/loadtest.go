package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/loadtest"
)

// The standing check "load test" (P13-03). The item's proof: times stay
// inside their budgets and the Monitor figures match the test's own.
//
// It runs the load test's small profile: on the real server, put together
// as internal/app puts it, in memory, with the fake provider on the seam
// and a throwaway data folder, a few dozen invented chats and one long
// chat are written for one person and a few for two more, by the same
// generator the full run uses; every measurement is made, context packing,
// past-chat search, the chat list and a long chat's page, the first
// streamed words and a streamed reply for one person and for every person
// at once, the usage report and the Operations report; each one's median
// is held to its budget in settings/loadtest.json; and what
// GET /api/v1/operations reports for the loaded person, the database's
// size, the files, the artifacts, the cap, each model's calls and the
// traces, is compared with the test's own counts.
//
// The full run, years of chats, is go run ./tools/loadtest, which takes
// minutes and is not part of verify. docs/architecture/load-test.md has
// its table.
func init() {
	componentDocs["internal/loadtest"] = "docs/architecture/load-test.md"
	componentDocs["tools/loadtest"] = "docs/architecture/load-test.md"
	networkAllowed["internal/loadtest"] = "the load test calls the API's handlers in memory, on a throwaway data folder; it opens no connection"
}

func checkLoadTest(string) error {
	p, err := loadtest.SmallProfile()
	if err != nil {
		return err
	}
	res, err := loadtest.Run(p, loadtest.Options{})
	if err != nil {
		return err
	}
	if len(res.Measurements) != len(loadtest.Measures) {
		return fmt.Errorf("the load test made %d measurements, and there are %d", len(res.Measurements), len(loadtest.Measures))
	}
	if len(res.Monitor) == 0 {
		return errors.New("the load test compared no figure of the Monitor")
	}
	if len(res.Problems) > 0 {
		return errors.New(strings.Join(res.Problems, "\n"))
	}
	return nil
}
