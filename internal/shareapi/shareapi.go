// Package shareapi serves share links (P10-04): the routes a person makes,
// lists and turns off their links with, and the two door routes that show
// a link's chat to someone who has not signed in.
//
// The rules are internal/share's, which reads no setting. This package
// reads the share settings for the tenant a call is for, and passes them in.
//
// The doors are the only routes reachable without signing in that show a
// tenant's data. They are given no store by the API shell. Each finds its
// tenant from the link's token alone, with share.Find, reads no cookie and
// sets none, and writes every call that finds a link in that tenant's
// audit log, under the route's pattern and the link's id and never the
// token. A view that cannot be recorded is not served.
package shareapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/share"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/uploads"
	"github.com/jonathanmkosar/keystone/settings"
)

// The share settings, in settings/share.json.
const (
	KeyLifetime    = "share.link_lifetime"
	KeyMaxLinks    = "share.max_links"
	KeyImages      = "share.images"
	KeyArtifacts   = "share.artifacts"
	KeyDefaultView = "share.default_view"
)

// PagePrefix is where a link's page is, on Keystone's own address: the
// page at PagePrefix + token reads the link through the view door.
const PagePrefix = "/share/"

// The door routes' patterns. They are what the audit log records of a
// call, so the token never reaches it.
const (
	viewRoute  = "GET " + api.Prefix + "shared/{token}"
	imageRoute = "GET " + api.Prefix + "shared/{token}/images/{image}"
)

// ActorPrefix starts the actor of a call made through a link: the link's
// id follows it.
const ActorPrefix = "share:"

// Options is what the routes run with.
type Options struct {
	// Install is the data folder. The doors find a link's tenant in it from
	// the token alone.
	Install *tenant.Install
	// Log is the server's own log.
	Log *api.Log
}

// Config is the share settings in force for one tenant.
type Config struct {
	Lifetime    time.Duration `json:"link_lifetime"`
	MaxLinks    int           `json:"max_links"`
	Images      bool          `json:"images"`
	Artifacts   bool          `json:"artifacts"`
	DefaultView string        `json:"default_view"`
}

// ReadConfig reads the share settings in force for a tenant.
func ReadConfig(ctx context.Context, s *tenant.Store) (Config, error) {
	reg, err := settings.Registry()
	if err != nil {
		return Config{}, err
	}
	var c Config
	read := func(key string, parse func(string) error) {
		if err != nil {
			return
		}
		v, gerr := tenantsettings.Get(ctx, s, reg, key)
		if gerr != nil {
			err = gerr
			return
		}
		if perr := parse(strings.TrimSpace(v)); perr != nil {
			err = fmt.Errorf("%s: %w", key, perr)
		}
	}
	read(KeyLifetime, func(v string) (e error) { c.Lifetime, e = time.ParseDuration(v); return })
	read(KeyMaxLinks, func(v string) (e error) { c.MaxLinks, e = strconv.Atoi(v); return })
	read(KeyImages, func(v string) (e error) { c.Images, e = strconv.ParseBool(v); return })
	read(KeyArtifacts, func(v string) (e error) { c.Artifacts, e = strconv.ParseBool(v); return })
	read(KeyDefaultView, func(v string) error { c.DefaultView = v; return nil })
	if err != nil {
		return Config{}, err
	}
	switch {
	case c.Lifetime < 0:
		return Config{}, fmt.Errorf("%s cannot be negative", KeyLifetime)
	case c.MaxLinks < 0:
		return Config{}, fmt.Errorf("%s cannot be negative", KeyMaxLinks)
	case c.DefaultView != share.ViewLive && c.DefaultView != share.ViewSnapshot:
		return Config{}, fmt.Errorf("%s is %s or %s", KeyDefaultView, share.ViewLive, share.ViewSnapshot)
	}
	return c, nil
}

// The shapes the routes take and return.

// CreateBody is what POST chats/{id}/shares takes. It may be left out.
type CreateBody struct {
	// View is live or snapshot. Empty means the share.default_view setting.
	View string `json:"view,omitempty"`
}

// CreateReply answers POST chats/{id}/shares. The token is in it this once
// and is never kept: a link that is lost is turned off and made again.
type CreateReply struct {
	Link  share.Link `json:"link"`
	Token string     `json:"token"`
	// Path is the page that shows the link, on Keystone's own address.
	Path string `json:"path"`
}

// LinksReply answers the lists of links.
type LinksReply struct {
	Links []share.Link `json:"links"`
}

