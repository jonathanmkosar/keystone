package turn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
)

// Location per message (P10-06). The coordinates are invented: a point in
// the open Atlantic.
const (
	weatherAsk  = "Will it rain this afternoon?"
	weatherNext = "And tomorrow morning?"
	weatherLine = "The person's approximate location: 12.35, -45.68 (rounded to about 1 km), shared with this message."
)

var exactPlace = Location{Latitude: 12.345678, Longitude: -45.678912}

// heard is a provider that keeps every request it is sent and answers each
// with the same short reply.
type heard struct {
	mu   sync.Mutex
	reqs []seam.Request
}

func (p *heard) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	p.mu.Unlock()
	return answers{chat: "Dry, with a light wind.", title: "Rain this afternoon"}.Stream(ctx, req, emit)
}

// asked returns the requests whose last message holds the words.
func (p *heard) asked(words string) []seam.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []seam.Request
	for _, r := range p.reqs {
		if n := len(r.Messages); n > 0 && strings.Contains(seam.Text(r.Messages[n-1]), words) {
			out = append(out, r)
		}
	}
	return out
}

// serveLocation starts a server where the ordinary tenant has the house
// model, answered by p, with titles, recaps and the memory pass off so
// that each turn makes one call.
func (g *rig) serveLocation(p *heard) *served {
	g.t.Helper()
	g.extra = adapters.Set{models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return p, nil }}
	sv := g.serve(nil)
	// The inspector's and incognito's routes beside the chat routes.
	routes := append(Routes(sv.runner), InspectorRoutes(sv.runner)...)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, append(routes, IncognitoRoutes(sv.runner)...))
	if err != nil {
		g.t.Fatal(err)
	}
	sv.server.Close()
	sv.server = httptest.NewServer(h)
	g.addModels(houseModel)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	g.set(tenantsettings.Person, KeyRecap, "false")
	g.set(tenantsettings.Person, memory.SettingOn, "false")
	return sv
}

func located(text string, at Location) string {
	raw, _ := json.Marshal(SendBody{Text: text, Location: &at})
	return string(raw)
}

func plainBody(text string) string {
	raw, _ := json.Marshal(SendBody{Text: text})
	return string(raw)
}

