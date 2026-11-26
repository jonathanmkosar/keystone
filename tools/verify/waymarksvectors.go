package main

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
)

// The fake Waymarks' page vectors, for the embeddings check (P6-08). Like
// Waymarks it stores and searches vectors and never makes one: Keystone's
// refresh asks which pages need one, makes it, and pushes it. Its calls
// have Waymarks' shape, from Waymarks' own source: GET /v1/vectors/needed,
// PUT /v1/pages/{id}/vector, and POST /v1/search/hybrid, which ranks a
// search by vector by its place in the list and joins it with the words by
// reciprocal rank, k = 60. A page's revision here is always 1, so a vector
// is never stale.

// wmVector is one page's vector, scaled to unit length, and its model.
type wmVector struct {
	model string
	unit  []float64
}

// vectorsNeeded lists the pages with no vector, in space and path order.
func (f *fakeWaymarks) vectorsNeeded(r *http.Request) (any, bool, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	pages := append([]wmPage(nil), f.pages...)
	sort.Slice(pages, func(i, j int) bool {
		if pages[i].space != pages[j].space {
			return pages[i].space < pages[j].space
		}
		return pages[i].path < pages[j].path
	})
	out := []map[string]any{}
	sensitive := false
	for _, p := range pages {
		if _, ok := f.vectors[p.id]; ok || len(out) == limit {
			continue
		}
		sensitive = sensitive || f.sensitive(p.space)
		out = append(out, map[string]any{"page_id": p.id, "space": p.space, "path": p.path, "title": p.title, "rev": 1, "reason": "none"})
	}
	return out, sensitive, 0
}

// vectorPut stores one page's vector.
func (f *fakeWaymarks) vectorPut(r *http.Request) (any, bool, int) {
	var in struct {
		Rev    int       `json:"rev"`
		Model  string    `json:"model"`
		Vector []float32 `json:"vector"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Rev != 1 || in.Model == "" {
		return nil, false, http.StatusNotFound
	}
	unit, ok := unitVector(in.Vector)
	if !ok {
		return nil, false, http.StatusNotFound
	}
	id := pathID(r)
	for _, p := range f.pages {
		if p.id == id {
			if f.vectors == nil {
				f.vectors = map[int64]wmVector{}
			}
			f.vectors[id] = wmVector{model: in.Model, unit: unit}
			return map[string]any{"page_id": id, "rev": 1, "model": in.Model, "size": len(in.Vector)}, f.sensitive(p.space), 0
		}
	}
	return nil, false, http.StatusNotFound
}

// unitVector scales a vector to unit length; ok is false for one that
// points nowhere.
func unitVector(v []float32) ([]float64, bool) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 || math.IsNaN(sum) || math.IsInf(sum, 0) {
		return nil, false
	}
	norm := math.Sqrt(sum)
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = float64(x) / norm
	}
	return out, true
}

// hybrid searches by a vector, and by words when the body has them, and
// joins the two by reciprocal rank, as Waymarks does.
func (f *fakeWaymarks) hybrid(r *http.Request) (any, bool, int) {
	var in struct {
		Q      string    `json:"q"`
		Vector []float32 `json:"vector"`
		Model  string    `json:"model"`
		Space  string    `json:"space"`
		Limit  int       `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return nil, false, http.StatusNotFound
	}
	if in.Limit <= 0 || in.Limit > 100 {
		in.Limit = 20
	}
	const k = 60
	type ranked struct {
		row   map[string]any
		score float64
		key   string
	}
	var all []ranked
	at := map[string]int{}
	if in.Q != "" {
		q := r.URL.Query()
		q.Set("q", in.Q)
		q.Set("space", in.Space)
		q.Set("limit", "100")
		r.URL.RawQuery = q.Encode()
		words, _, _ := f.search(r)
		for i, row := range words.([]map[string]any) {
			key := row["kind"].(string) + strconv.FormatInt(row["id"].(int64), 10)
			row["score"] = 1 / float64(k+i+1)
			at[key] = len(all)
			all = append(all, ranked{row, row["score"].(float64), key})
		}
	}
	query, ok := unitVector(in.Vector)
	if !ok {
		return nil, false, http.StatusNotFound
	}
	type near struct {
		p      wmPage
		cosine float64
	}
	var nearest []near
	for _, p := range f.pages {
		v, ok := f.vectors[p.id]
		if !ok || v.model != in.Model || len(v.unit) != len(query) || (in.Space != "" && in.Space != p.space) {
			continue
		}
		var dot float64
		for i := range query {
			dot += query[i] * v.unit[i]
		}
		nearest = append(nearest, near{p, dot})
	}
	sort.Slice(nearest, func(i, j int) bool {
		if nearest[i].cosine != nearest[j].cosine {
			return nearest[i].cosine > nearest[j].cosine
		}
		return nearest[i].p.id < nearest[j].p.id
	})
	for i, n := range nearest {
		key := "page" + strconv.FormatInt(n.p.id, 10)
		score := 1 / float64(k+i+1)
		if j, ok := at[key]; ok {
			all[j].score += score
			all[j].row["score"] = all[j].score
			continue
		}
		row := map[string]any{"kind": "page", "id": n.p.id, "space": n.p.space, "path": n.p.path, "title": n.p.title, "snippet": n.p.body, "score": score}
		at[key] = len(all)
		all = append(all, ranked{row, score, key})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].score > all[j].score })
	if len(all) > in.Limit {
		all = all[:in.Limit]
	}
	out := []map[string]any{}
	sensitive := false
	for _, h := range all {
		sensitive = sensitive || f.sensitive(h.row["space"].(string))
		out = append(out, h.row)
	}
	return out, sensitive, 0
}
