package turn

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/knowledge/personmap"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// This file is the server side of the Memory screen (P6-07): what the
// person's knowledge store remembers, and correcting and forgetting a fact
// the memory pass added. internal/knowledge/memory decides what a change
// does; here the settings are read, the store is opened, and the profile
// follows a fact it says. docs/architecture/memory-screens.md is the whole
// of it.

// The Memory screen's own settings, in settings/knowledge.json.
const (
	KeyMemoryScreenMemories = "knowledge.memory_screen_memories"
	KeyMemoryScreenPages    = "knowledge.memory_screen_pages"
)

// MemoryStore says which store the person has and what it can do.
type MemoryStore struct {
	// Adapter is the store's adapter, knowledge.adapter: none, built-in or
	// waymarks.
	Adapter string `json:"adapter"`
	// CanRemove says forgetting takes a source out of the store. When it is
	// false the store keeps it, and Keystone withdraws it.
	CanRemove bool `json:"can_remove"`
	// CanAdd says the store takes new sources, so the facts a change keeps,
	// and a corrected one, can be put back.
	CanAdd bool `json:"can_add"`
	// Note says, for a person, what forgetting does in this store.
	Note string `json:"note"`
	// Unavailable says the store could not be read, and why. What the
	// memory pass added is listed from Keystone's own record all the same.
	Unavailable string `json:"unavailable,omitempty"`
}

// Remembered is one source the memory pass added: its facts and where they
// came from.
type Remembered struct {
	SourceID string `json:"source_id"`
	ChatID   string `json:"chat_id"`
	// ChatTitle is the chat's title, empty when it has none or was deleted.
	ChatTitle string `json:"chat_title"`
	// ChatGone says the chat was deleted. Its facts stay until forgotten.
	ChatGone bool      `json:"chat_gone"`
	TurnID   string    `json:"turn_id"`
	Facts    []string  `json:"facts"`
	Added    time.Time `json:"added"`
}

// MemoryPage is one page of a space, as the catalog lists it.
type MemoryPage struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Title string `json:"title"`
	Kind  string `json:"kind,omitempty"`
}

// MemorySpace is one space of the store: its label, the sources the memory
// pass added to it, and its pages.
type MemorySpace struct {
	Name      string       `json:"name"`
	Sensitive bool         `json:"sensitive"`
	Memories  []Remembered `json:"memories"`
	Pages     []MemoryPage `json:"pages"`
	// MorePages counts the pages past knowledge.memory_screen_pages.
	MorePages int `json:"more_pages"`
}

// MemoryOverview answers GET /memory: the store, the profile, the map, and
// each space with what it remembers.
type MemoryOverview struct {
	Store MemoryStore `json:"store"`
	// Profile is the person's profile, as they wrote it.
	Profile string `json:"profile"`
	// Map is the map of the store, the whole of it, sensitive spaces too:
	// it is the person's own, shown to them and not sent to any model here.
	Map    string        `json:"map"`
	Spaces []MemorySpace `json:"spaces"`
	// More counts the sources the memory pass added past
	// knowledge.memory_screen_memories, the oldest, which are not listed.
	More int `json:"more"`
}

// MemoryEdit is the body of a correction or a forgetting: a fact of a
// source the memory pass added, and for a correction the fact as it
// should read.
type MemoryEdit struct {
	SourceID string `json:"source_id"`
	Fact     string `json:"fact"`
	// Text is the corrected fact. Only a correction takes it.
	Text string `json:"text,omitempty"`
}

// MemoryChangeReply answers a correction or a forgetting.
type MemoryChangeReply struct {
	Change memory.Change `json:"change"`
	// ProfileChanged says a line of the profile said the fact, and was taken
	// out or corrected too.
	ProfileChanged bool `json:"profile_changed"`
	// Note says, for a person, what was done.
	Note string `json:"note"`
}

// MemoryScreenRoutes returns the Memory screen's routes (P6-07), served by
// r.
func MemoryScreenRoutes(r *Runner) []api.Route {
	const base = api.Prefix + "memory"
	return []api.Route{
		{
			Method: "GET", Path: base, Handle: r.memoryOverview,
			Summary: "What the person's knowledge store remembers: the store and what it can do, the profile, the map of the store, and each space with the facts the memory pass added to it, where each came from, and its pages.",
			Reply:   MemoryOverview{},
		},
		{
			Method: "POST", Path: base + "/forget", Handle: r.memoryForget,
			Summary: "Forget one fact the memory pass added. Its source is removed from the store, or withdrawn when the store keeps every source, and the source's other facts are added back as a new source. A profile line that says the fact goes too.",
			Body:    MemoryEdit{}, Reply: MemoryChangeReply{},
		},
		{
			Method: "POST", Path: base + "/correct", Handle: r.memoryCorrect,
			Summary: "Correct one fact the memory pass added: its source is replaced by one with the fact as given in text. A profile line that says the fact is corrected too.",
			Body:    MemoryEdit{}, Reply: MemoryChangeReply{},
		},
	}
}