// The item's proof, first half: a message sent with a location carries the
// rounded position into the model's request, the audit log, the record and
// the inspector, as one line before the person's words, and the next call
// sends that message the same way, byte for byte, with no line of its own.
func TestAMessageSentWithALocationCarriesTheRoundedPosition(t *testing.T) {
	g := newRig(t)
	p := &heard{}
	sv := g.serveLocation(p)
	id := sv.newChat(g.ordinary.ID)

	events, turnID, requestID := sv.send(g.ordinary.ID, id, located(weatherAsk, exactPlace))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v: %s", end, names(events))
	}
	calls := p.asked(weatherAsk)
	if len(calls) != 1 {
		t.Fatalf("the model was asked %d times, want once", len(calls))
	}
	said := calls[0].Messages[len(calls[0].Messages)-1]
	if said.Role != seam.RoleUser || len(said.Blocks) != 2 || said.Blocks[0].Text != weatherLine || said.Blocks[1].Text != weatherAsk {
		raw, _ := json.Marshal(said)
		t.Fatalf("the person's message reached the model as %s, want the line and then the words", raw)
	}

	// What was sent is in the audit log, rounded; the exact position is
	// nowhere: not in the log, not in the turn's stored input.
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if err != nil || len(entries) == 0 || !strings.Contains(entries[0].Sent, weatherLine) {
			t.Errorf("the model call's audit entry does not hold the line (%d entries, %v)", len(entries), err)
		}
		for _, e := range entries {
			if strings.Contains(e.Sent, "12.3456") || strings.Contains(e.Sent, "45.6789") {
				t.Errorf("the audit log holds the exact position")
			}
		}
		var input string
		if err := s.DB().QueryRow("SELECT input FROM turns WHERE id = ?", turnID).Scan(&input); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(input, "12.3456") || !strings.Contains(input, `"latitude":12.35`) {
			t.Errorf("the turn's stored input is %s, want the rounded position only", input)
		}
	})

	// The record keeps it with the person's message, and with no other.
	var got ChatReply
	sv.call(g.ordinary.ID, "GET", "chats/"+id, "", http.StatusOK, &got)
	want := &SentLocation{Latitude: 12.35, Longitude: -45.68, Decimals: 2, Line: weatherLine}
	if len(got.Messages) != 2 || !reflect.DeepEqual(got.Messages[0].Location, want) || got.Messages[1].Location != nil {
		raw, _ := json.Marshal(got.Messages)
		t.Fatalf("the record holds %s", raw)
	}
	if len(got.Messages[0].Blocks) != 1 || got.Messages[0].Blocks[0].Text != weatherAsk {
		t.Errorf("the person's words in the record are %+v, want them as they were written", got.Messages[0].Blocks)
	}
	// The stream's message event said so too.
	for _, e := range events {
		if e.name == EventMessage {
			var m Message
			e.into(t, &m)
			if m.Role == seam.RoleUser && !reflect.DeepEqual(m.Location, want) {
				t.Errorf("the message event carries the location %+v", m.Location)
			}
		}
	}
	// The inspector shows the line the turn sent.
	if in := sv.inspectTurn(g.ordinary.ID, id, turnID); in.Location == nil || in.Location.Line != weatherLine {
		t.Errorf("the inspector shows the location %+v", in.Location)
	}

	// The next message has none of its own, and the first goes as it went.
	events, nextTurn, _ := sv.send(g.ordinary.ID, id, plainBody(weatherNext))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the second turn ended as %+v", end)
	}
	next := p.asked(weatherNext)
	if len(next) != 1 || len(next[0].Messages) != 3 {
		t.Fatalf("the second turn's call is %+v", next)
	}
	if !reflect.DeepEqual(next[0].Messages[0], said) {
		t.Errorf("the second call sent the first message differently: the start of the history would not be read from the cache")
	}
	if !reflect.DeepEqual(next[0].System, calls[0].System) {
		t.Errorf("the standing start changed between the calls")
	}
	if tail := next[0].Messages[2]; len(tail.Blocks) != 1 || tail.Blocks[0].Text != weatherNext {
		t.Errorf("the second message reached the model as %+v, want its words alone", tail.Blocks)
	}
	if in := sv.inspectTurn(g.ordinary.ID, id, nextTurn); in.Location != nil {
		t.Errorf("the inspector shows a location on a message sent without one: %+v", in.Location)
	}
	if n := countRows(g, g.ordinary.ID, "SELECT count(*) FROM message_locations"); n != 1 {
		t.Errorf("%d locations are kept, want 1", n)
	}
}

