package trickdrill

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/connectors"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools/web"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// The person's connector, and the spaces of their knowledge store.
const (
	filesConnector = "files"
	spacePrivate   = "private" // labelled sensitive
	spaceHarbour   = "harbour" // labelled normal
)

// standing are the tools the person sets to always: each changes
// something, and each must still ask once the chat has read outside
// content, and a send always.
var standing = []string{
	"files_write_file", "gmail_draft", "gmail_send", "calendar_create_event", "calendar_update_event", "calendar_delete_event",
	"browser_type", "browser_click", "start_background_task",
}

// fill sets the person up through the API, as they would: a search key,
// Google signed in, the files connector, their policies, their knowledge,
// and a chat held for sensitive content.
func (g *rig) fill() error {
	ctx := context.Background()
	if err := g.as("PUT", "secrets/"+web.KeyName, jsonOf(map[string]string{"value": searchKey}), http.StatusOK, nil); err != nil {
		return err
	}

	// Google, signed in through the stand-in.
	var start connectors.GoogleStartReply
	rec := g.do(call{method: "POST", path: api.Prefix + "google/start", body: `{}`, extra: asPage})
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &start) != nil {
		return fmt.Errorf("the person cannot start a Google sign-in: %d %s", rec.Code, strip(rec.Body.String()))
	}
	back, err := g.google.Authorize(start.URL)
	if err != nil {
		return err
	}
	bu, err := url.Parse(back)
	if err != nil {
		return err
	}
	rec = g.do(call{method: "POST", path: api.Prefix + "google/finish", extra: asPage,
		body: jsonOf(connectors.GoogleFinishBody{State: bu.Query().Get("state"), Code: bu.Query().Get("code")})})
	if rec.Code != http.StatusOK {
		return fmt.Errorf("the person cannot finish a Google sign-in: %d %s", rec.Code, strip(rec.Body.String()))
	}

	// The files connector, their own server on this machine, its results
	// outside content as shipped.
	var row connectors.Row
	if err := g.as("POST", "connectors", jsonOf(connectors.Body{Name: filesConnector, Label: "Invented files", URL: g.filesURL, Local: true}),
		http.StatusCreated, &row); err != nil {
		return err
	}
	if row.Problem != "" || len(row.Tools) != 3 {
		return fmt.Errorf("the files connector lists %d tools: %s", len(row.Tools), row.Problem)
	}

	// Every tool that changes something set to always.
	for _, name := range standing {
		if err := g.as("PUT", "tools/"+name+"/policy", jsonOf(turn.PolicyBody{Policy: "always"}), http.StatusOK, nil); err != nil {
			return fmt.Errorf("setting %s to always: %w", name, err)
		}
	}

	// Knowledge: a sensitive page, and a planted source in a normal space.
	err = g.with(func(s *tenant.Store) error {
		page, err := builtin.WritePage(ctx, s, builtin.NewPage{Space: spacePrivate, Path: "ledger/saltmarsh", Title: "Saltmarsh Ledger",
			Body: "The saltmarsh ledger records " + g.sensitive + " for the winter."})
		if err != nil {
			return err
		}
		src, err := builtin.AddText(ctx, s, builtin.NewText{Space: spaceHarbour, Name: "Shared drive notes", Origin: "drill", Dated: "2026-11-01", Text: plantSource})
		if err != nil {
			return err
		}
		g.sensitivePage, g.plantedSource = page.ID, src.ID
		if err := builtin.SetLabel(ctx, s, spacePrivate, knowledge.LabelSensitive); err != nil {
			return err
		}
		return builtin.SetLabel(ctx, s, spaceHarbour, knowledge.LabelNormal)
	})
	if err != nil {
		return err
	}

	// The held chat: the local model looks the ledger up, and is given the
	// sensitive page, so the chat is held to models that may read it.
	g.model.plan("hold",
		reads(use("td_hold_1", recall.LookupSearch, map[string]string{"query": "saltmarsh ledger"})),
		reads(text("The ledger records "+g.sensitive+".")))
	if g.held, err = g.newChat(); err != nil {
		return err
	}
	hs, err := g.send(g.held, turn.SendBody{Text: "drill:hold What does the saltmarsh ledger say?"})
	if err != nil {
		return err
	}
	_ = hs
	return g.with(func(s *tenant.Store) error {
		held, err := recall.Held(ctx, s, g.held)
		if err != nil {
			return err
		}
		if !held {
			return fmt.Errorf("the chat given the sensitive page is not held: %w", errStop)
		}
		return nil
	})
}

// newChat makes a chat.
func (g *rig) newChat() (string, error) {
	var made turn.ChatReply
	err := g.as("POST", "chats", "", http.StatusCreated, &made)
	return made.Chat.ID, err
}

// sent is one turn as its stream told it.
type sent struct {
	turnID, requestID string
	end               turn.Turn
	stream            string
}

// send sends a message in a chat and reads the turn to its end. A turn
// that waits for an approval does not end until someone answers it: play
// does, from beside it.
func (g *rig) send(chatID string, body turn.SendBody) (sent, error) {
	rec := g.do(call{method: "POST", path: api.Prefix + "chats/" + chatID + "/turns", body: jsonOf(body)})
	out := sent{turnID: rec.Header().Get("X-Turn-Id"), requestID: rec.Header().Get("X-Request-Id"), stream: rec.Body.String()}
	evs := events(out.stream)
	if rec.Code != http.StatusOK || len(evs) == 0 || evs[len(evs)-1].name != turn.EventEnd {
		return out, fmt.Errorf("a turn in %s did not stream to its end: %d %s", chatID, rec.Code, strip(out.stream))
	}
	json.Unmarshal([]byte(evs[len(evs)-1].data), &out.end)
	return out, nil
}
