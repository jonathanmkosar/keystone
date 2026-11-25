package fake

import (
	"context"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The context builder puts recalled knowledge before the person's words in
// their message (P5-03). A script's whole text still picks the message by
// the person's own words, and a part of the text is still looked for in all
// of it.
func TestAScriptMatchesThePersonsWordsAfterRecall(t *testing.T) {
	says := func(s string) []Step { return []Step{{Reply: reply(seam.StopEnd, seam.Usage{}, text(s))}} }
	p := provider(t, Options{},
		Script{Name: "anything", Steps: says("anything")},
		Script{Name: "the exact question", When: When{UserText: "When does the ferry leave?"}, Steps: says("the exact question")},
		Script{Name: "the tide", When: When{UserTextContains: "high water is at six"}, Steps: says("the tide")},
	)
	recalled := func(recall, said string) seam.Request {
		return seam.Request{Messages: []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{text(recall), text(said)}}}}
	}
	for name, c := range map[string]struct {
		req  seam.Request
		want string
	}{
		"the person's words after recall":    {recalled("From the person's knowledge store: the north pier is closed.", "When does the ferry leave?"), "the exact question"},
		"the whole text, as before":          {ask("When does the ferry leave?"), "the exact question"},
		"a part of the text, in recall":      {recalled("From the person's knowledge store: high water is at six.", "Good morning."), "the tide"},
		"recall alone does not pick a whole": {recalled("When does the ferry leave?", "And the bus?"), "anything"},
	} {
		got, err := seam.Call(context.Background(), p, c.req)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if seam.Text(got.Message) != c.want {
			t.Errorf("%s: answered by %q, want %q", name, seam.Text(got.Message), c.want)
		}
	}
}
