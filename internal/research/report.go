package research

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/research/cite"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/subagents"
	"github.com/jonathanmkosar/keystone/internal/subagents/carry"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/web"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// What turns a plan's reply into parts, the parts' chats into numbered
// sources and findings, and a model's report into the one that is saved.
// Nothing here calls a model.

var (
	planLine = regexp.MustCompile(`^\s*(?:\d{1,2}[.)]|[-*•])\s+(.+)$`)
	// A heading that says what follows is the model's own list of sources.
	sourcesHeading = regexp.MustCompile(`(?i)^#{1,6}\s*(sources|references|bibliography|works cited)\s*:?\s*$`)
	summaryHeading = regexp.MustCompile(`(?i)^#{1,6}\s*summary\s*:?\s*$`)
	anyHeading     = regexp.MustCompile(`^#{1,6}\s`)
)

// parsePlan reads the parts out of the plan step's reply: each line of a
// numbered or bulleted list is a part, "title: what to find out". At most
// most are kept. A reply with no such line is one part, the question.
func parsePlan(reply string, most int, title, question string) []PlanPart {
	var parts []PlanPart
	for _, line := range strings.Split(reply, "\n") {
		m := planLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text := strings.TrimSpace(strings.NewReplacer("**", "", "__", "", "`", "").Replace(m[1]))
		if text == "" {
			continue
		}
		p := PlanPart{Task: text}
		if head, rest, ok := strings.Cut(text, ": "); ok && strings.TrimSpace(rest) != "" && len(strings.Fields(head)) <= 12 {
			p.Title, p.Task = strings.TrimSpace(head), strings.TrimSpace(rest)
		}
		if p.Title == "" {
			words := strings.Fields(p.Task)
			if len(words) > 6 {
				words = words[:6]
			}
			p.Title = strings.TrimRight(strings.Join(words, " "), ".,;:")
		}
		parts = append(parts, p)
		if len(parts) == most {
			break
		}
	}
	if len(parts) == 0 {
		return []PlanPart{{Title: title, Task: question}}
	}
	return parts
}

// finding is what one part gave the report.
type finding struct {
	position int
	title    string
	status   string
	// text is the part's result with its citations in the research's own
	// numbers. Empty when it wrote nothing, or when held is set.
	text string
	// held says its result stays in its own chat: it was sent sensitive
	// content and the task's chat is not held (carry.Up).
	held bool
	// why says why it did not finish, when it did not.
	why string
	// atBudget says its budget, or the monthly cap, refused one of its calls.
	atBudget bool
	// outside says its result brings outside content with it.
	outside bool
	// dropped counts the citations in its result that named no source it
	// read.
	dropped int
}

// localSources reads the numbered sources a part's chat was given by its
// recorded web_search and web_fetch calls, by the numbers the tools gave
// them. Only a result that is in the chat record counts: a number a model
// wrote with no such result behind it names nothing.
func localSources(s *tenant.Store, workChatID string) (map[int]cite.Source, error) {
	path, err := chat.Path(s, workChatID)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	found := map[int]cite.Source{}
	for _, m := range path {
		for _, b := range m.Blocks {
			switch b.Type {
			case seam.BlockToolCall:
				names[b.ID] = b.Name
			case seam.BlockToolResult:
				name := names[b.ID]
				if (name != web.SearchName && name != web.FetchName) || !tools.IsOutside(b) {
					continue
				}
				for _, src := range web.Sources(seam.Text(seam.Message{Blocks: b.Content})) {
					if old, ok := found[src.N]; ok {
						// The same number again is the same source, read in full.
						old.Fetched = old.Fetched || (name == web.FetchName && old.URL == src.URL)
						found[src.N] = old
						continue
					}
					found[src.N] = cite.Source{N: src.N, Title: src.Title, URL: src.URL, Fetched: name == web.FetchName}
				}
			}
		}
	}
	return found, nil
}

