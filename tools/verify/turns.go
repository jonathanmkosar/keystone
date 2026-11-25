package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// modelCallsRecorded is the model calls' part of the "calls recorded"
// check. The fourth fixed rule says every model call is recorded.
//
// The test tenant is loaded into a throwaway data folder and sends one
// message through the API, with the shipped settings, so the model is the
// fake provider playing its recorded turns. The turn must stream to its
// end event, and the tenant's audit log must then hold, under the request
// id the answer came with: the API call, before and after, and the model
// call, before and after, with what was sent, what came back and the
// usage. Nothing may be in the other tenant's log.
func modelCallsRecorded() (problems []string, err error) {
	dir, err := os.MkdirTemp("", "keystone-turn-recorded-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	other, err := in.Create("Ordinary Person")
	if err != nil {
		return nil, err
	}
	fx, err := testtenant.Shipped()
	if err != nil {
		return nil, err
	}
	o, err := testtenant.DefaultOptions()
	if err != nil {
		return nil, err
	}
	loaded, err := testtenant.Load(in, fx, o)
	if err != nil {
		return nil, err
	}
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	// The goroutine that ran the turn has the tenant's database open
	// until it is done. It is waited for before the folder is removed.
	defer stop()

	token, rec := w.signIn(loaded.Tenant.ID, fx.Password)
	if token == "" {
		return nil, fmt.Errorf("the test tenant cannot sign in through the API: status %d", rec.Code)
	}
	rec = w.request("POST", api.Prefix+"chats", token, "", nil)
	var made turn.ChatReply
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil || made.Chat.ID == "" {
		return nil, fmt.Errorf("the test tenant cannot make a chat through the API: status %d", rec.Code)
	}
	if made.Model.Source != turn.SourceTest {
		problems = append(problems, fmt.Sprintf("the test tenant's model comes from %q, and it must be its recorded turns and nothing else", made.Model.Source))
	}
	body, _ := json.Marshal(turn.SendBody{Text: "Suggest three names for a small bakery by the harbour."})
	rec = w.request("POST", api.Prefix+"chats/"+made.Chat.ID+"/turns", token, string(body), nil)
	stream := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(stream, "event: "+turn.EventEnd+"\n") || !strings.Contains(stream, `"status":"done"`) {
		return nil, fmt.Errorf("the test tenant's turn did not stream to its end: status %d, %d bytes", rec.Code, len(stream))
	}
	if !strings.Contains(stream, "event: "+turn.EventText+"\n") || !strings.Contains(stream, "event: "+turn.EventUsage+"\n") {
		problems = append(problems, "the test tenant's turn streamed no text or no usage")
	}

	id := rec.Header().Get("X-Request-Id")
	store, err := in.Store(loaded.Tenant.ID)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	entries, err := audit.List(context.Background(), store, audit.Filter{RequestID: id})
	if err != nil {
		return nil, err
	}
	var calls []audit.Entry
	apiCalls := 0
	for _, e := range entries {
		switch e.Kind {
		case audit.KindModel:
			calls = append(calls, e)
		case audit.KindAPI:
			apiCalls++
		}
	}
	switch {
	case apiCalls != 2:
		problems = append(problems, fmt.Sprintf("the call that sent the message left %d API entries, want one before and one after", apiCalls))
	case len(calls) != 2:
		problems = append(problems, fmt.Sprintf("the turn's model call left %d audit entries under the request id of the call that sent it, want one before the call and one for its end", len(calls)))
	case calls[0].Outcome != audit.OutcomeStarted || calls[0].Sent == "":
		problems = append(problems, "the model call's first entry was not written before the call, with what was sent")
	case calls[1].Outcome != audit.OutcomeOK || calls[1].Received == "" || calls[1].Usage.OutputTokens == 0:
		problems = append(problems, fmt.Sprintf("the model call's end is recorded as %s, with %d output tokens", calls[1].Outcome, calls[1].Usage.OutputTokens))
	case calls[0].Action != turn.TestModelName || calls[1].Action != calls[0].Action:
		problems = append(problems, fmt.Sprintf("the test tenant's model call is recorded as %q: it was not its recorded turns", calls[0].Action))
	}

	theirs, err := in.Store(other.ID)
	if err != nil {
		return nil, err
	}
	defer theirs.Close()
	if got, err := logOf(theirs); err != nil {
		return nil, err
	} else if len(got) != 0 {
		problems = append(problems, fmt.Sprintf("the test tenant's turn wrote %d entries to another tenant's audit log", len(got)))
	}
	return problems, nil
}