// LinkReply answers DELETE shares/{share}.
type LinkReply struct {
	Link share.Link `json:"link"`
}

// Routes returns the share routes: four for the signed-in person, and the
// two doors.
func Routes(o Options) []api.Route {
	h := handlers{o: o}
	return []api.Route{
		{Method: "POST", Path: api.Prefix + "chats/{id}/shares", Handle: h.create, Body: CreateBody{}, Reply: CreateReply{},
			Summary: "Make a read-only link to the chat that anyone who has it can open without signing in. The token is in the answer this once and is never kept. Refused for a chat held for content from a sensitive space, an incognito chat, an empty chat, and past share.max_links open links."},
		{Method: "GET", Path: api.Prefix + "chats/{id}/shares", Handle: h.chatLinks, Reply: LinksReply{},
			Summary: "The chat's share links, the newest first, with whether each opens."},
		{Method: "GET", Path: api.Prefix + "shares", Handle: h.allLinks, Reply: LinksReply{},
			Summary: "Every share link of the person, the newest first, with whether each opens."},
		{Method: "DELETE", Path: api.Prefix + "shares/{share}", Handle: h.revoke, Reply: LinkReply{},
			Summary: "Turn a share link off for good. It then answers 410 to anyone who opens it, and stays in the list."},
		{Method: "GET", Path: api.Prefix + "shared/{token}", Door: true, Open: h.view, Reply: share.View{},
			Summary: "What a share link shows, to anyone who has it, without signing in: one chat's title and messages, as words, images and names, and nothing else of the person's. 404 for a link that is not there, 410 for one turned off or expired, 403 for one whose chat is now held for sensitive content or incognito. Every call is in the audit log, without the token."},
		{Method: "GET", Path: api.Prefix + "shared/{token}/images/{image}", Door: true, Open: h.image,
			Summary: "An image of a message a share link shows, when the person lets images show, without signing in."},
	}
}

type handlers struct{ o Options }

// owner answers an error from internal/share to the link's own person.
func (h handlers) owner(w http.ResponseWriter, r *http.Request, c api.Call, err error) {
	switch {
	case errors.Is(err, share.ErrNotFound):
		api.Error(w, r, http.StatusNotFound, "not_found", "there is no such chat or link")
	case errors.Is(err, share.ErrHeld):
		api.Error(w, r, http.StatusConflict, "sensitive", "This chat holds content from a sensitive space, and such a chat is never shared.")
	case errors.Is(err, share.ErrIncognito):
		api.Error(w, r, http.StatusConflict, "incognito", "An incognito chat is never shared.")
	case errors.Is(err, share.ErrEmpty):
		api.Error(w, r, http.StatusConflict, "empty", "The chat has no messages yet, so there is nothing to share.")
	case errors.Is(err, share.ErrTooMany):
		api.Error(w, r, http.StatusConflict, "too_many", "You have as many open links as you may. Turn one off first. "+trimmed(err))
	case errors.Is(err, share.ErrInvalid):
		api.Error(w, r, http.StatusBadRequest, "bad_request", trimmed(err))
	default:
		h.o.Log.Printf("%s share: %v", c.RequestID, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "the call failed")
	}
}

// trimmed is an error's reason after its last package prefix.
func trimmed(err error) string {
	text := err.Error()
	if i := strings.LastIndex(text, ": "); i >= 0 {
		return strings.TrimSuffix(text[i+2:], ".") + "."
	}
	return text
}

