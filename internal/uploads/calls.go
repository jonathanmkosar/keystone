package uploads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Blocks checks the image and file blocks of a message a person sends, and
// writes each as its upload says. A block that names a file must name an
// upload of the chat: its type, media type and name are then the upload's,
// whatever the client sent, so an image is always an image block and a
// text file a file block. A block that carries its bytes is left as it is.
// A ref that names no upload of the chat is refused with ErrRefused.
func Blocks(ctx context.Context, s *tenant.Store, chatID string, blocks []seam.Block) ([]seam.Block, error) {
	var out []seam.Block
	for _, b := range blocks {
		if (b.Type != seam.BlockImage && b.Type != seam.BlockFile) || b.Ref == "" {
			out = append(out, b)
			continue
		}
		up, err := Get(ctx, s, chatID, b.Ref)
		if err != nil {
			if isNotFound(err) {
				return nil, refusef("%q is not a file uploaded to this chat: upload it first, and name it by the id the upload returned", short(b.Ref))
			}
			return nil, err
		}
		out = append(out, BlockOf(up))
	}
	return out, nil
}

// BlockOf is the block a message names an upload with.
func BlockOf(up Upload) seam.Block {
	if up.Kind == KindImage {
		return seam.Block{Type: seam.BlockImage, MediaType: up.MediaType, Ref: up.ID}
	}
	return seam.Block{Type: seam.BlockFile, MediaType: up.MediaType, Name: up.Name, Ref: up.ID}
}

func short(s string) string {
	if len(s) > 70 {
		return s[:70] + "..."
	}
	return s
}

// SaveCall keeps what one model call of a turn was given of its chat's
// uploads. messageID is the person's message the turn answers.
func SaveCall(s *tenant.Store, turnID, chatID string, step int, messageID int64, c Call) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.DB().Exec("INSERT OR REPLACE INTO upload_calls(turn_id, chat_id, step, message_id, record) VALUES(?, ?, ?, ?, ?)",
		turnID, chatID, step, messageID, string(raw))
	return err
}

// StepCall is what one model call of a turn was given, by its step.
type StepCall struct {
	Step int  `json:"step"`
	Call Call `json:"call"`
}

// Calls returns what each model call of a turn was given of its chat's
// uploads, in the order of the calls. A turn whose branch held none has
// none.
func Calls(ctx context.Context, s *tenant.Store, turnID string) ([]StepCall, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT step, record FROM upload_calls WHERE turn_id = ? ORDER BY step", turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StepCall
	for rows.Next() {
		var sc StepCall
		var raw string
		if err := rows.Scan(&sc.Step, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &sc.Call); err != nil {
			return nil, fmt.Errorf("turn %s, call %d: its uploads record does not read: %w", turnID, sc.Step, err)
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// The kinds of notice.
const (
	// NoticeWithheld: an image or a file of the person's message did not
	// reach the reply's model.
	NoticeWithheld = "withheld"
	// NoticeMoved: the chat's model cannot see the image in the person's
	// message, so the first fallback that can answered.
	NoticeMoved = "moved"
)

// Notice is what the person is told about a reply and the uploads of the
// message it answers.
type Notice struct {
	TurnID string `json:"turn_id"`
	// MessageID is the person's message the reply answers.
	MessageID int64  `json:"message_id"`
	Kind      string `json:"kind"`
	// UploadID and Name are the upload's, for a withheld one.
	UploadID string `json:"upload_id,omitempty"`
	Name     string `json:"name,omitempty"`
	// Text is the sentence shown.
	Text string `json:"text"`
}

// Notices returns what the person is told about the replies of a chat:
// for each turn, from its first call, each image or file of the person's
// message that was withheld, and whether the turn went to a fallback to
// see an image. The oldest turn first.
func Notices(ctx context.Context, s *tenant.Store, chatID string) ([]Notice, error) {
	// The turns table is the turn runner's (turns.md); its seq is the order
	// the turns arrived in.
	rows, err := s.DB().QueryContext(ctx, `SELECT c.turn_id, c.message_id, c.record FROM upload_calls c
		JOIN turns t ON t.id = c.turn_id
		WHERE c.chat_id = ? AND c.step = (SELECT min(step) FROM upload_calls WHERE turn_id = c.turn_id)
		ORDER BY t.seq`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notice{}
	for rows.Next() {
		var turnID, raw string
		var message int64
		if err := rows.Scan(&turnID, &message, &raw); err != nil {
			return nil, err
		}
		var c Call
		if json.Unmarshal([]byte(raw), &c) != nil {
			continue // a record that does not read tells nothing
		}
		if c.Moved != "" {
			out = append(out, Notice{TurnID: turnID, MessageID: message, Kind: NoticeMoved,
				Text: fmt.Sprintf("%s cannot see images, so %s answered this message.", c.Moved, c.Model)})
		}
		for _, g := range c.Withheld() {
			if !g.Current {
				continue
			}
			out = append(out, Notice{TurnID: turnID, MessageID: message, Kind: NoticeWithheld, UploadID: g.ID, Name: g.Name, Text: withheldText(g)})
		}
	}
	return out, rows.Err()
}

// withheldText says, for a person, why a reply did not get an upload.
func withheldText(g Given) string {
	switch g.Why {
	case WhyVision:
		return fmt.Sprintf("The reply did not see %s: no model this message could go to can see images.", quoteName(g))
	case WhyFiles:
		return fmt.Sprintf("The reply did not get %s: Keystone cannot read its text yet, and no model this message could go to takes files.", quoteName(g))
	}
	return fmt.Sprintf("The reply did not get %s: it is no longer stored.", quoteName(g))
}

func quoteName(g Given) string {
	if g.ID == "" || strings.HasPrefix(g.Name, "an ") || strings.HasPrefix(g.Name, "a ") {
		return g.Name
	}
	return "“" + g.Name + "”"
}

func isNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
