package turn

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/uploads"
)

// This file joins uploads (P9-02) to a turn. The store, the text read from
// a file and how a branch is readied for a call are in internal/uploads;
// what is here decides the route by the turn's images, readies each call
// for the models its route can reach, and keeps the record.
//
// The rule: an image never reaches a model that cannot see. A turn whose
// message holds an image asks its route for vision, so it goes to the
// chat's model if that model can see, and otherwise to the first model of
// the fallback chain that can. When none can, it goes as it would have,
// and every call is readied for the least of its route: the image is left
// out, with a note in its place and a notice to the person. The adapters
// refuse an image to a model without vision as well (decision 0383).

// turnMedia is what a turn's images decide about its route.
type turnMedia struct {
	// current says the turn's own message holds an image.
	current bool
	// earlier says an earlier message of the turn's branch does.
	earlier bool
	// moved says the chat's model cannot see the turn's image, so the
	// route begins at a fallback that can. picked is the chat's model.
	moved  bool
	picked string
}

// mediaOf reads which messages of a turn's branch hold an image: the
// message the turn sends, or for a retry the message it answers again, and
// the messages before it. A branch that cannot be read holds none here;
// the calls are readied for their route all the same.
func (r *Runner) mediaOf(s *tenant.Store, t row) *turnMedia {
	tm := &turnMedia{}
	asked := t.input.content()
	var at int64 // the message the turn's own message takes the place of, or is
	switch {
	case t.input.RetryOf != 0:
		old, err := chat.GetMessage(s, t.ChatID, t.input.RetryOf)
		if err != nil || old.ParentID == 0 {
			return tm
		}
		was, err := chat.GetMessage(s, t.ChatID, old.ParentID)
		if err != nil {
			return tm
		}
		asked, at = was.Blocks, was.ID
	case t.input.EditOf != 0:
		at = t.input.EditOf
	}
	tm.current = holdsImage(asked)
	path, err := chat.Path(s, t.ChatID)
	if err != nil {
		return tm
	}
	for _, m := range path {
		if m.ID == at {
			break
		}
		if holdsImage(m.Blocks) {
			tm.earlier = true
			break
		}
	}
	return tm
}

func holdsImage(blocks []seam.Block) bool {
	return seam.Images(seam.Request{Messages: []seam.Message{{Blocks: blocks}}}) > 0
}

// planRoute is routing.Plan for a turn, with the turn's images. A chat
// turn asks for vision when its own message holds an image, or when an
// earlier one does and the chat's model can see, so a model that cannot
// see does not take a later question about the picture from one that can.
// When no model can take the call with vision, it is planned without, and
// its calls leave the images out.
func (r *Runner) planRoute(rules routing.Rules, ask routing.Ask, usable []models.Model, t row) (routing.Route, error) {
	if ask.Kind != routing.KindChat || t.media == nil {
		return routing.Plan(rules, ask, usable)
	}
	var picked models.Model
	found := false
	for _, m := range usable {
		if m.Name == ask.Model {
			picked, found = m, true
		}
	}
	if !t.media.current && !(t.media.earlier && found && picked.Has(models.CapVision)) {
		return routing.Plan(rules, ask, usable)
	}
	seeing := ask
	seeing.Needs = append(append([]models.Capability(nil), ask.Needs...), models.CapVision)
	route, err := routing.Plan(rules, seeing, usable)
	if err != nil {
		return routing.Plan(rules, ask, usable)
	}
	if t.media.current && found && !picked.Has(models.CapVision) && route.Models[0].Name != picked.Name {
		t.media.moved, t.media.picked = true, picked.Name
	}
	return route, nil
}

// chatModel is the model a turn writes on its row, which the chat's next
// turn takes as the chat's own: the person's model when the route moved
// off it only to see this turn's image, and otherwise the route's.
func (tm *turnMedia) chatModel(route string) string {
	if tm != nil && tm.moved {
		return tm.picked
	}
	return route
}

// uploadedBlocks checks the image and file blocks of a message as it is
// sent: a block that names a file must name an upload of the chat, and is
// written as that upload says. See uploads.Blocks.
func uploadedBlocks(s *tenant.Store, chatID string, body *SendBody) error {
	if len(body.Blocks) == 0 {
		return nil
	}
	blocks, err := uploads.Blocks(context.Background(), s, chatID, body.Blocks)
	if errors.Is(err, uploads.ErrRefused) {
		return fmt.Errorf("%w: %s", chat.ErrInvalid, strings.TrimPrefix(err.Error(), uploads.ErrRefused.Error()+": "))
	}
	if err != nil {
		return err
	}
	body.Blocks = blocks
	return nil
}

// everyHas is true only when each model one call may reach has the
// capability: the call is built once for its whole route. A provider that
// is not a route stands for the one model it was opened for.
func everyHas(p seam.Provider, m models.Model, c models.Capability) bool {
	if route, ok := p.(interface{ Models() []models.Model }); ok {
		all := route.Models()
		for _, x := range all {
			if !x.Has(c) {
				return false
			}
		}
		return len(all) > 0
	}
	return m.Has(c)
}

// withUploads readies one call's branch for the models its route can
// reach (uploads.Prepare), keeps the record of what the call was given and
// withheld, and returns the text files' texts as attachments for the
// context builder. A branch with no image or file is returned as it is.
func (r *Runner) withUploads(ctx context.Context, s *tenant.Store, t row, step int, m models.Model, p seam.Provider, messages []seam.Message) ([]seam.Message, []contextbuild.Item) {
	if !uploads.Holds(messages) {
		return messages, nil
	}
	route := uploads.Route{Sees: everyHas(p, m, models.CapVision), Files: everyHas(p, m, models.CapFiles)}
	current := 0
	if turns := contextbuild.Turns(messages); len(turns) > 0 {
		current = turns[len(turns)-1][0]
	}
	out := uploads.Prepare(messages, current, route, r.uploadWords(ctx, s), uploads.ChatSource(ctx, s, t.ChatID))
	out.Call.Model = m.Name
	if t.media != nil && t.media.moved {
		out.Call.Moved = t.media.picked
	}
	if err := uploads.SaveCall(s, t.ID, t.ChatID, step, t.UserMessageID, out.Call); err != nil {
		r.logf("turns: %s turn %s: keeping what call %d was given of the chat's uploads: %v", t.requestID, t.ID, step, err)
	}
	var items []contextbuild.Item
	for _, a := range out.Attached {
		items = append(items, contextbuild.Item{Label: a.Label, Text: a.Text, Ref: a.Ref})
	}
	return out.Messages, items
}

// uploadWords reads the person's settings for what a call is given of an
// upload.
func (r *Runner) uploadWords(ctx context.Context, s *tenant.Store) uploads.Words {
	return uploads.Words{
		TextMaxChars:     r.number(ctx, s, uploads.SettingTextMaxChars),
		FileHeading:      r.setting(ctx, s, uploads.SettingFileHeading),
		FileMarker:       r.setting(ctx, s, uploads.SettingFileMarker),
		TextCutNote:      r.setting(ctx, s, uploads.SettingTextCutNote),
		ImageWithheld:    r.setting(ctx, s, uploads.SettingImageWithheld),
		DocumentWithheld: r.setting(ctx, s, uploads.SettingDocumentWithheld),
	}
}