func (h handlers) create(w http.ResponseWriter, r *http.Request, c api.Call) {
	var body CreateBody
	if !readOptional(w, r, &body) {
		return
	}
	cfg, err := ReadConfig(r.Context(), c.Store)
	if err != nil {
		h.owner(w, r, c, err)
		return
	}
	view := body.View
	if view == "" {
		view = cfg.DefaultView
	}
	token, link, err := share.Create(r.Context(), c.Store, r.PathValue("id"), share.Draft{View: view, Lifetime: cfg.Lifetime, MaxOpen: cfg.MaxLinks})
	if err != nil {
		h.owner(w, r, c, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, CreateReply{Link: link, Token: token, Path: PagePrefix + token})
}

func (h handlers) chatLinks(w http.ResponseWriter, r *http.Request, c api.Call) {
	h.list(w, r, c, r.PathValue("id"))
}

func (h handlers) allLinks(w http.ResponseWriter, r *http.Request, c api.Call) {
	h.list(w, r, c, "")
}

func (h handlers) list(w http.ResponseWriter, r *http.Request, c api.Call, chatID string) {
	links, err := share.List(r.Context(), c.Store, chatID)
	if err != nil {
		h.owner(w, r, c, err)
		return
	}
	if links == nil {
		links = []share.Link{}
	}
	api.WriteJSON(w, http.StatusOK, LinksReply{Links: links})
}

func (h handlers) revoke(w http.ResponseWriter, r *http.Request, c api.Call) {
	link, err := share.Revoke(r.Context(), c.Store, r.PathValue("share"))
	if err != nil {
		h.owner(w, r, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, LinkReply{Link: link})
}

// readOptional reads a body that may be left out.
func readOptional(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			api.Error(w, r, http.StatusRequestEntityTooLarge, "too_large", "the request body is too large")
		} else {
			api.Error(w, r, http.StatusBadRequest, "bad_request", "the request body could not be read")
		}
		return false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return api.ReadJSON(w, r, v)
}

// A door's call, as it goes.
type doorCall struct {
	h     handlers
	w     http.ResponseWriter
	r     *http.Request
	store *tenant.Store
	link  share.Link
	route string
	start time.Time
}

// open finds the link a door's token names. It answers 404 itself, with
// nothing in any tenant's log, for a token that finds none.
func (h handlers) open(w http.ResponseWriter, r *http.Request, route string) (*doorCall, bool) {
	id := audit.RequestID(r.Context())
	store, link, err := share.Find(h.o.Install, r.PathValue("token"))
	if err != nil {
		if errors.Is(err, share.ErrNotFound) {
			h.o.Log.Printf("%s share: a token found no link", id)
		} else {
			h.o.Log.Printf("%s share: finding a link failed: %v", id, err)
		}
		api.Error(w, r, http.StatusNotFound, "not_found", share.Reason(share.ErrNotFound))
		return nil, false
	}
	return &doorCall{h: h, w: w, r: r, store: store, link: link, route: route, start: time.Now()}, true
}

// record writes the call in the link's tenant's audit log: the route's
// pattern, the link's id as the actor, and how it went. It holds no token.
func (d *doorCall) record(status int) error {
	outcome := audit.OutcomeOK
	switch {
	case status >= 500:
		outcome = audit.OutcomeError
	case status >= 400:
		outcome = audit.OutcomeRefused
	}
	_, err := audit.Append(context.WithoutCancel(d.r.Context()), d.store, audit.Entry{
		Kind: audit.KindAPI, RequestID: audit.RequestID(d.r.Context()), Actor: ActorPrefix + d.link.ID, Action: d.route,
		Outcome: outcome, Status: status, DurationMS: time.Since(d.start).Milliseconds(),
	}, d.r.PathValue("token"))
	if err == nil {
		d.h.o.Log.Printf("%s share: link %s of tenant %s answered %d", audit.RequestID(d.r.Context()), d.link.ID, d.store.TenantID(), status)
	}
	return err
}

// refuse records a call that is refused and answers it. A refusal that
// cannot be recorded is still a refusal.
func (d *doorCall) refuse(status int, code, message string) {
	if err := d.record(status); err != nil {
		d.h.o.Log.Printf("%s share: a refusal could not be recorded: %v", audit.RequestID(d.r.Context()), err)
	}
	api.Error(d.w, d.r, status, code, message)
}

// fail records a call that failed inside Keystone and answers it.
func (d *doorCall) fail(err error) {
	d.h.o.Log.Printf("%s share: link %s of tenant %s: %v", audit.RequestID(d.r.Context()), d.link.ID, d.store.TenantID(), err)
	d.refuse(http.StatusInternalServerError, "failed", "the link could not be read")
}

// usable refuses a link that shows nothing now, and reads the settings for
// one that does.
func (d *doorCall) usable() (Config, bool) {
	if err := share.Usable(d.r.Context(), d.store, d.link); err != nil {
		switch {
		case errors.Is(err, share.ErrRevoked):
			d.refuse(http.StatusGone, "revoked", share.Reason(err))
		case errors.Is(err, share.ErrExpired):
			d.refuse(http.StatusGone, "expired", share.Reason(err))
		case errors.Is(err, share.ErrHeld):
			d.refuse(http.StatusForbidden, "sensitive", share.Reason(err))
		case errors.Is(err, share.ErrIncognito):
			d.refuse(http.StatusForbidden, "incognito", share.Reason(err))
		default:
			d.fail(err)
		}
		return Config{}, false
	}
	cfg, err := ReadConfig(d.r.Context(), d.store)
	if err != nil {
		d.fail(err)
		return Config{}, false
	}
	return cfg, true
}

// show is what a view holds beyond the words, by the person's settings.
func show(cfg Config, chatID string) share.Show {
	s := share.Show{Images: cfg.Images}
	if cfg.Artifacts {
		s.Artifact = func(ctx context.Context, st *tenant.Store, id string, version int) (*share.Artifact, error) {
			a, err := artifacts.Get(ctx, st, id)
			if errors.Is(err, artifacts.ErrNotFound) || (err == nil && a.ChatID != chatID) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			v, content, err := artifacts.Content(ctx, st, id, version)
			if errors.Is(err, artifacts.ErrNotFound) {
				return nil, nil // a version no longer kept: shown as its step
			}
			if err != nil {
				return nil, err
			}
			return &share.Artifact{Title: v.Title, Type: string(a.Type), Language: a.Language, Version: v.Version, Content: content}, nil
		}
	}
	return s
}

// view is the door that shows a link's chat.
func (h handlers) view(w http.ResponseWriter, r *http.Request) {
	d, ok := h.open(w, r, viewRoute)
	if !ok {
		return
	}
	defer d.store.Close()
	cfg, ok := d.usable()
	if !ok {
		return
	}
	v, err := share.Read(r.Context(), d.store, d.link, show(cfg, d.link.ChatID))
	if err != nil {
		d.fail(err)
		return
	}
	// A view that cannot be recorded is not served.
	if err := d.record(http.StatusOK); err != nil {
		h.o.Log.Printf("%s share: a view of link %s could not be recorded: %v", audit.RequestID(r.Context()), d.link.ID, err)
		api.Error(w, r, http.StatusInternalServerError, "not_recorded", "the call could not be recorded, so it was not run")
		return
	}
	if err := share.Seen(context.WithoutCancel(r.Context()), d.store, d.link.ID); err != nil {
		h.o.Log.Printf("%s share: counting a view of link %s: %v", audit.RequestID(r.Context()), d.link.ID, err)
	}
	api.WriteJSON(w, http.StatusOK, v)
}

// image is the door that serves an image of a message the link shows. Only
// an upload of the link's chat that one of the person's messages on the
// branch shows, and only one whose type was known by its bytes as an
// image, under a policy that allows nothing.
func (h handlers) image(w http.ResponseWriter, r *http.Request) {
	d, ok := h.open(w, r, imageRoute)
	if !ok {
		return
	}
	defer d.store.Close()
	cfg, ok := d.usable()
	if !ok {
		return
	}
	ref := r.PathValue("image")
	v, err := share.Read(r.Context(), d.store, d.link, share.Show{Images: cfg.Images})
	if err != nil {
		d.fail(err)
		return
	}
	if !v.HasImage(ref) {
		d.refuse(http.StatusNotFound, "not_found", "the link shows no such image")
		return
	}
	up, err := uploads.Get(r.Context(), d.store, d.link.ChatID, ref)
	if errors.Is(err, uploads.ErrNotFound) || (err == nil && up.Kind != uploads.KindImage) {
		d.refuse(http.StatusNotFound, "not_found", "the link shows no such image")
		return
	}
	if err != nil {
		d.fail(err)
		return
	}
	data, err := uploads.Read(d.store, up.ID)
	if errors.Is(err, uploads.ErrNotFound) {
		d.refuse(http.StatusNotFound, "not_found", "the link shows no such image")
		return
	}
	if err != nil {
		d.fail(err)
		return
	}
	if err := d.record(http.StatusOK); err != nil {
		h.o.Log.Printf("%s share: an image of link %s could not be recorded: %v", audit.RequestID(r.Context()), d.link.ID, err)
		api.Error(w, r, http.StatusInternalServerError, "not_recorded", "the call could not be recorded, so it was not run")
		return
	}
	head := w.Header()
	head.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	head.Set("Content-Type", up.MediaType)
	head.Set("Content-Length", strconv.Itoa(len(data)))
	if disposition := mime.FormatMediaType("inline", map[string]string{"filename": up.Name}); disposition != "" {
		head.Set("Content-Disposition", disposition)
	} else {
		head.Set("Content-Disposition", "inline")
	}
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
