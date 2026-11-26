package wikijobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/wikijobs/scope"
)

// A job writes nothing with a tool. It ends its run with one JSON answer,
// and once the run is done Keystone checks the answer against the job's
// grant (scope.Check) and writes it through that grant: Waymarks batches
// under the job's own token, or the built-in store's pages. Each write is
// two entries in the person's audit log, as a tool call is, under the
// run's request id. Decision 0441 says why.

// errAnswer says a reply holds no answer in the shape the instructions give.
var errAnswer = errors.New("the answer does not read")

// ParseAnswer reads a job's last reply: the JSON object in its last ```json
// block, or else the text from its first { to its last }. A field the
// instructions do not give is refused, so a misspelt part is never lost
// without a word.
func ParseAnswer(reply string) (scope.Plan, error) {
	body := reply
	if i := strings.LastIndex(reply, "```json"); i >= 0 {
		rest := reply[i+len("```json"):]
		if j := strings.Index(rest, "```"); j >= 0 {
			body = rest[:j]
		} else {
			body = rest
		}
	}
	start, end := strings.Index(body, "{"), strings.LastIndex(body, "}")
	if start < 0 || end < start {
		return scope.Plan{}, fmt.Errorf("%w: the reply holds no JSON object", errAnswer)
	}
	dec := json.NewDecoder(strings.NewReader(body[start : end+1]))
	dec.DisallowUnknownFields()
	var p scope.Plan
	if err := dec.Decode(&p); err != nil {
		return scope.Plan{}, fmt.Errorf("%w: %v", errAnswer, err)
	}
	return p, nil
}

// Written is one thing a run wrote, for the run's note and its record.
type Written struct {
	What   string `json:"what"`   // batch, page, delete, raise, note or close
	ID     string `json:"id"`     // the page's, batch's, changeset's or issue's id
	Detail string `json:"detail"` // a few words a person reads
}

// recorder writes one call's two audit entries, as a tool call's.
type recorder struct {
	s         *tenant.Store
	requestID string
	actor     string
}

func (rc recorder) record(ctx context.Context, action string, sent any, do func() (any, error)) (any, error) {
	raw, _ := json.Marshal(sent)
	entry := audit.Entry{Kind: audit.KindTool, RequestID: rc.requestID, Actor: rc.actor, Action: action, Outcome: audit.OutcomeStarted, Sent: string(raw)}
	if _, err := audit.Append(context.Background(), rc.s, entry); err != nil {
		return nil, fmt.Errorf("the write could not be recorded, so it was not made: %w", err)
	}
	began := time.Now()
	got, err := do()
	entry.Sent, entry.DurationMS, entry.Outcome = "", time.Since(began).Milliseconds(), audit.OutcomeOK
	var rec any = got
	if err != nil {
		entry.Outcome = audit.OutcomeError
		var r *refusal
		if errors.As(err, &r) {
			entry.Outcome = audit.OutcomeRefused
		}
		rec = map[string]string{"error": err.Error()}
	}
	out, _ := json.Marshal(rec)
	entry.Received = string(out)
	if _, aerr := audit.Append(context.Background(), rc.s, entry); aerr != nil && err == nil {
		return got, fmt.Errorf("the write was made, and its end could not be recorded: %w", aerr)
	}
	return got, err
}

// num reads an id as Waymarks' whole number.
func num(id scope.ID) (int64, error) {
	if id == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(string(id), 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: %q is not one of Waymarks' ids, a whole number", scope.ErrRefused, id)
	}
	return n, nil
}

