package web

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
)

// Numbered citations. Each source a turn reads, a search result or a
// fetched page, gets a number, and the model cites it as [n]. Numbers run
// on through one turn, so a second search, or a page fetched after a
// search, carries on from the last number, and [n] names one source in
// the reply. The chat screen reads the numbers back from the results and
// links each [n] in the reply to its source.
//
// The form of a result is the contract with the screen
// (web/app/src/citations.tsx), and Sources is its reader on this side:
//
//	[Outside content] <one paragraph that says what this is>
//
//	[1] <title, one line>
//	<the address, one line>
//	<a search result's text, one line>
//
//	[2] …
//
// A fetched page has one such block, with no text in it, and then a block
// that says the page's text follows. The reader stops at the first block
// that is not a source, so nothing in a page's text can add a source.

// Source is one numbered source in a result.
type Source struct {
	N     int
	Title string
	URL   string
}

var (
	sourceHead = regexp.MustCompile(`^\[(\d{1,4})\] (.+)$`)
	sourceURL  = regexp.MustCompile(`^https?://\S+$`)
)

// Sources reads the numbered sources out of a web tool's result text. A
// text that is not outside content has none.
func Sources(text string) []Source {
	if !strings.HasPrefix(text, tools.OutsideMark) {
		return nil
	}
	blocks := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n")
	var out []Source
	for _, b := range blocks[1:] {
		lines := strings.Split(b, "\n")
		if len(lines) < 2 {
			break
		}
		head := sourceHead.FindStringSubmatch(lines[0])
		if head == nil || !sourceURL.MatchString(lines[1]) {
			break
		}
		n, _ := strconv.Atoi(head[1])
		out = append(out, Source{N: n, Title: head[2], URL: lines[1]})
	}
	return out
}

// sourceBlock writes one source: its number and title, its address, and
// its text when it has one. Each part is one line, so a source cannot
// break into blocks of its own.
func sourceBlock(n int, title, address, text string) string {
	title = cutRunes(oneLine(title), maxTitle)
	if title == "" {
		title = address
	}
	b := fmt.Sprintf("[%d] %s\n%s", n, title, address)
	if text = oneLine(text); text != "" {
		b += "\n" + text
	}
	return b
}

func cutRunes(text string, most int) string {
	r := []rune(text)
	if most <= 0 || len(r) <= most {
		return text
	}
	return strings.TrimSpace(string(r[:most-1])) + "…"
}

// counter hands out the numbers of a turn's sources. It keeps the next
// number of each chat's turn in memory, keyed by the message the turn
// answers, so calls in one step, which are stored together only once
// they are all done, still get numbers of their own. After a restart it
// reads the numbers the turn has already used from the chat record.
type counter struct {
	mu    sync.Mutex
	turns map[string]turnCount
}

type turnCount struct {
	anchor int64 // the id of the message the person wrote, which the turn answers
	next   int
}

// most chats remembered before the counter starts again.
const counterChats = 4096

// take reserves n numbers for one result of the turn running in chatID,
// and returns the first.
func (c *counter) take(s *tenant.Store, chatID string, n int) int {
	anchor, used := turnSoFar(s, chatID)
	key := s.TenantID() + "/" + chatID
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.turns == nil || len(c.turns) >= counterChats {
		c.turns = map[string]turnCount{}
	}
	t := c.turns[key]
	if t.anchor != anchor {
		t = turnCount{anchor: anchor, next: 1}
	}
	if t.next <= used {
		t.next = used + 1
	}
	first := t.next
	t.next += n
	c.turns[key] = t
	return first
}

// turnSoFar returns the id of the last message the person wrote in the
// chat, which the running turn answers, and the highest source number the
// turn's stored results have used. A chat that cannot be read gives zero
// for both.
func turnSoFar(s *tenant.Store, chatID string) (anchor int64, used int) {
	path, err := chat.Path(s, chatID)
	if err != nil {
		return 0, 0
	}
	start := 0
	for i, m := range path {
		if m.Role == seam.RoleUser && wrote(m.Blocks) {
			anchor, start = m.ID, i+1
		}
	}
	for _, m := range path[start:] {
		for _, b := range m.Blocks {
			if !tools.IsOutside(b) {
				continue
			}
			for _, src := range Sources(seam.Text(seam.Message{Blocks: b.Content})) {
				used = max(used, src.N)
			}
		}
	}
	return anchor, used
}

// wrote reports whether a user message is one the person wrote, and not a
// set of tool results.
func wrote(blocks []seam.Block) bool {
	for _, b := range blocks {
		if b.Type != seam.BlockToolResult {
			return true
		}
	}
	return false
}
