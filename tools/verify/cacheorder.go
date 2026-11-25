package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/settings"
)

// cacheTurns is how many messages the growing chat of the cache step sends.
const cacheTurns = 5

// cacheProblems is the cache step of the "context fit" check (P5-03): a
// provider can cache the start of a call only if that start is the same,
// byte for byte, from one call of a chat to the next.
//
// For every fixture model, a chat grows by one message at a time, with
// tools given in a different order each time and new recall each time,
// and each call is built by the context builder with the shipped settings.
// While no turn is left out, the step proves for each call after the first:
//
//   - the tools are by name, and the same bytes as the call before;
//   - the system text is the same bytes as the call before;
//   - the call's cache points say the standing start is the rules, the
//     profile and the map, and the history is every message before the
//     current one;
//   - the history the call before sent is sent again in the same bytes,
//     and the person's message of the call before follows it as written;
//   - recall is in the current message only, after the history.
func cacheProblems(entries []models.Model, o budget.Options) ([]string, error) {
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	opts, err := contextbuild.FromSettings(reg, nil)
	if err != nil {
		return nil, err
	}
	encode := func(v any) string {
		raw, _ := json.Marshal(v)
		return string(raw)
	}
	tools := []seam.Tool{
		{Name: "tide_table", Description: "Reads the tide table.", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "ferry_times", Description: "Reads the ferry timetable.", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "harbour_notices", Description: "Reads the notice board.", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	ctx := context.Background()
	var problems []string
	for _, m := range entries {
		bad := func(n int, format string, args ...any) {
			problems = append(problems, fmt.Sprintf("model %s, message %d: %s", m.Name, n, fmt.Sprintf(format, args...)))
		}
		var prev seam.Request
		compared := 0
		for n := 1; n <= cacheTurns; n++ {
			var branch []seam.Message
			for i := 1; i < n; i++ {
				branch = append(branch,
					seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: fmt.Sprintf("Tide %d?", i)}}},
					seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: fmt.Sprintf("High at %d.", i)}}})
			}
			branch = append(branch, seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: fmt.Sprintf("Tide %d?", n)}}})
			given := slices.Clone(tools)
			for range n { // a different order on every call
				given = append(given[1:], given[0])
			}
			recalled := fmt.Sprintf("Recalled for message %d: the harbour wall.", n)
			in := contextbuild.Input{
				Model: m, Budget: o, Options: opts, Messages: branch, Tools: given,
				Profile: "Keeps a boat.", PersonMap: "The harbour.", Summary: "Earlier: tides.",
				Recall: []contextbuild.Item{{Label: "harbour", Text: recalled}},
			}
			req, rec, err := contextbuild.Build(ctx, in)
			if err != nil {
				bad(n, "it was not built: %v", err)
				break
			}
			if slices.ContainsFunc(rec.Turns, func(t contextbuild.TurnRecord) bool { return !t.Kept }) ||
				rec.Part(contextbuild.PartRecall).Kept != 1 || rec.Part(contextbuild.PartSummary).Kept != 1 {
				break // the window is full: what is left out is the packing step's to judge
			}
			if !slices.IsSortedFunc(req.Tools, func(a, b seam.Tool) int { return strings.Compare(a.Name, b.Name) }) {
				bad(n, "the tools are not by name")
			}
			if c := req.Cache; c == nil || c.Start != 3 || c.History != len(branch)-1 {
				bad(n, "the cache points are %+v, not the three blocks of the standing start and %d messages of history", c, len(branch)-1)
				break
			}
			cur := req.Messages[req.Cache.History]
			if len(cur.Blocks) != 2 || cur.Blocks[0].Text != recalled || strings.Contains(encode(req.System), recalled) ||
				strings.Contains(encode(req.Messages[:req.Cache.History]), recalled) {
				bad(n, "recall is not in the current message alone, before the person's words")
			}
			if n > 1 {
				compared++
				h := prev.Cache.History
				switch {
				case encode(req.Tools) != encode(prev.Tools):
					bad(n, "the tools are not the bytes of the call before")
				case encode(req.System) != encode(prev.System):
					bad(n, "the system text is not the bytes of the call before")
				case encode(req.Messages[:h]) != encode(prev.Messages[:h]):
					bad(n, "the history of the call before is not sent again in the same bytes")
				case encode(req.Messages[h]) != encode(seam.Message{Role: seam.RoleUser, Blocks: prev.Messages[h].Blocks[1:]}):
					bad(n, "the person's message of the call before is not sent as it was written")
				}
			}
			prev = req
		}
		if compared < 2 {
			problems = append(problems, fmt.Sprintf("model %s: only %d calls of the growing chat fit with nothing left out, too few to compare", m.Name, compared+1))
		}
	}
	return problems, nil
}