// waymarksStore reads what Check needs to know of Waymarks, under the job's
// own token: each source the answer cites, whether it is the job's to cite
// and whether it is sensitive, and the label of each space.
func waymarksStore(ctx context.Context, s *tenant.Store, c *client, p scope.Plan) (scope.Store, error) {
	st := scope.Store{Sources: map[scope.ID]bool{}}
	gone, err := knowledge.Withdrawn(ctx, s, StoreWaymarks)
	if err != nil {
		return st, err
	}
	if st.Spaces, err = c.spaces(ctx); err != nil {
		return st, err
	}
	for _, b := range p.Batches {
		for _, w := range b.Writes {
			for _, id := range w.Sources {
				n, err := num(id)
				if err != nil {
					return st, err
				}
				if _, seen := st.Sources[id]; seen || gone[string(id)] || n < 1 {
					continue
				}
				var rec struct {
					Space string `json:"space"`
				}
				r, err := c.into(ctx, http.MethodGet, fmt.Sprintf("/v1/sources/%d", n), nil, nil, &rec)
				var ref *refusal
				if errors.As(err, &ref) {
					continue // not the job's to read: Check refuses citing it
				}
				if err != nil {
					return st, err
				}
				st.Sources[id] = r.sensitive || st.Spaces[rec.Space]
			}
		}
	}
	return st, nil
}

// applyWaymarks writes a checked answer to Waymarks under the job's token,
// batch by batch. A write Waymarks refuses drops its batch, and nothing
// after it is written; what was written before it stays.
func applyWaymarks(ctx context.Context, c *client, rc recorder, p scope.Plan) ([]Written, error) {
	var out []Written
	call := func(action, method, path string, body any, v any) error {
		_, err := rc.record(ctx, action, body, func() (any, error) {
			r, err := c.into(ctx, method, path, nil, body, v)
			if err != nil {
				return nil, err
			}
			return json.RawMessage(r.data), nil
		})
		return err
	}
	for i, b := range p.Batches {
		var batch struct {
			ID int64 `json:"id"`
		}
		if err := call("waymarks_open_batch", http.MethodPost, "/v1/batches", map[string]string{"note": b.Note}, &batch); err != nil {
			return out, fmt.Errorf("batch %d could not be opened: %w", i+1, err)
		}
		drop := func(err error) ([]Written, error) {
			_ = call("waymarks_drop_batch", http.MethodPost, fmt.Sprintf("/v1/batches/%d/drop", batch.ID), map[string]string{"reason": "Keystone stopped the batch: " + err.Error()}, nil)
			return out, fmt.Errorf("batch %d (%s) was dropped: %w", i+1, b.Note, err)
		}
		ids := map[string]string{}
		var again []int
		write := func(w scope.Write, body string) (int64, error) {
			pageID, err := num(w.PageID)
			if err != nil {
				return 0, err
			}
			rev, err := num(w.BaseRev)
			if err != nil {
				return 0, err
			}
			sources := []int64{}
			for _, id := range w.Sources {
				n, err := num(id)
				if err != nil {
					return 0, err
				}
				sources = append(sources, n)
			}
			sent := map[string]any{"page_id": pageID, "base_rev": rev, "space": w.Space, "path": w.Path, "title": w.Title, "body": body, "sources": sources}
			if len(w.Properties) > 0 {
				sent["properties"] = w.Properties
			}
			var staged struct {
				PageID int64 `json:"page_id"`
			}
			err = call("waymarks_write_page", http.MethodPost, fmt.Sprintf("/v1/batches/%d/writes", batch.ID), sent, &staged)
			return staged.PageID, err
		}
		for k, w := range b.Writes {
			body, done := scope.Resolve(w.Body, ids)
			id, err := write(w, body)
			if err != nil {
				return drop(fmt.Errorf("the page %s: %w", w.Path, err))
			}
			if w.Ref != "" {
				ids[w.Ref] = strconv.FormatInt(id, 10)
			}
			if !done {
				again = append(again, k)
			}
			out = append(out, Written{What: "page", ID: strconv.FormatInt(id, 10), Detail: fmt.Sprintf("%s/%s, “%s”", w.Space, w.Path, w.Title)})
		}
		// A page that links to a new page written after it is written again,
		// in the same batch, with the link's id (as the agent kit does).
		for _, k := range again {
			w := b.Writes[k]
			body, _ := scope.Resolve(w.Body, ids)
			if _, err := write(w, body); err != nil {
				return drop(fmt.Errorf("the page %s, written again with its links: %w", w.Path, err))
			}
		}
		for _, d := range b.Deletes {
			pageID, err := num(d.PageID)
			if err != nil {
				return drop(err)
			}
			rev, err := num(d.BaseRev)
			if err != nil {
				return drop(err)
			}
			if err := call("waymarks_delete_page", http.MethodPost, fmt.Sprintf("/v1/batches/%d/writes", batch.ID), map[string]any{"page_id": pageID, "base_rev": rev, "delete": true}, nil); err != nil {
				return drop(fmt.Errorf("deleting page %d: %w", pageID, err))
			}
			out = append(out, Written{What: "delete", ID: strconv.FormatInt(pageID, 10), Detail: fmt.Sprintf("page %d", pageID)})
		}
		var changeset struct {
			ID int64 `json:"id"`
		}
		if err := call("waymarks_close_batch", http.MethodPost, fmt.Sprintf("/v1/batches/%d/close", batch.ID), nil, &changeset); err != nil {
			return drop(fmt.Errorf("closing it: %w", err))
		}
		out = append(out, Written{What: "batch", ID: strconv.FormatInt(changeset.ID, 10), Detail: b.Note})
	}
	for i, is := range p.Issues {
		var err error
		switch {
		case is.Raise != nil:
			page, nerr := num(is.Raise.PageID)
			if nerr != nil {
				return out, nerr
			}
			var got struct {
				ID int64 `json:"id"`
			}
			err = call("waymarks_raise_issue", http.MethodPost, "/v1/issues", map[string]any{
				"page_id": page, "title": is.Raise.Title, "quote": is.Raise.Quote, "body": is.Raise.Body,
				"urgent": is.Raise.Urgent, "urgent_reason": is.Raise.UrgentReason}, &got)
			if err == nil {
				out = append(out, Written{What: "raise", ID: strconv.FormatInt(got.ID, 10), Detail: is.Raise.Title})
			}
		case is.Note != nil:
			id, nerr := num(is.Note.Issue)
			if nerr != nil {
				return out, nerr
			}
			err = call("waymarks_note_issue", http.MethodPost, fmt.Sprintf("/v1/issues/%d/notes", id), map[string]string{"text": is.Note.Text}, nil)
			if err == nil {
				out = append(out, Written{What: "note", ID: string(is.Note.Issue), Detail: "a note on issue " + string(is.Note.Issue)})
			}
		case is.Close != nil:
			id, nerr := num(is.Close.Issue)
			if nerr != nil {
				return out, nerr
			}
			cs, nerr := num(is.Close.Changeset)
			if nerr != nil {
				return out, nerr
			}
			err = call("waymarks_close_issue", http.MethodPost, fmt.Sprintf("/v1/issues/%d/close", id), map[string]any{"reason": is.Close.Reason, "changeset": cs}, nil)
			if err == nil {
				out = append(out, Written{What: "close", ID: string(is.Close.Issue), Detail: "issue " + string(is.Close.Issue) + ", " + is.Close.Reason})
			}
		}
		if err != nil {
			return out, fmt.Errorf("issue change %d: %w", i+1, err)
		}
	}
	return out, nil
}

