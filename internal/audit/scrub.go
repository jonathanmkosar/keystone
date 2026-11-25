package audit

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// removed stands where a key or a token was. It holds no quote and no
// backslash, so JSON text stays JSON after a value inside it is removed.
const removed = "[removed]"

// keyShapes are the shapes of real keys and tokens. They cover every shape
// verify's "no secrets" check knows, and a few more.
var keyShapes = []*regexp.Regexp{
	// A private key block, whole. Inside JSON text its line ends are
	// written as \n or \r\n.
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----(?:[A-Za-z0-9+/=\s]|\\[nr])*(?:-----END [A-Z ]*PRIVATE KEY-----)?`),
	// Provider keys that start sk-: Anthropic, OpenAI, OpenRouter and others.
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{30,}`),
	regexp.MustCompile(`tvly-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`AIza[A-Za-z0-9_-]{30,}`),
	regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`),
	// A signed web token: three runs of letters joined by dots.
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
	// Keystone's own session token (internal/signin).
	regexp.MustCompile(`kss_[a-f0-9]+_[a-f0-9]{64}`),
}

var (
	// bearer is a token named as one: "Bearer" and a long word.
	bearer = regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/=-]{16,}`)
	// headerLine is a header that carries a credential, written out as text.
	headerLine = regexp.MustCompile(`(?i)\b((?:proxy-)?authorization|x-api-key|api-key|cookie|set-cookie)(\s*:\s*)[^\r\n"\\]+`)
	// secretField is a JSON string field whose name says it holds a secret.
	// A number is left alone, so token counts are kept.
	secretField = regexp.MustCompile(`(?i)("[^"\\]*(?:password|passphrase|secret|token|authorization|cookie|api[_-]?key|access[_-]?key|private[_-]?key)[^"\\]*"\s*:\s*)"(?:[^"\\]|\\.)*"`)
)

// Scrub returns text with every key and token taken out, each replaced by
// "[removed]". It removes, in this order:
//
//   - each value in secrets, exactly as given and as it is written inside
//     JSON text, whatever its shape;
//   - anything shaped like a known key or token;
//   - the token after the word Bearer, and the value of a header that
//     carries a credential;
//   - the value of a JSON string field whose name says it holds a secret,
//     such as password, token or api_key.
//
// It is used on every entry before it is written. Logs and API responses
// may use it too.
func Scrub(text string, secrets ...string) string {
	if text == "" {
		return text
	}
	for _, s := range secrets {
		if s == "" {
			continue
		}
		for _, form := range forms(s) {
			text = strings.ReplaceAll(text, form, removed)
		}
	}
	for _, re := range keyShapes {
		text = re.ReplaceAllString(text, removed)
	}
	text = bearer.ReplaceAllString(text, "${1} "+removed)
	text = headerLine.ReplaceAllString(text, "${1}${2}"+removed)
	text = secretField.ReplaceAllString(text, `${1}"`+removed+`"`)
	return text
}

// forms returns a secret as given, and as it reads inside JSON text, where
// a quote or a backslash in it is escaped. JSON writers differ on <, > and
// &, so both spellings are returned.
func forms(secret string) []string {
	out := []string{secret}
	for _, escapeHTML := range []bool{true, false} {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(escapeHTML)
		if err := enc.Encode(secret); err != nil {
			continue
		}
		// Encode writes the string in quotes with a line end after it.
		inner := strings.TrimSuffix(buf.String(), "\n")
		if len(inner) < 2 {
			continue
		}
		inner = inner[1 : len(inner)-1]
		if inner != secret && inner != out[len(out)-1] {
			out = append(out, inner)
		}
	}
	return out
}
