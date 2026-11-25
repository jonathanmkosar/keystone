package turn

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
)

// The pin routes (P5-02). A pinned message is in every call of its chat's
// branch, in its turn or on its own when its turn is left out or
// summarised. The pins are the chat record's: chat.Pin and chat.Pins.

// PinsReply answers the pin routes: the chat's pinned messages.
type PinsReply struct {
	ChatID string `json:"chat_id"`
	// Pinned holds the ids of the pinned messages, the oldest first, on
	// every branch.
	Pinned []int64 `json:"pinned"`
}

func (r *Runner) pins(w http.ResponseWriter, req *http.Request, c api.Call) {
	r.pinsReply(w, req, c, req.PathValue("id"))
}

func (r *Runner) pin(w http.ResponseWriter, req *http.Request, c api.Call) {
	r.setPin(w, req, c, true)
}

func (r *Runner) unpin(w http.ResponseWriter, req *http.Request, c api.Call) {
	r.setPin(w, req, c, false)
}

func (r *Runner) setPin(w http.ResponseWriter, req *http.Request, c api.Call, pinned bool) {
	id := req.PathValue("id")
	n, err := strconv.ParseInt(req.PathValue("message"), 10, 64)
	if err != nil || n < 1 {
		r.answer(w, req, c, fmt.Errorf("message %q: %w", req.PathValue("message"), chat.ErrNotFound))
		return
	}
	if err := chat.Pin(c.Store, id, n, pinned); err != nil {
		r.answer(w, req, c, err)
		return
	}
	r.pinsReply(w, req, c, id)
}

func (r *Runner) pinsReply(w http.ResponseWriter, req *http.Request, c api.Call, chatID string) {
	pinned, err := chat.Pins(c.Store, chatID)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, PinsReply{ChatID: chatID, Pinned: pinned})
}