// builtinStore reads what Check needs to know of the built-in store.
func builtinStore(ctx context.Context, s *tenant.Store, p scope.Plan) (scope.Store, error) {
	st := scope.Store{Sources: map[scope.ID]bool{}, Spaces: map[string]bool{}}
	labels, err := builtin.SpaceLabels(ctx, s)
	if err != nil {
		return st, err
	}
	for name, l := range labels {
		st.Spaces[name] = l.Sensitive()
	}
	ks, err := knowledge.Open(ctx, StoreBuiltin, s) // leaves withdrawn sources out
	if err != nil {
		return st, err
	}
	for _, b := range p.Batches {
		for _, w := range b.Writes {
			for _, id := range w.Sources {
				if _, seen := st.Sources[id]; seen {
					continue
				}
				src, err := ks.ReadSource(ctx, string(id))
				if errors.Is(err, knowledge.ErrNotFound) || errors.Is(err, knowledge.ErrInvalid) {
					continue
				}
				if err != nil {
					return st, err
				}
				st.Sources[id] = src.Label.Sensitive()
			}
		}
	}
	return st, nil
}

// applyBuiltin writes a checked answer's pages to the built-in store.
func applyBuiltin(ctx context.Context, s *tenant.Store, rc recorder, p scope.Plan) ([]Written, error) {
	var out []Written
	for i, b := range p.Batches {
		ids := map[string]string{}
		var again []int
		write := func(w scope.Write, body string) (knowledge.Page, error) {
			np := builtin.NewPage{Space: w.Space, Path: w.Path, Title: w.Title, Kind: w.Properties["kind"], Body: body}
			for _, id := range w.Sources {
				np.Sources = append(np.Sources, string(id))
			}
			got, err := rc.record(ctx, "builtin_write_page", np, func() (any, error) {
				page, err := builtin.WritePage(ctx, s, np)
				if err != nil {
					return nil, err
				}
				return map[string]string{"id": page.ID, "revision": page.Revision}, nil
			})
			if err != nil {
				return knowledge.Page{}, err
			}
			m, _ := got.(map[string]string)
			return knowledge.Page{ID: m["id"]}, nil
		}
		for k, w := range b.Writes {
			body, done := scope.Resolve(w.Body, ids)
			page, err := write(w, body)
			if err != nil {
				return out, fmt.Errorf("batch %d (%s), the page %s: %w", i+1, b.Note, w.Path, err)
			}
			if w.Ref != "" {
				ids[w.Ref] = page.ID
			}
			if !done {
				again = append(again, k)
			}
			out = append(out, Written{What: "page", ID: page.ID, Detail: fmt.Sprintf("%s/%s, “%s”", w.Space, w.Path, w.Title)})
		}
		for _, k := range again {
			w := b.Writes[k]
			body, _ := scope.Resolve(w.Body, ids)
			if _, err := write(w, body); err != nil {
				return out, fmt.Errorf("batch %d (%s), the page %s, written again with its links: %w", i+1, b.Note, w.Path, err)
			}
		}
		out = append(out, Written{What: "batch", Detail: b.Note})
	}
	return out, nil
}

