package uploads

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/jonathanmkosar/keystone/internal/api"
)

// The upload routes. Each acts for the signed-in person on their own
// tenant, with the store the API shell hands it, so a file of one tenant
// is never served to another: another tenant's store has another files
// folder and another record.

// ListReply answers GET chats/{id}/uploads.
type ListReply struct {
	// Uploads are the chat's uploads, the oldest first.
	Uploads []Upload `json:"uploads"`
	// Notices are what the person is told about the chat's replies and the
	// uploads of the messages they answer, the oldest turn first.
	Notices []Notice `json:"notices"`
}

// OneReply answers GET chats/{id}/uploads/{upload}: the upload, and the
// text read from it.
type OneReply struct {
	Upload
	// Text is the text read from a text file, as a model is given it
	// before it is cut to uploads.text_max_chars. Empty for an image or a
	// document.
	Text string `json:"text"`
}

// AddReply answers POST chats/{id}/uploads.
type AddReply struct {
	Upload
	// Added is false when the chat already had the same file.
	Added bool `json:"added"`
}

// DoneReply answers DELETE chats/{id}/uploads/{upload}.
type DoneReply struct {
	Removed bool `json:"removed"`
}

// Routes returns the upload routes. The add route takes a body as large as
// the largest upload, which may be more than the API's own limit on a body.
func Routes(o Options) []api.Route {
	const (
		list = api.Prefix + "chats/{id}/uploads"
		one  = list + "/{upload}"
	)
	h := handlers{o: o}
	return []api.Route{
		{Method: "POST", Path: list, Handle: h.add, Reply: AddReply{}, MaxBodyBytes: o.MaxBytes,
			Query:   []api.Param{{Name: "name", Description: "The file's name. Its ending says what kind of text file it is."}},
			Summary: "Upload a file or an image to a chat. The body is the file's bytes. An image or a document is known by its bytes, a text file by its ending; the text of a text file is read. The file is kept under its SHA-256, which is the upload's id and the ref a message's block names it by."},
		{Method: "GET", Path: list, Handle: h.list, Reply: ListReply{},
			Summary: "A chat's uploads, and what the person is told about its replies: an image a reply did not see, and a reply a fallback wrote to see one."},
		{Method: "GET", Path: one, Handle: h.one, Reply: OneReply{},
			Summary: "One upload of a chat, with the text read from it."},
		{Method: "GET", Path: one + "/content", Handle: h.content,
			Summary: "An upload's bytes. An image is served as itself; any other file as a download."},
		{Method: "DELETE", Path: one, Handle: h.remove, Reply: DoneReply{},
			Summary: "Take an upload off a chat, and remove its file when no other chat holds it. Refused while a message of the chat holds it."},
		{Method: "GET", Path: api.Prefix + "uploads/limits", Handle: h.limits, Reply: Options{},
			Summary: "What may be uploaded: the largest size, the image and document types, and the endings of text files."},
	}
}

type handlers struct{ o Options }

// answer writes an error from this package as the API's error body.
func answer(w http.ResponseWriter, r *http.Request, err error) {
	var tooBig *http.MaxBytesError
	switch {
	case errors.Is(err, ErrNotFound):
		api.Error(w, r, http.StatusNotFound, "not_found", "there is no such chat or upload")
	case errors.As(err, &tooBig):
		api.Error(w, r, http.StatusRequestEntityTooLarge, "too_large", "the file is larger than the most one upload may be, "+Size(tooBig.Limit))
	case errors.Is(err, ErrRefused):
		api.Error(w, r, http.StatusBadRequest, "bad_upload", trimmed(err))
	case errors.Is(err, ErrInUse):
		api.Error(w, r, http.StatusConflict, "in_use", trimmed(err)+" is in a message of this chat, and goes when the chat is deleted")
	default:
		api.Error(w, r, http.StatusInternalServerError, "failed", "the call failed")
	}
}

// trimmed is an error's reason without this package's prefix.
func trimmed(err error) string {
	text := err.Error()
	for _, prefix := range []string{ErrRefused.Error() + ": ", ErrInUse.Error() + ": "} {
		if len(text) > len(prefix) && text[:len(prefix)] == prefix {
			return text[len(prefix):]
		}
	}
	return text
}

func (h handlers) add(w http.ResponseWriter, r *http.Request, c api.Call) {
	chatID := r.PathValue("id")
	if err := chatThere(c.Store, chatID); err != nil {
		answer(w, r, err)
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		answer(w, r, err)
		return
	}
	up, added, err := Add(r.Context(), c.Store, h.o, chatID, r.URL.Query().Get("name"), data)
	if err != nil {
		answer(w, r, err)
		return
	}
	status := http.StatusOK
	if added {
		status = http.StatusCreated
	}
	api.WriteJSON(w, status, AddReply{Upload: up, Added: added})
}

func (h handlers) list(w http.ResponseWriter, r *http.Request, c api.Call) {
	chatID := r.PathValue("id")
	ups, err := List(r.Context(), c.Store, chatID)
	if err != nil {
		answer(w, r, err)
		return
	}
	notices, err := Notices(r.Context(), c.Store, chatID)
	if err != nil {
		answer(w, r, err)
		return
	}
	if ups == nil {
		ups = []Upload{}
	}
	api.WriteJSON(w, http.StatusOK, ListReply{Uploads: ups, Notices: notices})
}

func (h handlers) one(w http.ResponseWriter, r *http.Request, c api.Call) {
	up, err := Get(r.Context(), c.Store, r.PathValue("id"), r.PathValue("upload"))
	if err != nil {
		answer(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, OneReply{Upload: up, Text: up.Text()})
}

// content serves an upload's bytes from Keystone's own address, which the
// page's content security policy allows for an image. Only an image is
// served as itself, and only one whose type was known by its bytes; every
// other file, an HTML page included, is served as a download that no
// browser runs, with a policy of its own that allows nothing.
func (h handlers) content(w http.ResponseWriter, r *http.Request, c api.Call) {
	up, err := Get(r.Context(), c.Store, r.PathValue("id"), r.PathValue("upload"))
	if err != nil {
		answer(w, r, err)
		return
	}
	data, err := Read(c.Store, up.ID)
	if err != nil {
		answer(w, r, err)
		return
	}
	head := w.Header()
	head.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	// The bytes of an id never change: the id is their hash.
	head.Set("Cache-Control", "private, max-age=31536000, immutable")
	head.Set("Content-Length", strconv.Itoa(len(data)))
	how := "attachment"
	head.Set("Content-Type", "application/octet-stream")
	if up.Kind == KindImage {
		how = "inline"
		head.Set("Content-Type", up.MediaType)
	}
	disposition := mime.FormatMediaType(how, map[string]string{"filename": up.Name})
	if disposition == "" {
		disposition = how // a name the header cannot carry is left out
	}
	head.Set("Content-Disposition", disposition)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (h handlers) remove(w http.ResponseWriter, r *http.Request, c api.Call) {
	if err := Remove(r.Context(), c.Store, r.PathValue("id"), r.PathValue("upload")); err != nil {
		answer(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, DoneReply{Removed: true})
}

func (h handlers) limits(w http.ResponseWriter, r *http.Request, c api.Call) {
	api.WriteJSON(w, http.StatusOK, h.o)
}