// gather numbers the sources the parts read once across all of them, by
// part and then by the number the part's tools gave, a page read by two
// parts once, and gives each part's result with its citations changed to
// those numbers. parent is the marks of the task's own chat: a part that
// was sent sensitive content gives nothing to a chat that is not held.
func gather(s *tenant.Store, parent carry.Marks, parts []subagents.Subagent, most int) ([]cite.Source, []finding, error) {
	var sources []cite.Source
	byURL := map[string]int{}
	var findings []finding
	for _, a := range parts {
		f := finding{position: a.Position, title: a.Title, status: a.Status}
		if a.Error != nil {
			f.why = strings.TrimSpace(a.Error.Message)
			f.atBudget = a.Error.Code == string(seam.ErrSpendCap)
		}
		f.atBudget = f.atBudget || a.Notice != ""
		pass, outside := true, false
		if subagents.Ended(a) {
			pass, outside = carry.Up(parent, carry.Marks{Outside: a.Outside, Sensitive: a.Sensitive})
		}
		if !pass {
			f.held = true
			findings = append(findings, f)
			continue
		}
		local, err := localSources(s, a.WorkChatID)
		if err != nil {
			return nil, nil, err
		}
		numbers := make([]int, 0, len(local))
		for n := range local {
			numbers = append(numbers, n)
		}
		sort.Ints(numbers)
		to := map[int]int{}
		for _, n := range numbers {
			src := local[n]
			g, ok := byURL[src.URL]
			if !ok {
				src.N, src.Part = len(sources)+1, a.Position
				sources = append(sources, src)
				g = src.N
				byURL[src.URL] = g
			} else if src.Fetched {
				sources[g-1].Fetched = true
			}
			to[n] = g
		}
		text := strings.TrimSpace(a.Result)
		if r := []rune(text); most > 0 && len(r) > most {
			text = string(r[:most]) + "\n[Cut here: the rest is in the part's own chat.]"
		}
		var dropped []int
		f.text, dropped = cite.Remap(text, to)
		f.dropped = len(dropped)
		f.outside = outside || len(local) > 0
		findings = append(findings, f)
	}
	return sources, findings, nil
}

// statusWords says how a part ended, for the report's writer and for the
// person.
func statusWords(f finding) string {
	switch f.status {
	case subagents.StatusDone:
		return "done"
	case subagents.StatusStopped:
		return "stopped before it finished"
	case subagents.StatusPaused:
		return "cut short when every agent was paused"
	case subagents.StatusFailed:
		return "did not finish"
	}
	return "still working"
}

// fill puts a task's numbers into a prompt from the settings.
func fill(prompt string, j row) string {
	return strings.NewReplacer("{parts}", fmt.Sprint(j.maxParts), "{sources}", fmt.Sprint(j.sourcesPerPart), "{words}", fmt.Sprint(j.reportWords)).Replace(prompt)
}

// planMessage is the message of the plan step.
func planMessage(c Config, j row) string {
	return fill(c.PlanPrompt, j) + "\n\nThe research question:\n" + j.Question
}

// partContext is what each part is given beside its own task.
func partContext(c Config, j row) string {
	return fill(c.PartBrief, j) + "\n\nThe research question:\n" + j.Question
}

// writeMessage is the message of the report step: the prompt, the
// question, what each part found, and the sources that were read.
func writeMessage(c Config, j row, findings []finding, sources []cite.Source) string {
	var b strings.Builder
	b.WriteString(fill(c.ReportPrompt, j))
	b.WriteString("\n\nThe research question:\n" + j.Question)
	b.WriteString("\n\nWhat the parts found:")
	for _, f := range findings {
		fmt.Fprintf(&b, "\n\nPart %d of %d, “%s” (%s):\n", f.position, len(findings), f.title, statusWords(f))
		switch {
		case f.held:
			b.WriteString("It was sent content from a sensitive space, so what it found stays in its own chat and is not given here.")
		case f.text != "":
			b.WriteString(f.text)
		default:
			b.WriteString("It found nothing.")
		}
		if f.why != "" && !f.held {
			b.WriteString("\n(" + f.why + ")")
		}
	}
	if len(sources) == 0 {
		b.WriteString("\n\nNo source was read. Write only what the findings say, say plainly that no source backs it, and cite nothing.")
		return b.String()
	}
	b.WriteString("\n\nThe sources that were read, by number:")
	for _, src := range sources {
		fmt.Fprintf(&b, "\n\n[%d] %s\n%s", src.N, src.Title, src.URL)
	}
	return b.String()
}

// composed is a report as it is saved.
type composed struct {
	report  string
	summary string
	cited   []cite.Source
	dropped int
}