// noteText is what a run's note says: what was written, or why nothing
// was.
func noteText(j Job, state string, written []Written, why string) string {
	var b bytes.Buffer
	switch state {
	case stateApplied:
		pages, batches, issues := 0, 0, 0
		for _, w := range written {
			switch w.What {
			case "page":
				pages++
			case "batch":
				batches++
			case "raise", "note", "close":
				issues++
			}
		}
		if pages == 0 && issues == 0 && batches == 0 {
			fmt.Fprintf(&b, "Keystone wrote nothing for “%s”: its answer asked for no change.", j.Title)
			return b.String()
		}
		fmt.Fprintf(&b, "Keystone wrote what “%s” asked for, through the job's own grant", j.Title)
		if j.Store == StoreWaymarks {
			fmt.Fprintf(&b, " (Waymarks shows it as %s)", j.Principal)
		}
		fmt.Fprintf(&b, ": %d %s, %d %s written, %d %s.", batches, plural(batches, "batch", "batches"), pages, plural(pages, "page", "pages"), issues, plural(issues, "issue change", "issue changes"))
	case stateRefused:
		fmt.Fprintf(&b, "Keystone wrote nothing for “%s”: %s.", j.Title, why)
	case stateFailed:
		fmt.Fprintf(&b, "Keystone stopped writing what “%s” asked for: %s. What was written before it stays.", j.Title, why)
	case stateInterrupted:
		fmt.Fprintf(&b, "Keystone stopped while it was writing what “%s” asked for. What was written before the stop stays, and the rest was not written.", j.Title)
	}
	for _, w := range written {
		if w.What == "page" || w.What == "delete" || w.What == "raise" || w.What == "close" || w.What == "note" {
			fmt.Fprintf(&b, "\n- %s %s: %s", w.What, w.ID, w.Detail)
		}
	}
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
