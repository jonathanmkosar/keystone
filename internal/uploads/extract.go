package uploads

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind says how an upload reaches a model.
type Kind string

const (
	// KindImage is sent as an image, and only to a model that can see.
	KindImage Kind = "image"
	// KindText has its text read, and the text goes into a call as an
	// attachment, to any model.
	KindText Kind = "text"
	// KindDocument is sent whole as a file, and only to a model that takes
	// files. Keystone reads no text from it yet (decision 0381).
	KindDocument Kind = "document"
)

// maxName is the most characters of a file's name that are kept. It bounds
// a label on the screen and in a call, and is not a limit a person tunes.
const maxName = 200

// cleanName makes the name a person's browser gave a file into one line to
// show and to tell a model: the last part of a path, with no control
// characters, cut to maxName. It is never used as a path: a stored file is
// named by its hash.
func cleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if utf8.RuneCountInString(name) > maxName {
		name = string([]rune(name)[:maxName])
	}
	if name == "" || name == "." || name == "/" || name == ".." {
		return "file"
	}
	return name
}

// textTypes are the media types the endings of text files are given. An
// ending not here is plain text.
var textTypes = map[string]string{
	".md": "text/markdown", ".markdown": "text/markdown",
	".csv": "text/csv", ".tsv": "text/tab-separated-values",
	".json": "application/json",
	".html": "text/html", ".htm": "text/html",
	".xml": "text/xml",
}

// classify says what kind of upload a file is, and its media type. An image
// or a document is known by its bytes, whatever its name says. A text file
// is known by its ending, and must be UTF-8 with no zero bytes. Anything
// else is refused, with a reason a person can act on.
func classify(name string, data []byte, o Options) (Kind, string, error) {
	if len(data) == 0 {
		return "", "", refusef("the file %s is empty", name)
	}
	sniffed, _, _ := strings.Cut(http.DetectContentType(data), ";")
	sniffed = strings.TrimSpace(sniffed)
	switch {
	case slices.Contains(o.ImageTypes, sniffed):
		return KindImage, sniffed, nil
	case slices.Contains(o.DocumentTypes, sniffed):
		return KindDocument, sniffed, nil
	}
	ext := strings.ToLower(path.Ext(name))
	if ext != "" && slices.Contains(o.TextExtensions, ext) {
		if !isText(data) {
			return "", "", refusef("the file %s ends in %s and is not UTF-8 text", name, ext)
		}
		media, ok := textTypes[ext]
		if !ok {
			media = "text/plain"
		}
		return KindText, media, nil
	}
	return "", "", refusef("Keystone does not take the file %s (%s). %s", name, sniffed, o.takes())
}

// takes says, for a person, which files may be uploaded.
func (o Options) takes() string {
	var parts []string
	if len(o.ImageTypes) > 0 {
		parts = append(parts, "images ("+strings.Join(o.ImageTypes, ", ")+")")
	}
	if len(o.DocumentTypes) > 0 {
		parts = append(parts, "documents ("+strings.Join(o.DocumentTypes, ", ")+")")
	}
	if len(o.TextExtensions) > 0 {
		parts = append(parts, "text files ending in "+strings.Join(o.TextExtensions, ", "))
	}
	return "It takes " + strings.Join(parts, ", and ") + "."
}

var bom = []byte{0xEF, 0xBB, 0xBF}

// isText reports whether data is UTF-8 text with no zero bytes, after a
// byte order mark.
func isText(data []byte) bool {
	data = bytes.TrimPrefix(data, bom)
	return utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
}

// Extract returns the text a model is given of a text file: the file as it
// is for plain text, Markdown, code, CSV and JSON, and the words of an HTML
// page with its markup, scripts and styles taken out. Line ends become \n
// and a byte order mark is dropped.
func Extract(mediaType string, data []byte) string {
	text := string(bytes.TrimPrefix(data, bom))
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if mediaType == "text/html" {
		return htmlText(text)
	}
	return text
}

