package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The marks a request's cache points ask for (P5-03): never more than the
// four the API takes, never on a thinking block, always at the end of the
// standing start and of the last message, and none with the setting off.
// Every request here is invented.
func TestTheCacheMarksStayInsideTheRules(t *testing.T) {
	p, err := New(testEntry("http://127.0.0.1:9"), testKey, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	off := testOptions()
	off.CacheControl = false
	quietP, err := New(testEntry("http://127.0.0.1:9"), testKey, off)
	if err != nil {
		t.Fatal(err)
	}
	text := func(s string) seam.Block { return seam.Block{Type: seam.BlockText, Text: s} }
	thinking := seam.Block{Type: seam.BlockThinking, Text: "Weighing it up.", Signature: "c2lnbmVkLWJ5LXRoZS10ZXN0cw=="}
	tried := 0
	for systems := 0; systems <= 3; systems++ {
		for tools := 0; tools <= 2; tools++ {
			for turns := 1; turns <= 4; turns++ {
				req := seam.Request{}
				for i := 0; i < systems; i++ {
					req.System = append(req.System, text(fmt.Sprintf("Standing text %d.", i)))
				}
				for i := 0; i < tools; i++ {
					req.Tools = append(req.Tools, seam.Tool{Name: fmt.Sprintf("tool_%d", i), InputSchema: json.RawMessage(`{"type":"object"}`)})
				}
				for i := 1; i < turns; i++ {
					reply := []seam.Block{thinking, text(fmt.Sprintf("Answer %d.", i))}
					if i%2 == 0 {
						reply = []seam.Block{thinking} // a reply that was all thinking
					}
					req.Messages = append(req.Messages,
						seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{text(fmt.Sprintf("Question %d.", i))}},
						seam.Message{Role: seam.RoleAssistant, Blocks: reply})
				}
				req.Messages = append(req.Messages, seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{text("Recalled."), text("The last question.")}})
				for start := 0; start <= systems; start++ {
					for history := 0; history <= len(req.Messages); history++ {
						req.Cache = &seam.Cache{Start: start, History: history}
						tried++
						body, err := p.body(req, true)
						if err != nil {
							t.Fatalf("%d system blocks, %d tools, points %+v: %v", systems, tools, *req.Cache, err)
						}
						marks := strings.Count(string(body), `"cache_control"`)
						if marks == 0 || marks > 4 {
							t.Fatalf("%d system blocks, %d tools, points %+v: %d marks\n%s", systems, tools, *req.Cache, marks, body)
						}
						var sent wireRequest
						if err := json.Unmarshal(body, &sent); err != nil {
							t.Fatal(err)
						}
						for _, m := range sent.Messages {
							for _, b := range m.Content {
								if b.CacheControl != nil && !markable(b) {
									t.Fatalf("points %+v: a %s block was marked", *req.Cache, b.Type)
								}
							}
						}
						last := sent.Messages[len(sent.Messages)-1].Content
						if last[len(last)-1].CacheControl == nil {
							t.Fatalf("points %+v: the last message was not marked", *req.Cache)
						}
						if start > 0 && sent.System[start-1].CacheControl == nil {
							t.Fatalf("points %+v: the end of the standing start was not marked", *req.Cache)
						}
						if body, _ := quietP.body(req, true); strings.Contains(string(body), "cache_control") {
							t.Fatalf("points %+v: a mark was sent with the setting off", *req.Cache)
						}
					}
				}
			}
		}
	}
	if tried < 100 {
		t.Fatalf("only %d requests were tried", tried)
	}
}