// The item's proof, second half: a message without a location carries
// none, and its request and its stored input are byte for byte what they
// are in a chat where no location was ever sent.
func TestAMessageWithoutALocationIsSentAsBefore(t *testing.T) {
	g := newRig(t)
	p := &heard{}
	sv := g.serveLocation(p)
	sentOf := func(requestID string) string {
		var sent string
		g.with(g.ordinary.ID, func(s *tenant.Store) {
			entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
			if err != nil || len(entries) == 0 {
				t.Fatalf("no model call is recorded (%v)", err)
			}
			sent = entries[0].Sent
		})
		return sent
	}
	inputOf := func(turnID string) string {
		var input string
		g.with(g.ordinary.ID, func(s *tenant.Store) {
			if err := s.DB().QueryRow("SELECT input FROM turns WHERE id = ?", turnID).Scan(&input); err != nil {
				t.Fatal(err)
			}
		})
		return input
	}

	before := sv.newChat(g.ordinary.ID)
	_, beforeTurn, beforeReq := sv.send(g.ordinary.ID, before, plainBody(weatherAsk))
	sv.send(g.ordinary.ID, sv.newChat(g.ordinary.ID), located(weatherNext, exactPlace))
	after := sv.newChat(g.ordinary.ID)
	_, afterTurn, afterReq := sv.send(g.ordinary.ID, after, plainBody(weatherAsk))

	if a, b := sentOf(beforeReq), sentOf(afterReq); a != b {
		t.Errorf("the same plain message was sent differently after a location was shared in another chat:\n%s\n%s", a, b)
	} else if strings.Contains(a, "approximate location") || strings.Contains(a, "12.35") {
		t.Errorf("a plain message's request carries a location: %s", a)
	}
	if got, want := inputOf(afterTurn), `{"text":"`+weatherAsk+`"}`; got != want || inputOf(beforeTurn) != want {
		t.Errorf("a plain message is stored as %s, want %s", got, want)
	}
	for _, r := range p.asked(weatherAsk) {
		if said := r.Messages[len(r.Messages)-1]; len(said.Blocks) != 1 || said.Blocks[0].Text != weatherAsk {
			t.Errorf("a plain message reached the model as %+v", said.Blocks)
		}
	}
	var got ChatReply
	sv.call(g.ordinary.ID, "GET", "chats/"+after, "", http.StatusOK, &got)
	for _, m := range got.Messages {
		if m.Location != nil {
			t.Errorf("message %d of a plain chat has a location", m.ID)
		}
	}
	// A chat with no location reads as before: no location field at all.
	resp := sv.request(g.ordinary.ID, "GET", "chats/"+after, "", nil)
	defer resp.Body.Close()
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw["messages"]), "location") {
		t.Errorf("a plain chat's messages name a location: %s", raw["messages"])
	}
}

// The rounding, and the words of the line and the distance.
func TestALocationIsRoundedToItsDecimals(t *testing.T) {
	for _, c := range []struct {
		v        float64
		decimals int
		want     float64
		text     string
	}{
		{12.345678, 2, 12.35, "12.35"},
		{-45.678912, 2, -45.68, "-45.68"},
		{12.345678, 1, 12.3, "12.3"},
		{12.345678, 3, 12.346, "12.346"},
		{12.345678, 4, 12.3457, "12.3457"},
		{38.6, 2, 38.6, "38.60"},
		{-0.004, 2, 0, "0.00"},
		{89.999, 2, 90, "90.00"},
		{-179.996, 2, -180, "-180.00"},
	} {
		got := roundTo(c.v, c.decimals)
		if got != c.want {
			t.Errorf("roundTo(%v, %d) = %v, want %v", c.v, c.decimals, got, c.want)
		}
		if s := formatCoordinate(got, c.decimals); s != c.text {
			t.Errorf("formatCoordinate(%v, %d) = %q, want %q", got, c.decimals, s, c.text)
		}
	}
	for n, want := range map[int]string{0: "11 km", 1: "11 km", 2: "1 km", 3: "110 m", 4: "11 m", 9: "11 m"} {
		if got := distanceWords(n); got != want {
			t.Errorf("distanceWords(%d) = %q, want %q", n, got, want)
		}
	}
	if got := clampDecimals(7); got != 4 {
		t.Errorf("clampDecimals(7) = %d", got)
	}
	template := "At {latitude}, {longitude}, within {distance}."
	if got := locationLine(template, 12.3, -45.7, 1); got != "At 12.3, -45.7, within 11 km." {
		t.Errorf("the line reads %q", got)
	}
}

