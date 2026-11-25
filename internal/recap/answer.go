package recap

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Read is a recap as the model's answer gave it, with what was left out.
type Read struct {
	Summary   string
	Topics    []string
	Decisions []string
	// Dropped counts the topics and decisions left out: blank, too long,
	// said twice, or past the limit.
	Dropped int
}

// Answer reads the recap from the model's answer. The prompt asks for one
// JSON object and nothing else, {"summary": "...", "topics": [...],
// "decisions": [...]}, and the model seam has no response format yet, so
// the shape is checked on the way in:
//
//   - The text from the first { to the last } is read, so a fenced block or
//     a sentence around the object does no harm.
//   - The summary must be there and not blank. Its spaces are made single,
//     and past Options.SummaryMaxWords words it is cut at a word's end.
//   - Topics and decisions may be missing, which is no topics and no
//     decisions. Each is a string, or an object with a text. A blank one,
//     one longer than Options.ItemMaxChars, one said twice (letter case
//     aside), and those past the limit are left out and counted.
//
// Anything else is ErrAnswer.
func Answer(resp seam.Response, o Options) (Read, error) {
	text := strings.TrimSpace(seam.Text(resp.Message))
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return Read{}, fmt.Errorf("%w: it holds no JSON object", ErrAnswer)
	}
	var raw struct {
		Summary   *string           `json:"summary"`
		Topics    []json.RawMessage `json:"topics"`
		Decisions []json.RawMessage `json:"decisions"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err != nil {
		return Read{}, fmt.Errorf("%w: its JSON does not read", ErrAnswer)
	}
	if raw.Summary == nil || strings.TrimSpace(*raw.Summary) == "" {
		return Read{}, fmt.Errorf("%w: its object has no summary", ErrAnswer)
	}
	out := Read{Summary: words(*raw.Summary, o.SummaryMaxWords), Topics: []string{}, Decisions: []string{}}
	var err error
	if out.Topics, err = items(raw.Topics, o.MaxTopics, o.ItemMaxChars, "topic", &out.Dropped); err != nil {
		return Read{}, err
	}
	if out.Decisions, err = items(raw.Decisions, o.MaxDecisions, o.ItemMaxChars, "decision", &out.Dropped); err != nil {
		return Read{}, err
	}
	return out, nil
}

// words makes a text's spaces single and cuts it after most words, with an
// ellipsis.
func words(text string, most int) string {
	all := strings.Fields(text)
	if most > 0 && len(all) > most {
		return strings.Join(all[:most], " ") + " …"
	}
	return strings.Join(all, " ")
}

// items reads a list of topics or decisions.
func items(list []json.RawMessage, most, longest int, what string, dropped *int) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for i, raw := range list {
		var item string
		if err := json.Unmarshal(raw, &item); err != nil {
			var obj struct {
				Text *string `json:"text"`
			}
			if err := json.Unmarshal(raw, &obj); err != nil || obj.Text == nil {
				return nil, fmt.Errorf("%w: %s %d is neither a text nor an object with a text", ErrAnswer, what, i+1)
			}
			item = *obj.Text
		}
		item = strings.Join(strings.Fields(item), " ")
		key := strings.ToLower(item)
		if item == "" || utf8.RuneCountInString(item) > longest || seen[key] || len(out) >= most {
			*dropped++
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out, nil
}
