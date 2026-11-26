// Package scope is the guard of a wiki upkeep job (P8-06): what model may
// serve one, and what one run's answer may write.
//
// It holds two of the fixed rules for the jobs and nothing else:
//
//   - Fixed rule 3: content from a sensitive space reaches only models the
//     registry marks local or trusted. A wiki job reads the person's
//     knowledge store, which may hold sensitive pages, so every call of a
//     wiki run goes to a local or trusted model, and to no other (MayServe).
//     A page that cites a source from a sensitive space is written only to
//     a sensitive space (Check).
//   - Fixed rule 2: a job's writes go only to the knowledge store, through
//     the job's own grant. Check refuses an answer that asks for anything
//     the job's grant does not allow, or more than one run may do, and then
//     nothing of it is written.
//
// The package imports nothing of Keystone's, so it can read no setting: the
// fixed rules check in verify fails if that ever changes. What a caller
// reads from settings or from the store comes in as arguments.
package scope

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The registry's classes, as text, so this package needs nothing of the
// registry's.
const (
	ClassLocal   = "local"
	ClassTrusted = "trusted"
)

// MayServe reports whether a model of this class may be sent any call of a
// wiki run: only a local or a trusted one. Anything else, and a class this
// package does not know, may not.
func MayServe(class string) bool {
	return class == ClassLocal || class == ClassTrusted
}

// ErrRefused says an answer asked for something the job may not do.
// Nothing of such an answer is written.
var ErrRefused = errors.New("the answer was refused")

// Actions are what a job's grant lets it write. Reading is not here: what
// a job may read is the tools it is offered.
type Actions struct {
	// Write writes pages: a new page, or a change to one.
	Write bool
	// Delete deletes pages.
	Delete bool
	// Raise raises issues, and notes them.
	Raise bool
	// Close closes issues, and notes them.
	Close bool
}

// Limits are what one run may write, from the person's settings.
type Limits struct {
	// Batches is the most batches one answer may hold.
	Batches int
	// NewPages is the most new pages one answer may make.
	NewPages int
}

// ID is a page's, a source's or an issue's id, as a store gives it. A
// model may send it as a number or as text; it is kept as text.
type ID string

// UnmarshalJSON reads a number or a string.
func (id *ID) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		*id = ""
		return nil
	}
	var s string
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
	} else {
		var n json.Number
		if err := json.Unmarshal(raw, &n); err != nil {
			return fmt.Errorf("an id is a number or a text")
		}
		s = n.String()
	}
	*id = ID(strings.TrimSpace(s))
	return nil
}

// Plan is what one run's answer asks Keystone to write.
type Plan struct {
	Batches []Batch `json:"batches"`
	Issues  []Issue `json:"issues"`
	// Report is the job's own summary of what it did, for the person.
	Report string `json:"report"`
}

// Batch is one piece of work, written as one change.
type Batch struct {
	Note    string   `json:"note"`
	Writes  []Write  `json:"writes"`
	Deletes []Delete `json:"deletes"`
}

// Write is one page written: a new one when PageID is empty.
type Write struct {
	// Ref names a new page within its batch, so another page of the batch
	// can link to it as [[new:ref]] or [[new:ref|shown text]] before its id
	// is known. Keystone puts the id in once the page is written.
	Ref        string            `json:"ref,omitempty"`
	PageID     ID                `json:"page_id"`
	BaseRev    ID                `json:"base_rev"`
	Space      string            `json:"space"`
	Path       string            `json:"path"`
	Title      string            `json:"title"`
	Body       string            `json:"body"`
	Properties map[string]string `json:"properties,omitempty"`
	Sources    []ID              `json:"sources"`
}

// New reports whether the write makes a page.
func (w Write) New() bool { return w.PageID == "" || w.PageID == "0" }

// Delete is one page deleted.
type Delete struct {
	PageID  ID `json:"page_id"`
	BaseRev ID `json:"base_rev"`
}

// Issue is one change to an issue: exactly one of Raise, Note and Close.
type Issue struct {
	Raise *Raise      `json:"raise,omitempty"`
	Note  *IssueNote  `json:"note,omitempty"`
	Close *IssueClose `json:"close,omitempty"`
}

// Raise raises an issue on a page.
type Raise struct {
	PageID       ID     `json:"page_id"`
	Title        string `json:"title"`
	Quote        string `json:"quote,omitempty"`
	Body         string `json:"body,omitempty"`
	Urgent       bool   `json:"urgent,omitempty"`
	UrgentReason string `json:"urgent_reason,omitempty"`
}

// IssueNote adds a note to an issue.
type IssueNote struct {
	Issue ID     `json:"issue_id"`
	Text  string `json:"text"`
}

// IssueClose closes an issue.
type IssueClose struct {
	Issue     ID     `json:"issue_id"`
	Reason    string `json:"reason"`
	Changeset ID     `json:"changeset,omitempty"`
}

// Store is what Check is told about the store the answer would write to,
// read by the caller before it asks: each source a write cites, and each
// space a page would be written to.
type Store struct {
	// Sources are the sources the job may cite, by id, with true for one
	// in a sensitive space. A withdrawn source is not here.
	Sources map[ID]bool
	// Spaces are the spaces pages would be written to, with true for a
	// sensitive one. A space not here counts as normal: a store makes a
	// space it does not hold as a normal one.
	Spaces map[string]bool
}

// closeReasons are the reasons an issue is closed with.
var closeReasons = map[string]bool{"fixed": true, "wont_fix": true, "duplicate": true, "not_a_problem": true}

