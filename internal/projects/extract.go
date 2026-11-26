package projects

import (
	"bytes"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TextReader reads the text a model is given of a file, and its media
// type, or refuses the file with ErrInvalid and a reason a person can act
// on. extensions are the endings taken. It is the seam the uploads lane's
// extraction may take the place of (decision 0401).
type TextReader func(name string, data []byte, extensions []string) (mediaType, text string, err error)

func (o Options) read() TextReader {
	if o.Read == nil {
		return ReadText
	}
	return o.Read
}

// nameMaxChars is the most characters of a file's name that are kept. It
// bounds a label on the screen and in a call, and is not a limit a person
// tunes.
const nameMaxChars = 200

// FileName makes the name a person's browser gave a file into one line to
// show and to tell a model: the last part of a path, with no control
// characters, cut to its longest. It is never used as a path: a stored file
// is named by its hash.
func FileName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if utf8.RuneCountInString(name) > nameMaxChars {
		name = string([]rune(name)[:nameMaxChars])
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

var bom = []byte{0xEF, 0xBB, 0xBF}

// ReadText is the TextReader this package ships. It takes text formats
// only: plain text, Markdown, code, CSV, JSON and the rest of extensions,
// known by the file's ending, which must be UTF-8 with no zero bytes. The
// text is the file as it is, with a byte order mark dropped and line ends
// made \n. A PDF, an image or any other file is refused until the uploads
// lane's extraction is shared (decision 0401).
func ReadText(name string, data []byte, extensions []string) (string, string, error) {
	if len(data) == 0 {
		return "", "", invalid("the file %s is empty", name)
	}
	ext := strings.ToLower(path.Ext(name))
	if ext == "" || !slices.Contains(extensions, ext) {
		return "", "", invalid("a project takes text files ending in %s, and %s is not one", strings.Join(extensions, ", "), name)
	}
	body := bytes.TrimPrefix(data, bom)
	if !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
		return "", "", invalid("the file %s ends in %s and is not UTF-8 text", name, ext)
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(body), "\r\n", "\n"), "\r", "\n")
	if strings.TrimSpace(text) == "" {
		return "", "", invalid("the file %s holds no text", name)
	}
	media, ok := textTypes[ext]
	if !ok {
		media = "text/plain"
	}
	return media, text, nil
}