// skipped are the elements whose content is not words a reader sees.
var skipped = map[string]bool{"script": true, "style": true, "noscript": true, "template": true, "svg": true, "math": true, "iframe": true, "object": true}

// breaks are the elements that start a new line, opening or closing.
var breaks = map[string]bool{
	"p": true, "div": true, "section": true, "article": true, "header": true, "footer": true, "main": true, "nav": true, "aside": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "ul": true, "ol": true, "dl": true, "dt": true, "dd": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true, "blockquote": true, "pre": true, "hr": true, "figure": true,
	"figcaption": true, "form": true, "fieldset": true, "address": true, "title": true, "caption": true, "details": true, "summary": true,
}

// htmlText is the words of an HTML page. It is a small reader on the
// standard library, as the repo has no HTML parser (decision 0381): tags
// are taken out, block elements and line breaks start lines, a list item
// starts a line with a dash, table cells are parted by a bar, scripts,
// styles and comments are dropped, and character references are decoded.
// What it cannot read as a tag it keeps as text.
func htmlText(src string) string {
	var b strings.Builder
	skip := "" // the element whose content is being skipped
	for i := 0; i < len(src); {
		if src[i] != '<' {
			j := strings.IndexByte(src[i:], '<')
			if j < 0 {
				j = len(src) - i
			}
			if skip == "" {
				b.WriteString(src[i : i+j])
			}
			i += j
			continue
		}
		if strings.HasPrefix(src[i:], "<!--") {
			end := strings.Index(src[i+4:], "-->")
			if end < 0 {
				break
			}
			i += 4 + end + 3
			continue
		}
		// A < that does not start a tag is text, as in "1 < 2".
		if i+1 >= len(src) || !(isLetter(src[i+1]) || src[i+1] == '/' || src[i+1] == '!' || src[i+1] == '?') {
			if skip == "" {
				b.WriteByte('<')
			}
			i++
			continue
		}
		end := strings.IndexByte(src[i:], '>')
		if end < 0 {
			if skip == "" {
				b.WriteString(src[i:])
			}
			break
		}
		tag := src[i+1 : i+end]
		i += end + 1
		name, closing := tagName(tag)
		if skip != "" {
			if closing && name == skip {
				skip = ""
			}
			continue
		}
		switch {
		case skipped[name] && !closing && !strings.HasSuffix(tag, "/"):
			skip = name
		case name == "br":
			b.WriteString("\n")
		case name == "li" && !closing:
			b.WriteString("\n- ")
		case name == "tr" && !closing:
			b.WriteString("\n")
		case (name == "td" || name == "th") && closing:
			b.WriteString(" | ")
		case breaks[name]:
			b.WriteString("\n")
		}
	}
	return tidyLines(html.UnescapeString(b.String()))
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// tagName returns a tag's element name in lower case, and whether it closes
// an element. A declaration or an instruction has no name.
func tagName(tag string) (string, bool) {
	closing := strings.HasPrefix(tag, "/")
	tag = strings.TrimPrefix(tag, "/")
	n := 0
	for n < len(tag) && (isLetter(tag[n]) || (n > 0 && tag[n] >= '0' && tag[n] <= '9')) {
		n++
	}
	return strings.ToLower(tag[:n]), closing
}

// tidyLines puts each line's words one space apart, drops the bar after a
// row's last cell, and keeps no more than one empty line in a row and none
// at either end.
func tidyLines(text string) string {
	var out []string
	blank := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.Join(strings.Fields(strings.ReplaceAll(line, " ", " ")), " ")
		line = strings.TrimSpace(strings.TrimSuffix(line, "|"))
		if line == "" {
			blank = len(out) > 0
			continue
		}
		if blank {
			out = append(out, "")
			blank = false
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// refusef is an upload refused for what it is, with a reason a person can
// act on.
func refusef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, args...))
}