// compose makes the report that is saved from the body a model wrote, or
// that Keystone put together: the citations checked against the sources the
// parts read and renumbered (cite.Check), a title, the list of the sources
// cited, written here and not by a model, and what the person should know
// about how it was made.
//
// earlier counts the citations already taken out of the parts' findings,
// which named no source their part read.
func compose(j row, body string, read []cite.Source, about []string, earlier, summaryChars int) composed {
	body = stripSources(strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n")))
	body, cited, dropped := cite.Check(body, read)
	var b strings.Builder
	if !strings.HasPrefix(body, "# ") {
		b.WriteString("# " + j.Title + "\n\n")
		if strings.TrimSpace(j.Question) != j.Title {
			b.WriteString("> " + strings.Join(strings.Fields(j.Question), " ") + "\n\n")
		}
	}
	b.WriteString(body)
	b.WriteString("\n\n## Sources\n\n")
	if len(cited) == 0 {
		b.WriteString("This report cites no source.\n")
	}
	for _, src := range cited {
		fmt.Fprintf(&b, "%d. [%s](%s)", src.N, linkText(src.Title), linkAddress(src.URL))
		if !src.Fetched {
			b.WriteString(" (read as a search result's extract)")
		}
		b.WriteString("\n")
	}
	lines := []string{fmt.Sprintf("Keystone's research mode wrote this from %s, which read %s between them; %s cited above.",
		count(len(j.Plan), "part", "parts"), count(len(read), "source", "sources"), countAre(len(cited)))}
	lines = append(lines, about...)
	n := len(dropped) + earlier
	if n > 0 {
		lines = append(lines, fmt.Sprintf("%s named no source that was read, and %s taken out.", count(n, "citation", "citations"), wasWere(n)))
	}
	b.WriteString("\n## About this report\n\n")
	for _, l := range lines {
		b.WriteString("- " + l + "\n")
	}
	return composed{report: b.String(), summary: summaryOf(body, summaryChars), cited: cited, dropped: n}
}

// fallbackBody is a report put together by Keystone when no model could
// write one: why, and what each part found, as it wrote it.
func fallbackBody(why string, findings []finding) string {
	var b strings.Builder
	b.WriteString("## Summary\n\n" + why + " What each part of the research found follows, as the part wrote it.")
	for _, f := range findings {
		fmt.Fprintf(&b, "\n\n## Part %d: %s\n\n", f.position, f.title)
		switch {
		case f.held:
			b.WriteString("This part was sent content from a sensitive space, so what it found stays in its own chat.")
		case f.text != "":
			b.WriteString(f.text)
		default:
			b.WriteString("This part found nothing.")
		}
		if f.status != subagents.StatusDone {
			b.WriteString("\n\nThis part " + statusWords(f) + ".")
		}
	}
	return b.String()
}

// stripSources takes off a list of sources a model wrote at the end of its
// report though it was asked not to: Keystone writes the list, from the
// sources that were read.
func stripSources(body string) string {
	lines := strings.Split(body, "\n")
	last := -1
	for i, l := range lines {
		if anyHeading.MatchString(l) {
			last = i
		}
	}
	if last > 0 && sourcesHeading.MatchString(strings.TrimSpace(lines[last])) {
		return strings.TrimSpace(strings.Join(lines[:last], "\n"))
	}
	return body
}

// summaryOf is what the note says of a report: the text under its Summary
// heading, or its first paragraph, on one line, cut at most characters.
func summaryOf(body string, most int) string {
	lines := strings.Split(body, "\n")
	start := -1
	for i, l := range lines {
		if summaryHeading.MatchString(strings.TrimSpace(l)) {
			start = i + 1
			break
		}
	}
	var picked []string
	if start >= 0 {
		for _, l := range lines[start:] {
			if anyHeading.MatchString(l) {
				break
			}
			picked = append(picked, l)
		}
	} else {
		for _, l := range lines {
			t := strings.TrimSpace(l)
			if t == "" && len(picked) > 0 {
				break
			}
			if t == "" || anyHeading.MatchString(l) || strings.HasPrefix(t, ">") {
				continue
			}
			picked = append(picked, l)
		}
	}
	text := strings.Join(strings.Fields(strings.Join(picked, " ")), " ")
	r := []rune(text)
	if most > 0 && len(r) > most {
		cut := string(r[:most])
		if i := strings.LastIndexAny(cut, " "); i > most/2 {
			cut = cut[:i]
		}
		text = strings.TrimRight(cut, " ,;:") + "…"
	}
	return text
}

// linkText is a source's title as the text of a Markdown link.
func linkText(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	return strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`).Replace(title)
}

// linkAddress is a source's address as the target of a Markdown link.
func linkAddress(address string) string {
	return strings.NewReplacer("(", "%28", ")", "%29", " ", "%20", "<", "%3C", ">", "%3E").Replace(address)
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func countAre(n int) string {
	if n == 1 {
		return "1 is"
	}
	return fmt.Sprintf("%d are", n)
}

func wasWere(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

// budgetLine says a task reached its budget, for the report and the note.
func budgetLine(j row, spent int64) string {
	return fmt.Sprintf("The research reached its budget of %s, having spent %s, so it stopped there: this report is what it had found.",
		usage.Dollars(j.BudgetMicros), usage.Dollars(spent))
}