// storeNote says what forgetting does in a store with these capabilities.
func storeNote(adapter string, caps knowledge.Capabilities) string {
	switch {
	case adapter == "" || adapter == knowledge.None:
		return "You have no knowledge store, so nothing is remembered between chats. Choose one in Settings, under knowledge.adapter."
	case caps.RemoveSource:
		return "Forgetting a fact takes it out of your knowledge store for good. The other facts from the same reply are kept, as a new source."
	case caps.AddSource:
		return "Your knowledge store keeps every source unchanged, so forgetting a fact withdraws its source: Keystone never reads or finds it again, and keeps the other facts from the same reply as a new source. The store itself still holds the withdrawn source until its owner erases it there."
	default:
		return "Your knowledge store keeps every source unchanged and takes no new ones, so Keystone can only withdraw a source whole: it never reads or finds it again. The store still holds it until its owner erases it there."
	}
}

// bounded reads a whole-number setting within bounds, or gives the
// fallback and a line in the log.
func (r *Runner) bounded(ctx context.Context, s *tenant.Store, key string, least, most, fallback int) int {
	n, err := strconv.Atoi(r.setting(ctx, s, key))
	if err != nil || n < least || n > most {
		r.logf("turns: tenant %s: the setting %s does not read as a number from %d to %d, so %d is used", s.TenantID(), key, least, most, fallback)
		return fallback
	}
	return n
}

func (r *Runner) memoryOverview(w http.ResponseWriter, req *http.Request, c api.Call) {
	if err := r.setup(); err != nil {
		r.answer(w, req, c, err)
		return
	}
	ctx, s := req.Context(), c.Store
	name := r.setting(ctx, s, knowledge.SettingAdapter)
	out := MemoryOverview{Store: MemoryStore{Adapter: name}, Spaces: []MemorySpace{}}

	profile, err := personmap.ReadProfile(ctx, s)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	out.Profile = profile.Text
	_, out.Map = r.person(ctx, s, row{}, true)

	passes, total, err := memory.Remembered(ctx, s, r.bounded(ctx, s, KeyMemoryScreenMemories, 1, 5000, 200))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	out.More = total - len(passes)

	spaces := map[string]*MemorySpace{}
	space := func(name string) *MemorySpace {
		sp := spaces[name]
		if sp == nil {
			sp = &MemorySpace{Name: name, Memories: []Remembered{}, Pages: []MemoryPage{}}
			spaces[name] = sp
		}
		return sp
	}
	var caps knowledge.Capabilities
	if name != "" && name != knowledge.None {
		ks, err := knowledge.Open(ctx, name, s)
		if err != nil {
			r.logf("turns: tenant %s: the Memory screen: the knowledge store did not open: %v", s.TenantID(), err)
			out.Store.Unavailable = "The knowledge store did not open. The reason is in the server's log."
		} else {
			defer ks.Close()
			caps = ks.Capabilities()
			entries, err := ks.Catalog(ctx)
			if err != nil {
				r.logf("turns: tenant %s: the Memory screen: reading the catalog: %v", s.TenantID(), err)
				out.Store.Unavailable = "The knowledge store's catalog could not be read. The reason is in the server's log."
			}
			most := r.bounded(ctx, s, KeyMemoryScreenPages, 0, 1000, 50)
			for _, e := range entries {
				sp := space(e.Space)
				// A label that is not normal reads as sensitive (fixed rule 3).
				sp.Sensitive = sp.Sensitive || e.Label.Sensitive()
				if len(sp.Pages) < most {
					sp.Pages = append(sp.Pages, MemoryPage{ID: e.ID, Path: e.Path, Title: e.Title, Kind: e.Kind})
				} else {
					sp.MorePages++
				}
			}
		}
	}
	out.Store.CanRemove, out.Store.CanAdd = caps.RemoveSource, caps.AddSource
	out.Store.Note = storeNote(name, caps)

	titles := map[string]*chat.Chat{}
	for _, p := range passes {
		sp := space(p.Space)
		sp.Sensitive = sp.Sensitive || p.Sensitive
		m := Remembered{SourceID: p.SourceID, ChatID: p.ChatID, TurnID: p.TurnID, Facts: p.Facts, Added: p.Created}
		if _, seen := titles[p.ChatID]; !seen {
			if ch, err := chat.Get(s, p.ChatID); err == nil {
				titles[p.ChatID] = &ch
			} else {
				titles[p.ChatID] = nil
			}
		}
		if ch := titles[p.ChatID]; ch != nil {
			m.ChatTitle = ch.Title
		} else {
			m.ChatGone = true
		}
		sp.Memories = append(sp.Memories, m)
	}
	for _, sp := range spaces {
		out.Spaces = append(out.Spaces, *sp)
	}
	sort.Slice(out.Spaces, func(i, j int) bool { return out.Spaces[i].Name < out.Spaces[j].Name })
	api.WriteJSON(w, http.StatusOK, out)
}

