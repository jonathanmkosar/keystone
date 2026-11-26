package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// answer writes the error a chat record or turn function gave as the
// API's error body: not found, refused, or failed.
func (r *Runner) answer(w http.ResponseWriter, req *http.Request, c api.Call, err error) {
	switch {
	case errors.Is(err, chat.ErrNotFound), errors.Is(err, errNoTurn):
		api.Error(w, req, http.StatusNotFound, "not_found", "there is no such chat, message or turn")
	case errors.Is(err, chat.ErrInvalid):
		api.Error(w, req, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, errQueueFull):
		api.Error(w, req, http.StatusTooManyRequests, "queue_full", "this chat has as many turns waiting as it may")
	default:
		r.logf("%s %s %s: %v", c.RequestID, req.Method, req.URL.Path, err)
		api.Error(w, req, http.StatusInternalServerError, "failed", "the call failed")
	}
}

func refused(w http.ResponseWriter, req *http.Request, why string) {
	api.Error(w, req, http.StatusBadRequest, "bad_request", why)
}

func shown(c chat.Chat, busy bool) Chat {
	return Chat{ID: c.ID, Title: c.Title, Starred: c.Starred, Created: c.Created, Active: c.Active, Busy: busy}
}

// whole builds a chat's reply: the chat, its current branch, its model and
// its open turns.
func (r *Runner) whole(ctx context.Context, s *tenant.Store, c chat.Chat) (ChatReply, error) {
	path, err := chat.Path(s, c.ID)
	if err != nil {
		return ChatReply{}, err
	}
	metas, err := metaOf(ctx, s, c.ID)
	if err != nil {
		return ChatReply{}, err
	}
	open, err := openTurns(ctx, s, c.ID)
	if err != nil {
		return ChatReply{}, err
	}
	model, err := r.choice(ctx, s, c.ID)
	if err != nil {
		return ChatReply{}, err
	}
	out := ChatReply{Chat: shown(c, len(open) > 0), Messages: []Message{}, Model: model, Turns: []Turn{}}
	for _, m := range path {
		out.Messages = append(out.Messages, withMeta(m, metas[m.ID]))
	}
	for _, t := range open {
		out.Turns = append(out.Turns, t.Turn)
	}
	return out, nil
}

func (r *Runner) list(w http.ResponseWriter, req *http.Request, c api.Call) {
	q := req.URL.Query()
	most := r.o.PageLimit
	if most < 1 {
		most = 1
	}
	limit, offset := most, 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			refused(w, req, "limit is a whole number, 1 or more")
			return
		}
		limit = min(n, most)
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			refused(w, req, "offset is a whole number, 0 or more")
			return
		}
		offset = n
	}
	// One more than asked for is read, to know whether there are more.
	chats, err := chat.List(c.Store, chat.Filter{Starred: q.Get("starred") == "true", Limit: limit + 1, Offset: offset})
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	busy, err := busyChats(req.Context(), c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply := ListReply{Chats: []Chat{}, More: len(chats) > limit}
	if reply.More {
		chats = chats[:limit]
	}
	for _, ch := range chats {
		reply.Chats = append(reply.Chats, shown(ch, busy[ch.ID]))
	}
	r.withSummaries(req.Context(), c.Store, reply.Chats) // each chat's summary (P5-05, recap.go)
	api.WriteJSON(w, http.StatusOK, reply)
}

func (r *Runner) create(w http.ResponseWriter, req *http.Request, c api.Call) {
	made, err := chat.Create(c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply, err := r.whole(req.Context(), c.Store, made)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, reply)
}

