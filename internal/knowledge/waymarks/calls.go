package waymarks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
)

// Waymarks numbers pages and sources, each from 1. An id here is that
// number as text. A page and a source may have the same number, so an id is
// read with the call that matches its kind.

// numberOf returns the number inside an id, or false when the text is not
// an id Waymarks gave. Only a plain number is ever put into an address.
func numberOf(id string) (int64, bool) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n < 1 || strconv.FormatInt(n, 10) != id {
		return 0, false
	}
	return n, true
}

func idOf(n int64) string { return strconv.FormatInt(n, 10) }

// kindProperty is the page property Waymarks keeps a page's kind in.
const kindProperty = "kind"

// catalogRow is one page as Waymarks' catalog lists it.
type catalogRow struct {
	ID         int64             `json:"id"`
	Space      string            `json:"space"`
	Path       string            `json:"path"`
	Title      string            `json:"title"`
	Properties map[string]string `json:"properties"`
}

// catalog reads the catalog and the label of each space in it.
func (s *store) catalog(ctx context.Context) ([]knowledge.Entry, error) {
	var rows []catalogRow
	r, err := s.get(ctx, s.reader, "/v1/catalog", nil, &rows)
	if err != nil {
		return nil, settle(err, false)
	}
	label, err := s.labelsFor(ctx, s.reader, r)
	if err != nil {
		return nil, err
	}
	out := make([]knowledge.Entry, 0, len(rows))
	for _, row := range rows {
		out = append(out, knowledge.Entry{
			ID: idOf(row.ID), Space: row.Space, Label: label(row.Space),
			Path: row.Path, Title: row.Title, Kind: row.Properties[kindProperty],
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Space != out[j].Space {
			return out[i].Space < out[j].Space
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func (s *store) Catalog(ctx context.Context) ([]knowledge.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.catalog(ctx)
}

// hitRow is one search hit as Waymarks sends it.
type hitRow struct {
	Kind    string  `json:"kind"`
	ID      int64   `json:"id"`
	Space   string  `json:"space"`
	Path    string  `json:"path"`
	Name    string  `json:"name"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

func (s *store) Search(ctx context.Context, q knowledge.Query) ([]knowledge.Hit, error) {
	byVector := len(q.Vector) > 0
	if byVector && s.embedder == "" {
		return nil, knowledge.ErrNotSupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	text := strings.TrimSpace(q.Text)
	if text == "" && !byVector {
		return nil, nil
	}
	if byVector && strings.TrimSpace(q.Model) == "" {
		return nil, fmt.Errorf("%w: a query vector needs the name of the model that made it", knowledge.ErrInvalid)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = s.searchResults
	}
	if limit > waymarksSearchMax {
		limit = waymarksSearchMax
	}

	var rows []hitRow
	var r reply
	var err error
	if byVector {
		// Words, a vector, or both, in one ranking.
		body, merr := json.Marshal(struct {
			Q      string    `json:"q,omitempty"`
			Vector []float32 `json:"vector"`
			Model  string    `json:"model"`
			Space  string    `json:"space,omitempty"`
			Limit  int       `json:"limit"`
		}{text, q.Vector, q.Model, q.Space, limit})
		if merr != nil {
			return nil, fmt.Errorf("%w: the query vector cannot be sent: %v", knowledge.ErrInvalid, merr)
		}
		r, err = s.into(ctx, request{token: s.reader, method: http.MethodPost, path: "/v1/search/hybrid",
			body: body, contentType: "application/json"}, &rows)
	} else {
		query := url.Values{"q": {text}, "limit": {strconv.Itoa(limit)}}
		if q.Space != "" {
			query.Set("space", q.Space)
		}
		r, err = s.get(ctx, s.reader, "/v1/search", query, &rows)
	}
	if err != nil {
		// A space the reader's grant does not reach is, for Keystone, a
		// space the store does not hold: a search of it finds nothing.
		var refused *refusal
		if q.Space != "" && errors.As(err, &refused) && refused.status == http.StatusForbidden {
			return nil, nil
		}
		return nil, settle(err, false)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	label, err := s.labelsFor(ctx, s.reader, r)
	if err != nil {
		return nil, err
	}
	out := make([]knowledge.Hit, 0, len(rows))
	for _, row := range rows {
		if len(out) == limit {
			break
		}
		h := knowledge.Hit{ID: idOf(row.ID), Space: row.Space, Label: label(row.Space), Snippet: row.Snippet, Score: row.Score}
		switch row.Kind {
		case knowledge.HitPage:
			h.Type, h.Path, h.Title = knowledge.HitPage, row.Path, row.Title
		case knowledge.HitSource:
			h.Type, h.Title = knowledge.HitSource, row.Name
		default:
			continue // a kind of hit this build does not know
		}
		if q.Space != "" && row.Space != q.Space {
			continue
		}
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

func (s *store) ReadPage(ctx context.Context, id string) (knowledge.Page, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.Page{}, err
	}
	n, ok := numberOf(id)
	if !ok {
		return knowledge.Page{}, fmt.Errorf("%w: page %q", knowledge.ErrNotFound, id)
	}
	var row struct {
		ID         int64             `json:"id"`
		Space      string            `json:"space"`
		Path       string            `json:"path"`
		Title      string            `json:"title"`
		Body       string            `json:"body"`
		Rev        int               `json:"rev"`
		Deleted    bool              `json:"deleted"`
		Properties map[string]string `json:"properties"`
		Sources    []int64           `json:"sources"`
	}
	// The answer is about one space, so its mark is that space's label,
	// read in the same call as the page.
	r, err := s.get(ctx, s.reader, "/v1/pages/"+idOf(n), nil, &row)
	if err != nil {
		return knowledge.Page{}, settle(err, true)
	}
	if row.Deleted || row.ID != n {
		return knowledge.Page{}, fmt.Errorf("%w: page %q", knowledge.ErrNotFound, id)
	}
	p := knowledge.Page{
		ID: id, Space: row.Space, Label: labelOf(r.sensitive), Path: row.Path, Title: row.Title,
		Kind: row.Properties[kindProperty], Body: row.Body, Revision: strconv.Itoa(row.Rev),
	}
	for _, src := range row.Sources {
		p.Sources = append(p.Sources, idOf(src))
	}
	return p, nil
}

// sourceRow is a source's record as Waymarks sends it.
type sourceRow struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Origin string `json:"origin"`
	Dated  string `json:"dated"`
	Space  string `json:"space"`
	Erased bool   `json:"erased"`
}

func (s *store) ReadSource(ctx context.Context, id string) (knowledge.Source, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.Source{}, err
	}
	n, ok := numberOf(id)
	if !ok {
		return knowledge.Source{}, fmt.Errorf("%w: source %q", knowledge.ErrNotFound, id)
	}
	var row struct {
		Source sourceRow `json:"source"`
		Text   string    `json:"text"`
	}
	r, err := s.get(ctx, s.reader, "/v1/sources/"+idOf(n)+"/text", nil, &row)
	if err != nil {
		return knowledge.Source{}, settle(err, true)
	}
	if row.Source.Erased || row.Source.ID != n {
		return knowledge.Source{}, fmt.Errorf("%w: source %q", knowledge.ErrNotFound, id)
	}
	return knowledge.Source{
		ID: id, Space: row.Source.Space, Label: labelOf(r.sensitive), Name: row.Source.Name,
		Origin: row.Source.Origin, Dated: row.Source.Dated, Text: row.Text,
	}, nil
}

// factsMediaType is what the facts are uploaded as: a Markdown list.
const factsMediaType = "text/markdown"

func (s *store) AddSource(ctx context.Context, n knowledge.NewSource) (knowledge.Source, error) {
	if s.intake == "" {
		return knowledge.Source{}, knowledge.ErrNotSupported
	}
	if err := ctx.Err(); err != nil {
		return knowledge.Source{}, err
	}
	if err := knowledge.CheckNewSource(n); err != nil {
		return knowledge.Source{}, err
	}
	// The facts become the source's text and its file, one to a line, as a
	// Markdown list. Waymarks has no field for facts.
	var text strings.Builder
	for _, f := range n.Facts {
		text.WriteString("- " + strings.TrimSpace(f.Text) + "\n")
	}

	// Waymarks reads the fields first and the file last.
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for _, field := range [][2]string{
		{"space", n.Space}, {"name", n.Name}, {"media_type", factsMediaType},
		{"origin", n.Origin}, {"dated", n.Dated}, {"text", text.String()},
	} {
		if field[1] == "" {
			continue
		}
		if err := form.WriteField(field[0], field[1]); err != nil {
			return knowledge.Source{}, fmt.Errorf("%w: %v", knowledge.ErrInvalid, err)
		}
	}
	file, err := form.CreateFormFile("file", "facts.md")
	if err == nil {
		_, err = file.Write([]byte(text.String()))
	}
	if err == nil {
		err = form.Close()
	}
	if err != nil {
		return knowledge.Source{}, fmt.Errorf("%w: %v", knowledge.ErrInvalid, err)
	}

	// The intake token reads nothing, so the source as stored is put
	// together from the record Waymarks answers with and the text sent. The
	// answer is about one space, and its mark is that space's label.
	var row sourceRow
	r, err := s.into(ctx, request{token: s.intake, method: http.MethodPost, path: "/v1/sources",
		body: body.Bytes(), contentType: form.FormDataContentType()}, &row)
	if err != nil {
		return knowledge.Source{}, settle(err, false)
	}
	if row.ID < 1 {
		return knowledge.Source{}, unavailable("answered a new source with no id")
	}
	return knowledge.Source{
		ID: idOf(row.ID), Space: row.Space, Label: labelOf(r.sensitive), Name: row.Name,
		Origin: row.Origin, Dated: row.Dated, Text: text.String(),
	}, nil
}

// CatalogMarker is a short hash of the catalog as the reader sees it and of
// the label of every space the reader can read. It is the same while those
// are the same, and it does not change when only a page's body does.
func (s *store) CatalogMarker(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	entries, err := s.catalog(ctx)
	if err != nil {
		return "", err
	}
	spaces, err := s.labels(ctx, s.reader)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	part := func(text string) { fmt.Fprintf(h, "%d:%s,", len(text), text) }
	for _, e := range entries {
		part(e.ID)
		part(e.Space)
		part(e.Path)
		part(e.Title)
		part(e.Kind)
	}
	names := make([]string, 0, len(spaces))
	for name := range spaces {
		names = append(names, name)
	}
	sort.Strings(names)
	part("spaces")
	for _, name := range names {
		part(name)
		part(string(spaces[name]))
	}
	return "catalog " + hex.EncodeToString(h.Sum(nil)[:12]), nil
}

// VectorsNeeded lists what Waymarks says needs a vector. The model is not
// sent: an instance takes vectors of one model, set by its owner, and
// counts a vector of any other as none. A model that is not the instance's
// is refused when its vector is stored.
func (s *store) VectorsNeeded(ctx context.Context, _ string, limit int) ([]knowledge.PageNeed, error) {
	if s.embedder == "" {
		return nil, knowledge.ErrNotSupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = s.vectorsNeeded
	}
	if limit > waymarksNeedsMax {
		limit = waymarksNeedsMax
	}
	var rows []struct {
		PageID int64  `json:"page_id"`
		Space  string `json:"space"`
		Title  string `json:"title"`
		Reason string `json:"reason"`
	}
	r, err := s.get(ctx, s.embedder, "/v1/vectors/needed", url.Values{"limit": {strconv.Itoa(limit)}}, &rows)
	if err != nil {
		return nil, settle(err, false)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	label, err := s.labelsFor(ctx, s.embedder, r)
	if err != nil {
		return nil, err
	}
	out := make([]knowledge.PageNeed, 0, len(rows))
	for _, row := range rows {
		if len(out) == limit {
			break
		}
		out = append(out, knowledge.PageNeed{
			PageID: idOf(row.PageID), Space: row.Space, Label: label(row.Space),
			Title: row.Title, Stale: row.Reason == "stale",
		})
	}
	return out, nil
}

func (s *store) PutVector(ctx context.Context, v knowledge.PageVector) error {
	if s.embedder == "" {
		return knowledge.ErrNotSupported
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(v.Vector) == 0 {
		return fmt.Errorf("%w: a vector needs numbers", knowledge.ErrInvalid)
	}
	if strings.TrimSpace(v.Model) == "" {
		return fmt.Errorf("%w: a vector needs the name of the model that made it", knowledge.ErrInvalid)
	}
	n, ok := numberOf(v.PageID)
	if !ok {
		return fmt.Errorf("%w: page %q", knowledge.ErrNotFound, v.PageID)
	}
	// A revision here is a number from 1. Anything else was never this
	// page's revision, and Waymarks would refuse it as a bad call. The page
	// is read first, so one that is not there still answers as not found.
	rev, err := strconv.Atoi(v.Revision)
	stale := err != nil || rev < 1 || strconv.Itoa(rev) != v.Revision
	if stale {
		if _, err := s.ReadPage(ctx, v.PageID); err != nil {
			return err
		}
		return fmt.Errorf("%w: %q is not a revision of page %s", knowledge.ErrStale, v.Revision, v.PageID)
	}
	body, err := json.Marshal(struct {
		Rev    int       `json:"rev"`
		Model  string    `json:"model"`
		Vector []float32 `json:"vector"`
	}{rev, v.Model, v.Vector})
	if err != nil {
		return fmt.Errorf("%w: the vector cannot be sent: %v", knowledge.ErrInvalid, err)
	}
	var stored json.RawMessage
	_, err = s.into(ctx, request{token: s.embedder, method: http.MethodPut, path: "/v1/pages/" + idOf(n) + "/vector",
		body: body, contentType: "application/json"}, &stored)
	return settle(err, true)
}