// Check says whether every part of an answer may be written. A nil answer
// lets all of it be written; any other refuses all of it, with ErrRefused
// and the first reason, in words a person reads.
func Check(may Actions, lim Limits, p Plan, st Store) error {
	refuse := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, args...))
	}
	if len(p.Batches) > lim.Batches {
		return refuse("it holds %d batches, and one run may write %d at most", len(p.Batches), lim.Batches)
	}
	newPages := 0
	for i, b := range p.Batches {
		n := i + 1
		if len(b.Writes) == 0 && len(b.Deletes) == 0 {
			return refuse("batch %d writes nothing", n)
		}
		if len(b.Writes) > 0 && !may.Write {
			return refuse("batch %d writes pages, and this job's grant does not let it", n)
		}
		if len(b.Deletes) > 0 && !may.Delete {
			return refuse("batch %d deletes pages, and this job's grant does not let it", n)
		}
		refs := map[string]bool{}
		for _, w := range b.Writes {
			if w.Ref == "" {
				continue
			}
			if !w.New() || refs[w.Ref] {
				return refuse("batch %d names %q twice, or names a page that is not new", n, w.Ref)
			}
			refs[w.Ref] = true
		}
		for _, w := range b.Writes {
			for _, ref := range NewLinks(w.Body) {
				if !refs[ref] {
					return refuse("the page %s links to [[new:%s]], and no new page of its batch is named %s", w.Path, ref, ref)
				}
			}
			switch {
			case strings.TrimSpace(w.Space) == "" || strings.TrimSpace(w.Path) == "" || strings.TrimSpace(w.Title) == "":
				return refuse("batch %d has a page with no space, path or title", n)
			case strings.TrimSpace(w.Body) == "":
				return refuse("batch %d has a page with no text: %s", n, w.Path)
			case !w.New() && w.BaseRev == "":
				return refuse("batch %d changes page %s without the revision it read", n, w.PageID)
			}
			if w.New() {
				newPages++
			}
			for _, src := range w.Sources {
				sensitive, ok := st.Sources[src]
				if !ok {
					return refuse("the page %s cites source %s, which this job may not cite: it is not in the store as the job sees it, or it was withdrawn", w.Path, src)
				}
				// Fixed rule 3: what a sensitive source says stays in a
				// sensitive space.
				if sensitive && !st.Spaces[w.Space] {
					return refuse("the page %s in the space %s cites source %s, which is from a sensitive space, and %s is not one", w.Path, w.Space, src, w.Space)
				}
			}
		}
		for _, d := range b.Deletes {
			if d.PageID == "" || d.BaseRev == "" {
				return refuse("batch %d deletes a page without its id and the revision it read", n)
			}
		}
	}
	if newPages > lim.NewPages {
		return refuse("it makes %d new pages, and one run may make %d at most", newPages, lim.NewPages)
	}
	for i, is := range p.Issues {
		n := i + 1
		count := 0
		for _, set := range []bool{is.Raise != nil, is.Note != nil, is.Close != nil} {
			if set {
				count++
			}
		}
		if count != 1 {
			return refuse("issue change %d must be one of raise, note and close", n)
		}
		switch {
		case is.Raise != nil:
			if !may.Raise {
				return refuse("it raises an issue, and this job's grant does not let it")
			}
			if is.Raise.PageID == "" || strings.TrimSpace(is.Raise.Title) == "" {
				return refuse("issue change %d raises an issue with no page or no title", n)
			}
			if is.Raise.Urgent && strings.TrimSpace(is.Raise.UrgentReason) == "" {
				return refuse("issue change %d marks an issue urgent and says not why", n)
			}
		case is.Note != nil:
			if !may.Raise && !may.Close {
				return refuse("it notes an issue, and this job's grant does not let it")
			}
			if is.Note.Issue == "" || strings.TrimSpace(is.Note.Text) == "" {
				return refuse("issue change %d notes no issue, or says nothing", n)
			}
		case is.Close != nil:
			if !may.Close {
				return refuse("it closes an issue, and this job's grant does not let it")
			}
			if is.Close.Issue == "" || !closeReasons[is.Close.Reason] {
				return refuse("issue change %d closes no issue, or gives a reason that is not fixed, wont_fix, duplicate or not_a_problem", n)
			}
			if is.Close.Reason == "fixed" && is.Close.Changeset == "" {
				return refuse("issue change %d closes an issue as fixed without the change that fixed it", n)
			}
		}
	}
	return nil
}

// Empty reports whether an answer asks for nothing to be written.
func (p Plan) Empty() bool { return len(p.Batches) == 0 && len(p.Issues) == 0 }

// newLink is a link to a new page of the same batch, by its ref.
var newLink = regexp.MustCompile(`\[\[new:([A-Za-z0-9_.-]+)(\|[^\]]*)?\]\]`)

// NewLinks returns the refs a page's text links to as [[new:ref]].
func NewLinks(body string) []string {
	var out []string
	for _, m := range newLink.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

// Resolve puts each new page's id in for its ref, as the store's link
// [[page:id]] or [[page:id|shown text]]. A ref with no id yet becomes its
// shown text, or the ref, so no store is sent a link it does not know, and
// done says whether every ref had its id.
func Resolve(body string, ids map[string]string) (out string, done bool) {
	done = true
	out = newLink.ReplaceAllStringFunc(body, func(m string) string {
		sub := newLink.FindStringSubmatch(m)
		id, ok := ids[sub[1]]
		if !ok {
			done = false
			if sub[2] != "" {
				return strings.TrimPrefix(sub[2], "|")
			}
			return sub[1]
		}
		return "[[page:" + id + sub[2] + "]]"
	})
	return out, done
}