// The server rounds whatever a client sends to the person's setting, and
// refuses what cannot be a location; with the switch off it refuses any.
func TestALocationIsCheckedAndRoundedByTheServer(t *testing.T) {
	g := newRig(t)
	p := &heard{}
	sv := g.serveLocation(p)
	id := sv.newChat(g.ordinary.ID)

	g.set(tenantsettings.Person, KeyLocationDecimals, "1")
	_, turnID, _ := sv.send(g.ordinary.ID, id, located(weatherAsk, exactPlace))
	var got ChatReply
	sv.call(g.ordinary.ID, "GET", "chats/"+id, "", http.StatusOK, &got)
	if loc := got.Messages[0].Location; loc == nil || loc.Latitude != 12.3 || loc.Longitude != -45.7 || loc.Decimals != 1 ||
		loc.Line != "The person's approximate location: 12.3, -45.7 (rounded to about 11 km), shared with this message." {
		t.Errorf("with one decimal the location kept is %+v", loc)
	}
	g.set(tenantsettings.Person, KeyLocationDecimals, "2")

	for name, body := range map[string]string{
		"a latitude past the pole":  located(weatherNext, Location{Latitude: 91, Longitude: 0}),
		"a longitude past the date": located(weatherNext, Location{Latitude: 0, Longitude: -180.5}),
		"a retry with a location":   `{"retry_of":` + itoa(int(got.Messages[1].ID)) + `,"location":{"latitude":1,"longitude":2}}`,
		"a field a location lacks":  `{"text":"x","location":{"latitude":1,"longitude":2,"accuracy":5}}`,
	} {
		resp := sv.request(g.ordinary.ID, "POST", "chats/"+id+"/turns", body, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s answered %d, want 400", name, resp.StatusCode)
		}
	}
	g.set(tenantsettings.Person, KeyLocationShare, "false")
	resp := sv.request(g.ordinary.ID, "POST", "chats/"+id+"/turns", located(weatherNext, exactPlace), nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("with sharing switched off a located message answered %d, want 400", resp.StatusCode)
	}
	if n := countRows(g, g.ordinary.ID, "SELECT count(*) FROM turns WHERE chat_id = ?", id); n != 1 {
		t.Errorf("the refused messages made turns: %d turns, want 1", n)
	}
	if in := sv.inspectTurn(g.ordinary.ID, id, turnID); in.Location == nil || in.Location.Decimals != 1 {
		t.Errorf("the inspector shows %+v", in.Location)
	}
}

// Fixed rule 1 holds for a location as for any message, and a location
// goes with its chat: an incognito chat may send one, and leaving it
// deletes it.
func TestALocationStaysWithItsTenantAndGoesWithItsChat(t *testing.T) {
	g := newRig(t)
	p := &heard{}
	sv := g.serveLocation(p)

	var made ChatReply
	sv.call(g.ordinary.ID, "POST", "chats/incognito", "", http.StatusCreated, &made)
	id := made.Chat.ID
	events, turnID, _ := sv.send(g.ordinary.ID, id, located(weatherAsk, exactPlace))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the incognito turn ended as %+v", end)
	}
	if n := countRows(g, g.ordinary.ID, "SELECT count(*) FROM message_locations"); n != 1 {
		t.Fatalf("%d locations are kept, want 1", n)
	}
	// Another person, with the right ids, reads nothing of it.
	sv.call(g.test.ID, "GET", "chats/"+id, "", http.StatusNotFound, nil)
	sv.call(g.test.ID, "GET", "chats/"+id+"/turns/"+turnID+"/inspect", "", http.StatusNotFound, nil)
	if n := countRows(g, g.test.ID, "SELECT count(*) FROM message_locations"); n != 0 {
		t.Errorf("the other tenant holds %d locations", n)
	}
	sv.call(g.ordinary.ID, "POST", "chats/"+id+"/close", "", http.StatusOK, nil)
	sv.call(g.ordinary.ID, "GET", "chats/"+id, "", http.StatusNotFound, nil)
	if n := countRows(g, g.ordinary.ID, "SELECT count(*) FROM message_locations"); n != 0 {
		t.Errorf("leaving the incognito chat left %d locations", n)
	}
}
