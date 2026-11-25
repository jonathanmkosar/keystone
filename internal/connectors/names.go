package connectors

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// maxToolName is the longest name a tool may have (internal/tools).
const maxToolName = 64

// ToolNames returns the name a chat offers each of a connector's tools by,
// in the order given: the connector's name, an underscore, and the server's
// name for the tool made into a tool's name. "searchNotes" becomes
// "search_notes", and "get-weather" "get_weather". A name too long, or one
// that two of the server's names come to, ends with a few characters of a
// hash of the server's own name, so each is its own and stays the same.
func ToolNames(connector string, theirs []string) []string {
	out := make([]string, len(theirs))
	taken := map[string]bool{}
	for i, t := range theirs {
		name := connector + "_" + snake(t)
		if len(name) > maxToolName || taken[name] {
			name = hashed(name, t)
		}
		taken[name] = true
		out[i] = name
	}
	return out
}

// snake makes a server's tool name into the lower-case letters, digits and
// single underscores of a tool's name.
func snake(s string) string {
	var b strings.Builder
	var prev rune
	for i, r := range s {
		switch {
		case unicode.IsUpper(r) && r < unicode.MaxASCII:
			if i > 0 && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		prev = r
	}
	parts := strings.FieldsFunc(b.String(), func(r rune) bool { return r == '_' })
	if len(parts) == 0 {
		return "tool"
	}
	return strings.Join(parts, "_")
}

// hashed cuts a name to fit and ends it with six characters of a hash of
// the server's own name.
func hashed(name, theirs string) string {
	sum := sha256.Sum256([]byte(theirs))
	tag := "_" + hex.EncodeToString(sum[:3])
	if len(name) > maxToolName-len(tag) {
		name = strings.TrimRight(name[:maxToolName-len(tag)], "_")
	}
	return name + tag
}
