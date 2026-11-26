package share

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// View is what a link shows: one chat's title and the messages of one
// branch, as text, images and names. It holds nothing else of the person's:
// no name, no other chat, no model, no setting, no memory, and of a reply
// its words and the names of the tools it used, never what a tool was sent
// or gave back, and never the model's thinking.
type View struct {
	Title string `json:"title"`
	// View is live or snapshot.
	View string `json:"view"`
	// Shared is when the link was made.
	Shared time.Time `json:"shared"`
	// AsOf is when the last message shown was written. Absent for a chat
	// whose branch is empty.
	AsOf *time.Time `json:"as_of,omitempty"`
	// Entries are the person's messages and the replies, oldest first.
	Entries []Entry `json:"entries"`

	images map[string]bool
}

// Who wrote an entry, in Entry.Role.
const (
	RolePerson = "person"
	RoleReply  = "reply"
)

// Entry is one of the person's messages, or one reply: a model's messages
// in a row, with the tool steps between them, read as one.
type Entry struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

// The kinds of a Part.
const (
	PartText     = "text"
	PartImage    = "image"
	PartFile     = "file"
	PartTool     = "tool"
	PartArtifact = "artifact"
	PartNote     = "note"
)

// Part is one piece of an entry.
//
//	text      Text, to be read as Markdown and drawn as text, never as HTML
//	image     Image, the id to fetch through the link, when images are
//	          shown; MediaType
//	file      Name, the file's name; its contents are never shown
//	tool      Name, the tool's name; Failed when it gave an error
//	artifact  Artifact, a version a tool step made, as its source
//	note      Text, Keystone's own words about the reply
type Part struct {
	Kind      string    `json:"kind"`
	Text      string    `json:"text,omitempty"`
	Name      string    `json:"name,omitempty"`
	Image     string    `json:"image,omitempty"`
	MediaType string    `json:"media_type,omitempty"`
	Failed    bool      `json:"failed,omitempty"`
	Artifact  *Artifact `json:"artifact,omitempty"`
}

// Artifact is a version of an artifact the chat made, shown as its source
// and never run.
type Artifact struct {
	Title    string `json:"title"`
	Type     string `json:"type"`
	Language string `json:"language,omitempty"`
	Version  int    `json:"version"`
	Content  string `json:"content"`
}

// HasImage reports whether the view shows the image with this id, so it may
// be fetched through the link.
func (v View) HasImage(id string) bool { return v.images[id] }

// Show is what a view may hold beyond the words. The caller reads it from
// the settings.
type Show struct {
	// Images gives the person's uploaded images their ids, so the page can
	// fetch them through the link.
	Images bool
	// Artifact returns an artifact version that a tool step of the chat
	// made, as its source. Nil shows each such step by its tool's name. It
	// is called only for an artifact of the link's own chat.
	Artifact func(ctx context.Context, s *tenant.Store, id string, version int) (*Artifact, error)
}

// The tools that make an artifact version, and the words of their result
// that name it: "(artifact <id>, version <n>". They are the artifacts
// package's; this package cannot import it, because it reads settings.
var (
	artifactTools = map[string]bool{"create_artifact": true, "update_artifact": true}
	artifactNamed = regexp.MustCompile(`\(artifact ([0-9a-f]{16}), version (\d+)`)
)

// Keystone's words for a reply that did not end by the model finishing.
var endNotes = map[string]string{
	"stopped":    "The person stopped this reply here.",
	"failed":     "This reply broke off here.",
	"max_tokens": "This reply reached its length limit here.",
	"refused":    "The model declined to answer.",
}

// Stop reasons of Keystone's own messages (internal/turn).
const (
	stopStepLimit = "step_limit"
	stopTask      = "task"
)

// currentBranch walks a chat's current branch from its first message, as
// chat.Path does. Its one parameter is the chat's id.
const currentBranch = `WITH RECURSIVE branch(id, n) AS (
		SELECT current_root, 0 FROM chats WHERE id = ?1 AND current_root IS NOT NULL
		UNION ALL
		SELECT m.current_child, branch.n + 1 FROM messages m JOIN branch ON m.id = branch.id
		  WHERE m.current_child IS NOT NULL AND m.chat_id = ?1 AND branch.n < 1000000
	)`

// upToMessage walks from a message up to the chat's first, by each one's
// parent. Its parameters are the chat's id and the message's.
const upToMessage = `WITH RECURSIVE branch(id, n) AS (
		SELECT id, 0 FROM messages WHERE chat_id = ?1 AND id = ?2
		UNION ALL
		SELECT m.parent_id, branch.n - 1 FROM messages m JOIN branch ON m.id = branch.id
		  WHERE m.parent_id IS NOT NULL AND m.chat_id = ?1 AND branch.n > -1000000
	)`

type stored struct {
	id      int64
	message seam.Message
	created time.Time
	stop    string
}

