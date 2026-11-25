package turn

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
)

// Every page and name in these tests is invented.

// A local model that can use tools, for the knowledge lookups.
var toolHouse = models.Model{
	Name: "house-tools", Label: "House Tools", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8080/v1",
	ProviderModel: "tools-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassLocal,
	Capabilities: []models.Capability{models.CapTools},
}

// lookupScripts has the model look in the knowledge store for what it was
// asked about, then answer.
const lookupScripts = `{"test_data":true,"scripts":[
	{"name":"beans","when":{"user_text":"What do my notes say about beans?"},"steps":[
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_1","name":"knowledge_search","input":{"query":"runner beans"}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"They go in the north bed."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]},
	{"name":"sleep","when":{"user_text":"What did I note about sleep?"},"steps":[
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_1","name":"knowledge_search","input":{"query":"insomnia"}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"Melatonin helped."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]},
	{"name":"anything else","when":{},"steps":[{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"A plain answer."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]}
]}`

func lookupRig(t *testing.T) (*rig, *served) {
	g := newRig(t)
	g.addModels(toolHouse)
	p := scripted(t, lookupScripts)
	g.extra = adapters.Set{models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return p, nil }}
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	seedKnowledge(g)
	return g, g.serve(nil)
}

// The model is offered the lookups when the person's store is open, and a
// search it makes finds the page and gives it back as the tool's result.
func TestAModelCanSearchTheKnowledgeStore(t *testing.T) {
	g, sv := lookupRig(t)
	body, _ := json.Marshal(SendBody{Text: "What do my notes say about beans?"})
	events, _, requestID := sv.send(g.ordinary.ID, sv.newChat(g.ordinary.ID), string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	if call := sentFor(g, requestID); !strings.Contains(call, "knowledge_search") || !strings.Contains(call, "knowledge_read_page") {
		t.Fatalf("the lookups were not offered:\n%s", call)
	}
	got, ok := results(t, events)["call_1"]
	if !ok || got.IsError || !strings.Contains(said(got), "Runner beans") {
		t.Fatalf("the search's result is %+v (%v): %s", got, ok, said(got))
	}
}

// A search whose result is from a sensitive space holds the chat to models
// that may read it.
func TestASensitiveLookupHoldsTheChat(t *testing.T) {
	g, sv := lookupRig(t)
	chatID := sv.newChat(g.ordinary.ID)
	body, _ := json.Marshal(SendBody{Text: "What did I note about sleep?"})
	events, _, _ := sv.send(g.ordinary.ID, chatID, string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	got := results(t, events)["call_1"]
	if got.IsError || !strings.Contains(said(got), "Sleep notes") {
		t.Fatalf("the local model did not get the sensitive page: %s", said(got))
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		held, err := recall.Held(context.Background(), s, chatID)
		if err != nil || !held {
			t.Errorf("a chat given a sensitive result is not held (%v, %v)", held, err)
		}
	})
}

// With tools switched off, no lookup is offered.
func TestNoLookupWithToolsOff(t *testing.T) {
	g, sv := lookupRig(t)
	g.set(tenantsettings.Person, KeyToolsEnabled, "false")
	body, _ := json.Marshal(SendBody{Text: "Can you write a short poem about the sea?"})
	_, _, requestID := sv.send(g.ordinary.ID, sv.newChat(g.ordinary.ID), string(body))
	if call := sentFor(g, requestID); strings.Contains(call, "knowledge_search") {
		t.Errorf("a lookup was offered with tools switched off:\n%s", call)
	}
}
