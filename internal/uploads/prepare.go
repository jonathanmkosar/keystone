package uploads

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Route is what every model one call may reach can take. A call is built
// once and sent as it is to whichever model of its route answers, so it is
// built for the least of them.
type Route struct {
	// Sees says every model of the route can see images.
	Sees bool
	// Files says every model of the route takes files.
	Files bool
}

// Words are what a call is given in place of what it is not shown, and
// around a file's text, from the person's settings. {name} becomes the
// file's name, and {chars} the number of characters left out.
type Words struct {
	// TextMaxChars is the most characters of one file's text that go in.
	TextMaxChars int
	// FileHeading comes before a text file's text, in the attachments.
	FileHeading string
	// FileMarker stands in the person's message where a text file was.
	FileMarker string
	// TextCutNote follows a file's text that was cut.
	TextCutNote string
	// ImageWithheld stands where an image was, for a call no model of
	// which can see.
	ImageWithheld string
	// DocumentWithheld stands where a document was, for a call no model of
	// which takes files.
	DocumentWithheld string
}

// Source is where Prepare finds a chat's uploads.
type Source interface {
	// Upload returns the upload of the chat with this id, and false when
	// there is none.
	Upload(id string) (Upload, bool, error)
	// Bytes returns the bytes of an upload's file.
	Bytes(id string) ([]byte, error)
}

// ChatSource is the uploads of one chat in a tenant's store.
func ChatSource(ctx context.Context, s *tenant.Store, chatID string) Source {
	return chatSource{ctx, s, chatID}
}

type chatSource struct {
	ctx    context.Context
	s      *tenant.Store
	chatID string
}

func (c chatSource) Upload(id string) (Upload, bool, error) {
	u, err := Get(c.ctx, c.s, c.chatID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Upload{}, false, nil
		}
		return Upload{}, false, err
	}
	return u, true, nil
}

func (c chatSource) Bytes(id string) ([]byte, error) { return Read(c.s, id) }

// How an upload went into a call, in Given.Sent.
const (
	// SentImage: the image's bytes were sent.
	SentImage = "image"
	// SentFile: the document's bytes were sent as a file.
	SentFile = "file"
	// SentText: its text went into the attachments.
	SentText = "text"
	// SentWithheld: it was not sent, and a note stands in its place.
	SentWithheld = "withheld"
)

// Why an upload was withheld, in Given.Why.
const (
	// WhyVision: a model the call could reach cannot see images.
	WhyVision = "vision"
	// WhyFiles: a model the call could reach takes no files.
	WhyFiles = "files"
	// WhyMissing: the upload or its file is no longer there.
	WhyMissing = "missing"
)

// Call is what one model call was given of its chat's uploads, kept for
// the turn inspector and for the notice to the person.
type Call struct {
	// Model is the model the call was built for: the first of its route.
	Model string `json:"model"`
	// Sees and Files are the route's: every model it could reach can see,
	// and takes files.
	Sees  bool `json:"sees"`
	Files bool `json:"files"`
	// Moved names the chat's model when it cannot see the image in the
	// turn's message, and the call went to the first model of the
	// fallback chain that can.
	Moved string `json:"moved,omitempty"`
	// Items are the images and files of the call's branch, in the order of
	// the branch.
	Items []Given `json:"items"`
}

// Given is one image or file of a call's branch.
type Given struct {
	// ID is the upload's id, and empty for an image sent in the message
	// itself.
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	// Current says it is in the turn's own message.
	Current bool `json:"current,omitempty"`
	// Sent is image, file, text or withheld.
	Sent string `json:"sent"`
	// Why says why it was withheld: vision, files or missing.
	Why string `json:"why,omitempty"`
	// Chars is how many characters of its text went in, and Cut says the
	// rest was left out.
	Chars int  `json:"chars,omitempty"`
	Cut   bool `json:"cut,omitempty"`
}

// Withheld lists what the call did not send.
func (c Call) Withheld() []Given {
	var out []Given
	for _, g := range c.Items {
		if g.Sent == SentWithheld {
			out = append(out, g)
		}
	}
	return out
}

