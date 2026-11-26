package turn

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/styles"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
)

// This file joins styles (P9-05) to the API and to each model call. A
// style is a name and instructions on tone and format. A chat uses its own
// style, or the person's default, and every call of it carries the style's
// instructions in the standing start's style part, under the first cache
// point, so a chat that keeps its style keeps its start. The style is
// chosen after the call's route and changes nothing but those words: not
// where the call may go, not what it is given of the knowledge store, and
// not what a tool needs to run. internal/styles keeps the rows and reads no
// setting, so the settings are read here. docs/architecture/styles.md is
// the whole of it.

// StyleLimits are the limits a styles screen needs to know.
type StyleLimits struct {
	// MaxStyles is styles.max_styles: how many of their own a person may
	// have. The built-in styles are not counted.
	MaxStyles int `json:"max_styles"`
	// NameMaxChars is styles.name_max_chars.
	NameMaxChars int `json:"name_max_chars"`
	// InstructionsMaxChars is styles.instructions_max_chars.
	InstructionsMaxChars int `json:"instructions_max_chars"`
}

// StylesReply answers GET styles.
type StylesReply struct {
	// Styles are every style the person may choose: the built-in ones in
	// their order, then their own by name.
	Styles []styles.Style `json:"styles"`
	// Default is the id in styles.default: the style a new chat uses. It may
	// name no style, and then a new chat uses none.
	Default string      `json:"default"`
	Limits  StyleLimits `json:"limits"`
}

// StyleBody makes or changes a style of the person's own. A field that is
// absent is left as it is; making a style needs both.
type StyleBody struct {
	Name         *string `json:"name,omitempty"`
	Instructions *string `json:"instructions,omitempty"`
}

// ChatStyleReply says which style a chat uses.
type ChatStyleReply struct {
	ChatID string `json:"chat_id"`
	// Style is the style the chat's next call carries. Absent when it uses
	// none.
	Style *styles.Style `json:"style,omitempty"`
	// Source is where it comes from: chat (the chat's own), default (the
	// person's default, since the chat has had no turn and no style chosen)
	// or none.
	Source string `json:"source"`
	// Default is the id in styles.default.
	Default string `json:"default"`
	// Styles are every style the person may choose, as GET styles lists
	// them, so a picker needs one call.
	Styles []styles.Style `json:"styles"`
}

// ChatStyleBody chooses a chat's style.
type ChatStyleBody struct {
	// Style is the id of a built-in style or of one of the person's own.
	Style string `json:"style"`
}

// StyleRoutes returns the style routes (P9-05), served by r.
func StyleRoutes(r *Runner) []api.Route {
	const (
		list = api.Prefix + "styles"
		one  = list + "/{id}"
		mine = api.Prefix + "chats/{id}/style"
	)
	return []api.Route{
		{Method: "GET", Path: list, Handle: r.styleList, Reply: StylesReply{},
			Summary: "Every style the person may choose, built-in ones first, the default style for new chats, and the limits a styles screen needs."},
		{Method: "POST", Path: list, Handle: r.styleCreate, Body: StyleBody{}, Reply: styles.Style{},
			Summary: "Make a style of the person's own: a name and instructions on tone and format. A name another style has is refused (400); past styles.max_styles it is refused (409 too_many)."},
		{Method: "GET", Path: one, Handle: r.styleRead, Reply: styles.Style{},
			Summary: "One style, built-in or the person's own, with its instructions."},
		{Method: "PATCH", Path: one, Handle: r.stylePatch, Body: StyleBody{}, Reply: styles.Style{},
			Summary: "Rename one of the person's own styles or change its instructions. Every chat that uses it carries the new words from its next call. A built-in style cannot be changed (400)."},
		{Method: "DELETE", Path: one, Handle: r.styleDelete, Reply: DeletedReply{},
			Summary: "Delete one of the person's own styles. A chat that used it uses the person's default from its next turn, and a default that named it goes back to the shipped one. A built-in style cannot be deleted (400)."},
		{Method: "PUT", Path: one + "/default", Handle: r.styleDefault, Reply: StylesReply{},
			Summary: "Make a style the person's default: the style a new chat uses until another is chosen for it. Chats that have had a turn keep their own. It takes no body."},
		{Method: "GET", Path: mine, Handle: r.chatStyle, Reply: ChatStyleReply{},
			Summary: "The style a chat uses, where it comes from, and every style the person may choose."},
		{Method: "PUT", Path: mine, Handle: r.chatStylePut, Body: ChatStyleBody{}, Reply: ChatStyleReply{},
			Summary: "Choose a chat's style: a built-in style or one of the person's own. The chat's next call carries its instructions at the start; nothing else about the call changes."},
	}
}

