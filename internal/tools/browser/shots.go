package browser

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Screenshots are kept in the person's own database, in browser_shots, one
// row for each, and go with their chat. The result of the call that took
// one names it on a line of its own, "Screenshot: <id>", and the chat's
// step shows it from the route below.

// ShotLine starts the line of a result that names its screenshot.
const ShotLine = "Screenshot: "

var shotID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// saveShot keeps one screenshot and returns its id.
func saveShot(ctx context.Context, s *tenant.Store, chatID, callID, pageURL, title string, data []byte) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	_, err := s.DB().ExecContext(ctx, `INSERT INTO browser_shots(id, chat_id, call_id, url, title, media_type, data, created_at) VALUES(?, ?, ?, ?, ?, 'image/jpeg', ?, ?)`,
		id, chatID, callID, pageURL, title, data, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	return id, err
}

// Routes are the browser's routes: one screenshot, as an image.
func Routes() []api.Route {
	return []api.Route{
		{Method: "GET", Path: api.Prefix + "browser/shots/{shot}", Handle: serveShot,
			Summary: "One screenshot the agent's browser took (P10-03), as a JPEG. Only the person whose chat it is in can read it."},
	}
}

func serveShot(w http.ResponseWriter, r *http.Request, c api.Call) {
	id := r.PathValue("shot")
	var data []byte
	err := sql.ErrNoRows
	if shotID.MatchString(id) {
		err = c.Store.DB().QueryRowContext(r.Context(), "SELECT data FROM browser_shots WHERE id = ?", id).Scan(&data)
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		api.Error(w, r, http.StatusNotFound, "not_found", "there is no screenshot of that id")
		return
	case err != nil:
		api.Error(w, r, http.StatusInternalServerError, "failed", "the screenshot could not be read")
		return
	}
	head := w.Header()
	head.Set("Content-Type", "image/jpeg")
	head.Set("Content-Length", strconv.Itoa(len(data)))
	head.Set("Cache-Control", "private, max-age=31536000, immutable")
	head.Set("X-Content-Type-Options", "nosniff")
	head.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