// Attachment is a text file's text, for the attachments part of a call.
type Attachment struct {
	// Ref is the turn inspector's name for it: upload:<id>.
	Ref string
	// Label is what the inspector calls it.
	Label string
	// Text is what is sent: the heading and the file's text.
	Text string
}

// Prepared is a branch readied for one call.
type Prepared struct {
	// Messages are the branch, with every image and file block as the call
	// sends it. There are as many messages as before, each with as many
	// blocks, so an index into the branch still holds.
	Messages []seam.Message
	// Attached are the text files' texts, the newest first, each once.
	Attached []Attachment
	Call     Call
}

// Holds reports whether a branch has any image or file block that Prepare
// would change: an image, or a block that names an upload.
func Holds(messages []seam.Message) bool {
	for _, m := range messages {
		if blocksHold(m.Blocks) {
			return true
		}
	}
	return false
}

func blocksHold(blocks []seam.Block) bool {
	for _, b := range blocks {
		if b.Type == seam.BlockImage || (b.Type == seam.BlockFile && b.Ref != "") || blocksHold(b.Content) {
			return true
		}
	}
	return false
}

// RefOf is the turn inspector's name for an upload's attachment.
func RefOf(id string) string { return "upload:" + id }

// Prepare readies a branch for one call to a route. current is the index
// of the first message of the turn's own part of the branch.
//
//   - An image goes as its bytes only when every model of the route can
//     see. Otherwise words.ImageWithheld stands in its place. This is the
//     rule that an image never reaches a model without vision; the
//     adapters refuse one as well (decision 0383).
//   - A text file's block becomes words.FileMarker, and its text, cut to
//     words.TextMaxChars, goes into Attached under words.FileHeading.
//   - A document goes as its bytes, as a file, only when every model of
//     the route takes files. Otherwise words.DocumentWithheld stands in
//     its place.
//   - An upload or a file that is no longer there is withheld with the
//     same words, and recorded as missing.
//
// A file sent in the message itself, with its bytes, is left as it is. An
// error reading an upload withholds it: Prepare never fails, and never
// sends what it could not check.
func Prepare(messages []seam.Message, current int, route Route, w Words, src Source) Prepared {
	p := &preparer{route: route, words: w, src: src, seen: map[string]int{}}
	out := Prepared{Messages: make([]seam.Message, len(messages)), Call: Call{Sees: route.Sees, Files: route.Files, Items: []Given{}}}
	for i, m := range messages {
		out.Messages[i] = seam.Message{Role: m.Role, Blocks: p.blocks(m.Blocks, i >= current)}
	}
	out.Call.Items = p.given
	// The newest file first: the one the person most likely means.
	for i := len(p.texts) - 1; i >= 0; i-- {
		if p.texts[i].Text != "" {
			out.Attached = append(out.Attached, p.texts[i])
		}
	}
	return out
}

type preparer struct {
	route Route
	words Words
	src   Source
	given []Given
	texts []Attachment
	// seen maps an upload's id to its place in texts, so a file named
	// twice goes in once, at its newest place.
	seen map[string]int
}

func (p *preparer) blocks(blocks []seam.Block, current bool) []seam.Block {
	if !blocksHold(blocks) {
		return blocks
	}
	out := make([]seam.Block, len(blocks))
	for i, b := range blocks {
		switch {
		case b.Type == seam.BlockImage:
			out[i] = p.image(b, current)
		case b.Type == seam.BlockFile && b.Ref != "":
			out[i] = p.file(b, current)
		case b.Type == seam.BlockToolResult && blocksHold(b.Content):
			b.Content = p.blocks(b.Content, current)
			out[i] = b
		default:
			out[i] = b
		}
	}
	return out
}

func (p *preparer) note(text, name string) seam.Block {
	text = strings.ReplaceAll(text, "{name}", name)
	if strings.TrimSpace(text) == "" {
		text = "[" + name + "]" // a block needs text, and a note set to nothing still says where the file was
	}
	return seam.Block{Type: seam.BlockText, Text: text}
}