// styleOptions reads what styles take from the person's settings.
func (r *Runner) styleOptions(ctx context.Context, s *tenant.Store) (styles.Options, error) {
	var bad []string
	whole := func(key string) int {
		n, err := strconv.Atoi(r.setting(ctx, s, key))
		if err != nil {
			bad = append(bad, key)
		}
		return n
	}
	o := styles.Options{
		MaxStyles:            whole(styles.SettingMaxStyles),
		NameMaxChars:         whole(styles.SettingNameMaxChars),
		InstructionsMaxChars: whole(styles.SettingInstructionsMaxChars),
	}
	if len(bad) > 0 {
		return styles.Options{}, errors.New("these settings do not read: " + strings.Join(bad, ", "))
	}
	var err error
	if o.BuiltIn, err = styles.ParseBuiltIn(r.setting(ctx, s, styles.SettingBuiltIn)); err != nil {
		return styles.Options{}, err
	}
	return o, styles.CheckOptions(o)
}

func (r *Runner) styleLimits(o styles.Options) StyleLimits {
	return StyleLimits{MaxStyles: o.MaxStyles, NameMaxChars: o.NameMaxChars, InstructionsMaxChars: o.InstructionsMaxChars}
}

// styleFailed answers an error of internal/styles, or of the chat record.
func (r *Runner) styleFailed(w http.ResponseWriter, req *http.Request, c api.Call, err error) {
	switch {
	case errors.Is(err, styles.ErrNotFound):
		api.Error(w, req, http.StatusNotFound, "not_found", "there is no such style or chat")
	case errors.Is(err, styles.ErrInvalid):
		refused(w, req, styles.Reason(err))
	case errors.Is(err, styles.ErrBuiltIn):
		refused(w, req, "a built-in style cannot be changed or deleted; make a style of your own")
	case errors.Is(err, styles.ErrFull):
		api.Error(w, req, http.StatusConflict, "too_many", styles.Reason(err))
	default:
		r.answer(w, req, c, err)
	}
}

// styleReady makes the runner ready and reads the style options, or
// answers why not.
func (r *Runner) styleReady(w http.ResponseWriter, req *http.Request, c api.Call) (styles.Options, bool) {
	if err := r.setup(); err != nil {
		r.answer(w, req, c, err)
		return styles.Options{}, false
	}
	o, err := r.styleOptions(req.Context(), c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return styles.Options{}, false
	}
	return o, true
}

// styleListAnswer writes every style with the default and the limits.
func (r *Runner) styleListAnswer(w http.ResponseWriter, req *http.Request, c api.Call, o styles.Options) {
	all, err := styles.List(req.Context(), c.Store, o)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, StylesReply{Styles: all, Default: r.setting(req.Context(), c.Store, styles.SettingDefault), Limits: r.styleLimits(o)})
}

func (r *Runner) styleList(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.styleReady(w, req, c)
	if !ok {
		return
	}
	r.styleListAnswer(w, req, c, o)
}