func (r *Runner) search(w http.ResponseWriter, req *http.Request, c api.Call) {
	query := strings.TrimSpace(req.URL.Query().Get("q"))
	reply := SearchReply{Query: query, Hits: []chat.Hit{}}
	if query != "" {
		hits, err := chat.Search(c.Store, r.o.Config.Limits, query)
		if err != nil {
			r.answer(w, req, c, err)
			return
		}
		reply.Hits = append(reply.Hits, hits...)
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

func (r *Runner) read(w http.ResponseWriter, req *http.Request, c api.Call) {
	got, err := chat.Get(c.Store, req.PathValue("id"))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply, err := r.whole(req.Context(), c.Store, got)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

func (r *Runner) patch(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	got, err := chat.Get(c.Store, id)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	var body PatchBody
	if !api.ReadJSON(w, req, &body) {
		return
	}
	if body.Title == nil && body.Starred == nil {
		refused(w, req, "give a title, starred, or both")
		return
	}
	if body.Title != nil {
		if got, err = chat.Rename(c.Store, r.o.Config.Limits, id, *body.Title); err != nil {
			r.answer(w, req, c, err)
			return
		}
	}
	if body.Starred != nil {
		if got, err = chat.Star(c.Store, id, *body.Starred); err != nil {
			r.answer(w, req, c, err)
			return
		}
	}
	busy, err := busyChats(req.Context(), c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, shown(got, busy[id]))
}

func (r *Runner) remove(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	// A turn that is running would go on writing to a chat that is gone.
	open, err := openTurns(req.Context(), c.Store, id)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	for i := len(open) - 1; i >= 0; i-- {
		if _, err := r.stopTurn(req.Context(), c.Store, id, open[i].ID); err != nil && !errors.Is(err, errNoTurn) {
			r.answer(w, req, c, err)
			return
		}
	}
	if err := chat.Delete(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, DeletedReply{Deleted: true})
}

func (r *Runner) selectBranch(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	got, err := chat.Get(c.Store, id)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	var body SelectBody
	if !api.ReadJSON(w, req, &body) {
		return
	}
	if body.MessageID < 1 {
		refused(w, req, "message_id is the id of a message in the chat")
		return
	}
	if err := chat.Select(c.Store, id, body.MessageID); err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply, err := r.whole(req.Context(), c.Store, got)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

func (r *Runner) model(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	choice, err := r.choice(req.Context(), c.Store, id)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, choice)
}

// checkSend refuses a body no turn can be made from, before anything is
// written or streamed.
func checkSend(s *tenant.Store, chatID string, body *SendBody) error {
	invalid := func(why string) error { return fmt.Errorf("%w: %s", chat.ErrInvalid, why) }
	body.Text = strings.TrimSpace(body.Text)
	switch {
	case body.EditOf < 0 || body.RetryOf < 0:
		return invalid("edit_of and retry_of are message ids")
	case body.EditOf != 0 && body.RetryOf != 0:
		return invalid("give edit_of or retry_of, not both")
	case body.Text != "" && len(body.Blocks) > 0:
		return invalid("give text or blocks, not both")
	case body.RetryOf != 0 && (body.Text != "" || len(body.Blocks) > 0):
		return invalid("a retry sends no message: leave out text and blocks")
	case body.RetryOf == 0 && body.Text == "" && len(body.Blocks) == 0:
		return invalid("the message is empty")
	}
	for _, b := range body.Blocks {
		if b.Type != seam.BlockText && b.Type != seam.BlockImage && b.Type != seam.BlockFile {
			return invalid("a message holds text, image and file blocks")
		}
	}
	if err := uploadedBlocks(s, chatID, body); err != nil { // P9-02: a ref names an upload of the chat (uploads.go)
		return err
	}
	if body.RetryOf == 0 {
		if err := seam.ValidateMessage(seam.Message{Role: seam.RoleUser, Blocks: body.content()}); err != nil {
			return invalid(err.Error())
		}
	}
	if id := body.EditOf + body.RetryOf; id != 0 {
		old, err := chat.GetMessage(s, chatID, id)
		if err != nil {
			return err
		}
		switch {
		case body.EditOf != 0 && old.Role != seam.RoleUser:
			return invalid("edit_of names a message the person did not write")
		case body.RetryOf != 0 && (old.Role != seam.RoleAssistant || old.ParentID == 0):
			return invalid("retry_of names a message that is not a reply")
		}
		for _, b := range old.Blocks {
			if body.EditOf != 0 && b.Type == seam.BlockToolResult {
				return invalid("edit_of names a tool's results, not a message the person wrote")
			}
		}
	}
	return nil
}

func (r *Runner) send(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	var body SendBody
	if !api.ReadJSON(w, req, &body) {
		return
	}
	if err := checkSend(c.Store, id, &body); err != nil {
		r.answer(w, req, c, err)
		return
	}
	if r.o.Install == nil {
		r.answer(w, req, c, errors.New("the runner has no data folder"))
		return
	}
	t, err := enqueue(req.Context(), c.Store, id, c.RequestID, api.ActorPerson, body, r.o.Config.QueueMax)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	r.kick(c.Store.TenantID(), id)
	w.Header().Set("X-Turn-Id", t.ID)
	r.stream(w, req, c, t.ID, 0)
}

func (r *Runner) turn(w http.ResponseWriter, req *http.Request, c api.Call) {
	t, err := getTurn(req.Context(), c.Store, req.PathValue("id"), req.PathValue("turn"))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, t.Turn)
}

func (r *Runner) events(w http.ResponseWriter, req *http.Request, c api.Call) {
	t, err := getTurn(req.Context(), c.Store, req.PathValue("id"), req.PathValue("turn"))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	last := api.LastEventID(req)
	if last == "" {
		last = req.URL.Query().Get("after")
	}
	var after int64
	if last != "" {
		if after, err = strconv.ParseInt(last, 10, 64); err != nil || after < 0 {
			refused(w, req, "Last-Event-ID and after are the id of an event of this turn, a whole number")
			return
		}
	}
	w.Header().Set("X-Turn-Id", t.ID)
	r.stream(w, req, c, t.ID, after)
}

// stream sends a turn's events after the one with the id after, as they
// are written, until the end event or until the client goes. The events
// are read from the database, so it does not matter which call, or which
// start of the server, wrote them.
func (r *Runner) stream(w http.ResponseWriter, req *http.Request, c api.Call, turnID string, after int64) {
	s, err := api.OpenStream(w, req)
	if err != nil {
		return
	}
	defer s.Close()
	ctx := req.Context()
	key := c.Store.TenantID() + "/" + turnID
	for {
		events, err := eventsAfter(ctx, c.Store, turnID, after, r.o.Config.StreamBatch)
		if err != nil {
			if ctx.Err() == nil {
				r.logf("%s reading the events of turn %s: %v", c.RequestID, turnID, err)
			}
			return
		}
		for _, e := range events {
			if s.Send(api.Event{ID: strconv.FormatInt(e.seq, 10), Name: e.name, Data: json.RawMessage(e.data)}) != nil {
				return
			}
			after = e.seq
			if e.name == EventEnd {
				return
			}
		}
		if len(events) > 0 {
			continue
		}
		// Ask to be woken, then look once more: an event written between
		// the read above and the asking would otherwise be missed.
		news := r.waitFor(key)
		var newest int64
		if err := c.Store.DB().QueryRowContext(ctx, "SELECT last_event FROM turns WHERE id = ?", turnID).Scan(&newest); err != nil {
			return // the turn went with its chat, or the client left
		}
		if newest > after {
			continue
		}
		select {
		case <-news:
		case <-time.After(r.o.Config.StreamPoll):
		case <-ctx.Done():
			return
		}
	}
}

func (r *Runner) stop(w http.ResponseWriter, req *http.Request, c api.Call) {
	t, err := r.stopTurn(req.Context(), c.Store, req.PathValue("id"), req.PathValue("turn"))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, t.Turn)
}

func ended(t row) bool { return t.Status != StatusQueued && t.Status != StatusRunning }

// stopTurn stops a turn and returns it as it then stands. A turn that
// waits is closed at once. A turn that runs has its model call cancelled,
// and the goroutine that runs it stores what had streamed and closes it;
// this waits for that, up to chats.stop_wait. A turn that has ended is
// returned as it is.
func (r *Runner) stopTurn(ctx context.Context, s *tenant.Store, chatID, turnID string) (row, error) {
	t, err := getTurn(ctx, s, chatID, turnID)
	if err != nil || ended(t) {
		return t, err
	}
	key := s.TenantID() + "/" + turnID
	// The request is written down first, so a turn that is just starting
	// finds it.
	if _, err := s.DB().ExecContext(ctx, "UPDATE turns SET stop_requested = 1 WHERE id = ?", turnID); err != nil {
		return row{}, err
	}
	if t.Status == StatusQueued {
		closed, err := finishQueued(s, turnID)
		if err != nil {
			return row{}, err
		}
		if closed {
			r.wake(key)
			return getTurn(ctx, s, chatID, turnID)
		}
	}
	deadline := time.Now().Add(r.o.Config.StopWait)
	for {
		r.mu.Lock()
		cancel := r.live[key]
		r.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		news := r.waitFor(key)
		if t, err = getTurn(ctx, s, chatID, turnID); err != nil || ended(t) {
			return t, err
		}
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		select {
		case <-news:
		case <-time.After(min(left, r.o.Config.StreamPoll)):
		case <-ctx.Done():
			return t, nil
		}
	}
	// Nothing in this server is running it: it is closed here.
	r.mu.Lock()
	_, running := r.live[key]
	r.mu.Unlock()
	if !running {
		if _, err := finish(s, turnID, StatusStopped, nil); err != nil {
			return row{}, err
		}
		r.wake(key)
	}
	return getTurn(ctx, s, chatID, turnID)
}
