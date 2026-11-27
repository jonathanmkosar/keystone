package settings

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The settings screen shows each setting by its title and its summary, and
// keeps the description one step away (P11-04). These are the shapes the two
// must have. They are the registry's own rules, as the shape of a key is,
// and not settings.
const (
	// titleMaxChars is the longest a title may be. A title is a name, and a
	// longer one wraps to a third line on a phone.
	titleMaxChars = 40
	// summaryMaxChars and summaryMaxWords keep a summary to one short line:
	// about ten words, which is two lines at most on a phone.
	summaryMaxChars = 80
	summaryMaxWords = 14
	summaryMinWords = 3
)

// keyInWords finds a setting's key, or a part of one, in words meant for a
// person: lower-case words joined by a dot or an underscore.
var keyInWords = regexp.MustCompile(`[a-z0-9]_[a-z0-9]|[a-z0-9]\.[a-z]|[{}]`)

// CheckWords lists what is wrong with the titles and summaries of a
// registry, one line for each fault, and nothing when all is well.
//
// Reading the registry does not ask for them, so a setting added without
// them still loads and still has a control: the screen makes a title from
// the key and shows the first sentence of the description. This is the
// check that asks, and verify's settings registry check runs it.
//
// A title is the setting's readable name: 1 to 40 characters, starting with
// a capital or a digit, with no full stop at its end and no key in it, and
// no other setting has the same one. A summary is one short plain line on
// what the setting does: 3 to 14 words, 80 characters at most, ending in a
// full stop, with no key and no placeholder in it.
func CheckWords(reg []Setting) []string {
	var problems []string
	titles := map[string]string{}
	for _, s := range reg {
		where := s.Key
		if s.Lane != "" {
			where = "settings/" + s.Lane + ".json: " + s.Key
		}
		if p := titleProblem(s.Title); p != "" {
			problems = append(problems, fmt.Sprintf(`%s %s. Add a "title" line under its "key" line: the setting's name in plain words, %d characters at most, such as "Budget for each scheduled run"`, where, p, titleMaxChars))
		} else if other, taken := titles[strings.ToLower(s.Title)]; taken {
			problems = append(problems, fmt.Sprintf("%s has the title %q, and so has %s. Each setting has a title of its own", where, s.Title, other))
		} else {
			titles[strings.ToLower(s.Title)] = s.Key
		}
		if p := summaryProblem(s.Summary); p != "" {
			problems = append(problems, fmt.Sprintf(`%s %s. Add a "summary" line under its "key" line: one short plain sentence on what the setting does, %d words and %d characters at most, such as "The most one scheduled run may spend on paid models."`, where, p, summaryMaxWords, summaryMaxChars))
		}
	}
	return problems
}

// titleProblem says what is wrong with a title, or nothing.
func titleProblem(title string) string {
	switch first, _ := utf8.DecodeRuneInString(title); {
	case strings.TrimSpace(title) == "":
		return "has no title"
	case title != strings.TrimSpace(title):
		return "has a title with a space at an end"
	case utf8.RuneCountInString(title) > titleMaxChars:
		return fmt.Sprintf("has a title of %d characters", utf8.RuneCountInString(title))
	case !unicode.IsUpper(first) && !unicode.IsDigit(first):
		return "has a title that does not start with a capital"
	case strings.HasSuffix(title, "."):
		return "has a title that ends in a full stop, and a title is a name"
	case keyInWords.MatchString(title):
		return "has a title with a key in it"
	}
	return ""
}

// summaryProblem says what is wrong with a summary, or nothing.
func summaryProblem(summary string) string {
	words := len(strings.Fields(summary))
	switch {
	case strings.TrimSpace(summary) == "":
		return "has no summary"
	case summary != strings.TrimSpace(summary):
		return "has a summary with a space at an end"
	case utf8.RuneCountInString(summary) > summaryMaxChars:
		return fmt.Sprintf("has a summary of %d characters", utf8.RuneCountInString(summary))
	case words > summaryMaxWords || words < summaryMinWords:
		return fmt.Sprintf("has a summary of %d words", words)
	case !strings.HasSuffix(summary, "."):
		return "has a summary that does not end in a full stop"
	case keyInWords.MatchString(summary):
		return "has a summary with a key or a placeholder in it"
	}
	return ""
}