// branchOf returns the messages a link shows, first message first: the
// current branch for a live link, and the branch up to its last message for
// a snapshot. Every row is the link's own chat's.
func branchOf(ctx context.Context, s *tenant.Store, l Link) ([]stored, error) {
	walk, args := currentBranch, []any{l.ChatID}
	if l.View == ViewSnapshot {
		walk, args = upToMessage, []any{l.ChatID, l.upto}
	}
	rows, err := s.DB().QueryContext(ctx, walk+`
		SELECT m.id, m.content, m.created_at, COALESCE(t.stop_reason, '')
		FROM branch JOIN messages m ON m.id = branch.id AND m.chat_id = ?1
		LEFT JOIN turn_messages t ON t.message_id = m.id
		ORDER BY branch.n`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []stored
	for rows.Next() {
		var (
			m                stored
			content, created string
		)
		if err := rows.Scan(&m.id, &content, &created, &m.stop); err != nil {
			return nil, err
		}
		if m.message, err = seam.DecodeMessage([]byte(content)); err != nil {
			return nil, fmt.Errorf("share: message %d does not read: %w", m.id, err)
		}
		if m.created, err = time.Parse(timeLayout, created); err != nil {
			return nil, fmt.Errorf("share: message %d: %w", m.id, err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// result is what came back for one tool call.
type result struct {
	text   string
	failed bool
}

// Read builds what a link shows. It does not ask whether the link may be
// shown: the caller asks Usable first.
func Read(ctx context.Context, s *tenant.Store, l Link, show Show) (View, error) {
	v := View{Title: l.ChatTitle, View: l.View, Shared: l.Created, Entries: []Entry{}, images: map[string]bool{}}
	msgs, err := branchOf(ctx, s, l)
	if err != nil {
		return View{}, err
	}
	if len(msgs) > 0 {
		at := msgs[len(msgs)-1].created
		v.AsOf = &at
	}
	// Each tool call's result, by its id, read only to know whether it
	// failed and which artifact version it made.
	results := map[string]result{}
	for _, m := range msgs {
		for _, b := range m.message.Blocks {
			if b.Type == seam.BlockToolResult {
				text := ""
				for _, c := range b.Content {
					if c.Type == seam.BlockText {
						text += c.Text
					}
				}
				results[b.ID] = result{text: text, failed: b.IsError}
			}
		}
	}
	// lastIsReply says the last entry is a reply that a later message of
	// the model joins.
	lastIsReply := false
	for _, m := range msgs {
		switch m.message.Role {
		case seam.RoleUser:
			parts := personParts(m.message.Blocks, show, v.images)
			if len(parts) == 0 {
				continue // a message of tool results alone
			}
			v.Entries = append(v.Entries, Entry{Role: RolePerson, Parts: parts})
			lastIsReply = false
		case seam.RoleAssistant:
			parts, err := replyParts(ctx, s, l, m, results, show)
			if err != nil {
				return View{}, err
			}
			// A background task's note is a reply of its own (P8-01).
			if lastIsReply && m.stop != stopTask {
				last := &v.Entries[len(v.Entries)-1]
				last.Parts = append(last.Parts, parts...)
			} else {
				v.Entries = append(v.Entries, Entry{Role: RoleReply, Parts: parts})
			}
			lastIsReply = m.stop != stopTask
		}
	}
	// A reply with nothing to show is left out.
	kept := v.Entries[:0]
	for _, e := range v.Entries {
		if len(e.Parts) > 0 {
			kept = append(kept, e)
		}
	}
	v.Entries = kept
	return v, nil
}

// personParts is what a person's message shows: its words, its images and
// the names of its files. A tool's results, which travel in a message of the
// person's role, are left out.
func personParts(blocks []seam.Block, show Show, images map[string]bool) []Part {
	var parts []Part
	for _, b := range blocks {
		switch b.Type {
		case seam.BlockText:
			parts = append(parts, Part{Kind: PartText, Text: b.Text})
		case seam.BlockImage:
			p := Part{Kind: PartImage, MediaType: b.MediaType}
			if show.Images && b.Ref != "" {
				p.Image = b.Ref
				images[b.Ref] = true
			}
			parts = append(parts, p)
		case seam.BlockFile:
			parts = append(parts, Part{Kind: PartFile, Name: b.Name})
		}
	}
	return parts
}

// replyParts is what one of the model's messages shows: its words, each
// tool step by its name, an artifact version a step made as its source, and
// a note when it did not end by the model finishing.
func replyParts(ctx context.Context, s *tenant.Store, l Link, m stored, results map[string]result, show Show) ([]Part, error) {
	var parts []Part
	for _, b := range m.message.Blocks {
		switch b.Type {
		case seam.BlockText:
			kind := PartText
			if m.stop == stopStepLimit {
				kind = PartNote // Keystone's own note at the step limit
			}
			parts = append(parts, Part{Kind: kind, Text: b.Text})
		case seam.BlockToolCall:
			res, answered := results[b.ID]
			if answered && !res.failed && artifactTools[b.Name] && show.Artifact != nil {
				a, err := artifactOf(ctx, s, l, res.text, show)
				if err != nil {
					return nil, err
				}
				if a != nil {
					parts = append(parts, Part{Kind: PartArtifact, Name: b.Name, Artifact: a})
					continue
				}
			}
			parts = append(parts, Part{Kind: PartTool, Name: b.Name, Failed: answered && res.failed})
		}
	}
	if note, ok := endNotes[m.stop]; ok {
		parts = append(parts, Part{Kind: PartNote, Text: note})
	}
	return parts, nil
}

// artifactOf is the artifact version a tool step's result names, when it is
// of the link's own chat, or nil.
func artifactOf(ctx context.Context, s *tenant.Store, l Link, said string, show Show) (*Artifact, error) {
	m := artifactNamed.FindStringSubmatch(said)
	if m == nil {
		return nil, nil
	}
	version, err := strconv.Atoi(m[2])
	if err != nil || version < 1 {
		return nil, nil
	}
	var chatID string
	err = s.DB().QueryRowContext(ctx, "SELECT chat_id FROM artifacts WHERE id = ?", m[1]).Scan(&chatID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && chatID != l.ChatID) {
		return nil, nil // gone, or not this chat's: shown as a step
	}
	if err != nil {
		return nil, err
	}
	return show.Artifact(ctx, s, m[1], version)
}
