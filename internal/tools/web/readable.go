package web

import (
	"html"
	"regexp"
	"strings"
)

// readable turns an HTML page into its title and the text a person would
// read: what is in scripts, styles and the like is dropped, block elements
// become line breaks, tags go, entities are decoded and spaces are
// collapsed. It is a reader for a model, not a browser: a page built by
// script may come back nearly empty.
func readable(page string) (title, text string) {
	if m := titlePattern.FindStringSubmatch(page); m != nil {
		title = oneLine(html.UnescapeString(tagPattern.ReplaceAllString(m[1], " ")))
	}
	page = commentPattern.ReplaceAllString(page, " ")
	for _, p := range droppedPatterns {
		page = p.ReplaceAllString(page, " ")
	}
	page = breakPattern.ReplaceAllString(page, "\n")
	page = tagPattern.ReplaceAllString(page, " ")
	return title, plain(html.UnescapeString(page))
}

// plain tidies text: each line's runs of spaces become one, lines are
// trimmed, and no more than one empty line is kept between paragraphs.
func plain(text string) string {
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n", " ", " ").Replace(text)
	var lines []string
	empty := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			empty++
			continue
		}
		if empty > 0 && len(lines) > 0 {
			lines = append(lines, "")
		}
		empty = 0
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// oneLine collapses text into one line.
func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

var (
	titlePattern   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title\s*>`)
	commentPattern = regexp.MustCompile(`(?s)<!--.*?-->`)
	tagPattern     = regexp.MustCompile(`(?s)<[^>]*>`)
	breakPattern   = regexp.MustCompile(`(?i)<\s*/?\s*(p|div|br|hr|li|ul|ol|dl|dt|dd|h[1-6]|tr|td|th|table|thead|tbody|section|article|header|footer|nav|main|aside|blockquote|pre|figure|figcaption|form|fieldset|address|details|summary)\b[^>]*>`)

	// droppedPatterns are the elements whose content is not text a person
	// reads. Go's regular expressions have no back references, so each
	// element has a pattern of its own.
	droppedPatterns = func() []*regexp.Regexp {
		var out []*regexp.Regexp
		for _, tag := range []string{"head", "script", "style", "noscript", "template", "svg", "math", "iframe", "object", "canvas", "select", "button", "nav"} {
			out = append(out, regexp.MustCompile(`(?is)<`+tag+`\b[^>]*>.*?</`+tag+`\s*>`))
		}
		return out
	}()
)