func (r *Runner) memoryForget(w http.ResponseWriter, req *http.Request, c api.Call) {
	var body MemoryEdit
	if !api.ReadJSON(w, req, &body) {
		return
	}
	if strings.TrimSpace(body.Text) != "" {
		refused(w, req, "forgetting takes no text: to change a fact, correct it")
		return
	}
	body.Text = ""
	r.memoryChange(w, req, c, body)
}

func (r *Runner) memoryCorrect(w http.ResponseWriter, req *http.Request, c api.Call) {
	var body MemoryEdit
	if !api.ReadJSON(w, req, &body) {
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		refused(w, req, "a correction needs text: the fact as it should read")
		return
	}
	r.memoryChange(w, req, c, body)
}

// memoryChange makes one correction or forgetting, then has the profile
// follow it.
func (r *Runner) memoryChange(w http.ResponseWriter, req *http.Request, c api.Call, body MemoryEdit) {
	if err := r.setup(); err != nil {
		r.answer(w, req, c, err)
		return
	}
	if strings.TrimSpace(body.SourceID) == "" || strings.TrimSpace(body.Fact) == "" {
		refused(w, req, "give source_id and fact: the source the memory pass added, and the fact as it holds it")
		return
	}
	ctx, s := req.Context(), c.Store
	name := r.setting(ctx, s, knowledge.SettingAdapter)
	if name == "" || name == knowledge.None {
		api.Error(w, req, http.StatusConflict, "no_store", "there is no knowledge store, so nothing is remembered to change")
		return
	}
	ks, err := knowledge.Open(ctx, name, s)
	if err != nil {
		r.logf("%s %s %s: the knowledge store did not open: %v", c.RequestID, req.Method, req.URL.Path, err)
		api.Error(w, req, http.StatusServiceUnavailable, "unavailable", "the knowledge store did not open")
		return
	}
	defer ks.Close()
	maxChars := r.bounded(ctx, s, memory.SettingFactMaxChars, 20, 2000, 300)
	e := memory.Edit{SourceID: strings.TrimSpace(body.SourceID), Fact: body.Fact, Text: body.Text}
	ch, err := memory.Apply(ctx, s, ks, name, e, maxChars)
	switch {
	case err == nil:
	case errors.Is(err, memory.ErrNotAdded), errors.Is(err, memory.ErrGone):
		api.Error(w, req, http.StatusNotFound, "not_found", "there is no such source among those the memory pass added; read the Memory screen again")
		return
	case errors.Is(err, memory.ErrNoFact):
		api.Error(w, req, http.StatusNotFound, "not_found", "the source holds no such fact; read the Memory screen again")
		return
	case errors.Is(err, knowledge.ErrInvalid):
		refused(w, req, err.Error())
		return
	case errors.Is(err, memory.ErrCannotChange), errors.Is(err, knowledge.ErrNotSupported):
		api.Error(w, req, http.StatusConflict, "cannot_change", "the knowledge store cannot take the facts that would be kept, so nothing was changed")
		return
	case errors.Is(err, memory.ErrLanded):
		r.logf("%s %s %s: %v", c.RequestID, req.Method, req.URL.Path, err)
		api.Error(w, req, http.StatusConflict, "landed", "the knowledge store put the facts of a sensitive space in an ordinary one, so the change was taken back")
		return
	case errors.Is(err, knowledge.ErrUnavailable):
		r.logf("%s %s %s: %v", c.RequestID, req.Method, req.URL.Path, err)
		api.Error(w, req, http.StatusServiceUnavailable, "unavailable", "the knowledge store could not be reached, so nothing was changed")
		return
	default:
		r.answer(w, req, c, err)
		return
	}

	reply := MemoryChangeReply{Change: ch}
	if p, err := personmap.ReadProfile(ctx, s); err != nil {
		r.logf("%s %s %s: reading the profile, so it was not looked through: %v", c.RequestID, req.Method, req.URL.Path, err)
	} else if text, changed := memory.ProfileEdited(p.Text, e); changed {
		most := r.bounded(ctx, s, personmap.SettingProfileMaxChars, 1, personmap.MaxProfileChars, 2000)
		if _, err := personmap.SetProfile(ctx, s, text, most); err != nil {
			r.logf("%s %s %s: the profile could not follow the change: %v", c.RequestID, req.Method, req.URL.Path, err)
		} else {
			reply.ProfileChanged = true
		}
	}
	switch {
	case body.Text != "" && ch.Removed:
		reply.Note = "Corrected. The fact as it was is gone from your knowledge store."
	case body.Text != "":
		reply.Note = "Corrected. Keystone will not read the fact as it was again; your store still holds its old source until its owner erases it there."
	case ch.Removed:
		reply.Note = "Forgotten. The fact is gone from your knowledge store."
	default:
		reply.Note = "Forgotten. Keystone will not read or find it again; your store still holds its old source until its owner erases it there."
	}
	if reply.ProfileChanged {
		reply.Note += " Your profile said it too, and now follows."
	}
	api.WriteJSON(w, http.StatusOK, reply)
}