func (r *Runner) styleCreate(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.styleReady(w, req, c)
	if !ok {
		return
	}
	var body StyleBody
	if !api.ReadJSON(w, req, &body) {
		return
	}
	if body.Name == nil || body.Instructions == nil {
		refused(w, req, "a style needs a name and instructions")
		return
	}
	st, err := styles.Create(req.Context(), c.Store, o, *body.Name, *body.Instructions)
	if err != nil {
		r.styleFailed(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, st)
}

func (r *Runner) styleRead(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.styleReady(w, req, c)
	if !ok {
		return
	}
	st, err := styles.Get(req.Context(), c.Store, o, req.PathValue("id"))
	if err != nil {
		r.styleFailed(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, st)
}

func (r *Runner) stylePatch(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.styleReady(w, req, c)
	if !ok {
		return
	}
	id := req.PathValue("id")
	if _, err := styles.Get(req.Context(), c.Store, o, id); err != nil {
		r.styleFailed(w, req, c, err)
		return
	}
	var body StyleBody
	if !api.ReadJSON(w, req, &body) {
		return
	}
	if body.Name == nil && body.Instructions == nil {
		refused(w, req, "give a name or instructions")
		return
	}
	st, err := styles.Update(req.Context(), c.Store, o, id, styles.Change{Name: body.Name, Instructions: body.Instructions})
	if err != nil {
		r.styleFailed(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, st)
}

func (r *Runner) styleDelete(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.styleReady(w, req, c)
	if !ok {
		return
	}
	ctx, id := req.Context(), req.PathValue("id")
	if err := styles.Delete(ctx, c.Store, o, id); err != nil {
		r.styleFailed(w, req, c, err)
		return
	}
	// A default that named the style goes back to the shipped one.
	if r.setting(ctx, c.Store, styles.SettingDefault) == id {
		if err := tenantsettings.Clear(ctx, c.Store, r.reg, tenantsettings.Person, styles.SettingDefault); err != nil {
			r.logf("%s deleting style %s: putting the default style back: %v", c.RequestID, id, err)
		}
	}
	api.WriteJSON(w, http.StatusOK, DeletedReply{Deleted: true})
}

func (r *Runner) styleDefault(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.styleReady(w, req, c)
	if !ok {
		return
	}
	ctx, id := req.Context(), req.PathValue("id")
	if _, err := styles.Get(ctx, c.Store, o, id); err != nil {
		r.styleFailed(w, req, c, err)
		return
	}
	if err := tenantsettings.Set(ctx, c.Store, r.reg, tenantsettings.Person, styles.SettingDefault, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	r.styleListAnswer(w, req, c, o)
}

// chatStyleAnswer writes the style a chat uses.
func (r *Runner) chatStyleAnswer(w http.ResponseWriter, req *http.Request, c api.Call, o styles.Options, chatID string) {
	ctx := req.Context()
	def := r.setting(ctx, c.Store, styles.SettingDefault)
	st, source, err := styles.Resolve(ctx, c.Store, o, chatID, def)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	all, err := styles.List(ctx, c.Store, o)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply := ChatStyleReply{ChatID: chatID, Source: source, Default: def, Styles: all}
	if source != styles.SourceNone {
		reply.Style = &st
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

func (r *Runner) chatStyle(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.styleReady(w, req, c)
	if !ok {
		return
	}
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err) // another tenant's chat is not in this store: not found
		return
	}
	r.chatStyleAnswer(w, req, c, o, id)
}

func (r *Runner) chatStylePut(w http.ResponseWriter, req *http.Request, c api.Call) {
	o, ok := r.styleReady(w, req, c)
	if !ok {
		return
	}
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	var body ChatStyleBody
	if !api.ReadJSON(w, req, &body) {
		return
	}
	if strings.TrimSpace(body.Style) == "" {
		refused(w, req, "give the id of a style")
		return
	}
	if _, err := styles.Choose(req.Context(), c.Store, o, id, body.Style); err != nil {
		r.styleFailed(w, req, c, err)
		return
	}
	r.chatStyleAnswer(w, req, c, o, id)
}

// styleFor returns what one model call of a turn carries in the standing
// start's style part: the heading and the instructions of the style the
// chat uses, or nothing. When the style is the person's default, the chat
// keeps it as its own from this turn, so a later change of the default
// leaves the chat's start as it is. Nothing here stops a turn: a style
// that cannot be read sends none, with a line in the log.
func (r *Runner) styleFor(ctx context.Context, s *tenant.Store, t row) string {
	o, err := r.styleOptions(ctx, s)
	if err != nil {
		r.logf("turns: %s turn %s: the style settings do not read, so the call carries no style: %v", t.requestID, t.ID, err)
		return ""
	}
	st, source, err := styles.Resolve(ctx, s, o, t.ChatID, r.setting(ctx, s, styles.SettingDefault))
	if err != nil {
		r.logf("turns: %s turn %s: reading the chat's style, so the call carries none: %v", t.requestID, t.ID, err)
		return ""
	}
	if source == styles.SourceDefault {
		if _, err := styles.Keep(context.Background(), s, t.ChatID, st.ID); err != nil {
			r.logf("turns: %s turn %s: keeping the default style %s as the chat's own: %v", t.requestID, t.ID, st.ID, err)
		}
	}
	return styles.Text(r.setting(ctx, s, styles.SettingHeading), st)
}