// image readies one image block.
func (p *preparer) image(b seam.Block, current bool) seam.Block {
	g := Given{ID: b.Ref, Name: "an image", Kind: KindImage, Current: current}
	if b.Ref == "" {
		// An image sent in the message itself.
		if p.route.Sees {
			g.Sent = SentImage
			p.given = append(p.given, g)
			return b
		}
		g.Sent, g.Why = SentWithheld, WhyVision
		p.given = append(p.given, g)
		return p.note(p.words.ImageWithheld, g.Name)
	}
	up, ok, err := p.src.Upload(b.Ref)
	if ok {
		g.Name = up.Name
	}
	switch {
	case err != nil || !ok:
		g.Sent, g.Why = SentWithheld, WhyMissing
	case !p.route.Sees:
		g.Sent, g.Why = SentWithheld, WhyVision
	default:
		data, err := p.src.Bytes(b.Ref)
		if err != nil {
			g.Sent, g.Why = SentWithheld, WhyMissing
			break
		}
		g.Sent = SentImage
		p.given = append(p.given, g)
		return seam.Block{Type: seam.BlockImage, MediaType: up.MediaType, Data: data}
	}
	p.given = append(p.given, g)
	return p.note(p.words.ImageWithheld, g.Name)
}

// file readies one file block that names an upload.
func (p *preparer) file(b seam.Block, current bool) seam.Block {
	g := Given{ID: b.Ref, Name: b.Name, Kind: KindDocument, Current: current}
	if g.Name == "" {
		g.Name = "a file"
	}
	up, ok, err := p.src.Upload(b.Ref)
	if err != nil || !ok {
		g.Sent, g.Why = SentWithheld, WhyMissing
		p.given = append(p.given, g)
		return p.note(p.words.DocumentWithheld, g.Name)
	}
	g.Name, g.Kind = up.Name, up.Kind
	switch up.Kind {
	case KindImage:
		// A file block for an image: sent as the image it is.
		return p.image(seam.Block{Type: seam.BlockImage, MediaType: up.MediaType, Ref: b.Ref}, current)
	case KindText:
		text, cut := cutText(up.Text(), p.words.TextMaxChars)
		g.Sent, g.Chars, g.Cut = SentText, utf8.RuneCountInString(text), cut
		p.given = append(p.given, g)
		body := strings.ReplaceAll(p.words.FileHeading, "{name}", up.Name) + "\n\n" + text
		if cut {
			left := utf8.RuneCountInString(up.Text()) - g.Chars
			body += "\n\n" + strings.ReplaceAll(strings.ReplaceAll(p.words.TextCutNote, "{name}", up.Name), "{chars}", fmt.Sprint(left))
		}
		a := Attachment{Ref: RefOf(up.ID), Label: "file: " + up.Name, Text: strings.TrimSpace(body)}
		if at, dup := p.seen[up.ID]; dup {
			p.texts[at] = Attachment{} // kept at its newest place only
		}
		p.seen[up.ID] = len(p.texts)
		p.texts = append(p.texts, a)
		return p.note(p.words.FileMarker, up.Name)
	}
	if !p.route.Files {
		g.Sent, g.Why = SentWithheld, WhyFiles
		p.given = append(p.given, g)
		return p.note(p.words.DocumentWithheld, up.Name)
	}
	data, err := p.src.Bytes(b.Ref)
	if err != nil {
		g.Sent, g.Why = SentWithheld, WhyMissing
		p.given = append(p.given, g)
		return p.note(p.words.DocumentWithheld, up.Name)
	}
	g.Sent = SentFile
	p.given = append(p.given, g)
	return seam.Block{Type: seam.BlockFile, MediaType: up.MediaType, Name: up.Name, Data: data}
}

// cutText returns at most most characters of text, cut at the end of a
// line where one is near, and whether anything was left out. Zero or less
// keeps none.
func cutText(text string, most int) (string, bool) {
	if utf8.RuneCountInString(text) <= most {
		return text, false
	}
	if most <= 0 {
		return "", text != ""
	}
	kept := string([]rune(text)[:most])
	// A cut at a line's end reads better, if one is in the last tenth.
	if at := strings.LastIndexByte(kept, '\n'); at > 0 && utf8.RuneCountInString(kept[at:]) < most/10 {
		kept = kept[:at]
	}
	return kept, true
}
