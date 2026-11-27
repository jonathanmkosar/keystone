// Package usagereport sums a tenant's usage records for a period into the
// report that Settings → Usage shows and tools/tenant usage prints: the
// total, the spend by model, by chat and by kind of work, and where the
// month stands against the monthly cap and the memory pass's reserve.
//
// It reads the usage records and nothing that keeps a total of its own, so
// the report cannot drift from them: every breakdown is summed from the
// same records as the total, and its rows add up to it. It writes nothing.
// docs/architecture/usage-report.md describes it.
package usagereport

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Sum is what some usage records add up to: calls, tokens and cost, and
// the same split by the class of the model that took each call. A local
// model costs nothing, and its calls and tokens are counted all the same.
type Sum struct {
	usage.Total
	Local   usage.Total `json:"local"`
	Hosted  usage.Total `json:"hosted"`
	Trusted usage.Total `json:"trusted"`
}

func addTo(t *usage.Total, r usage.Record) {
	t.Calls++
	t.InputTokens += int64(r.InputTokens)
	t.OutputTokens += int64(r.OutputTokens)
	t.CacheReadTokens += int64(r.CacheReadTokens)
	t.CacheWriteTokens += int64(r.CacheWriteTokens)
	t.CostMicros += r.CostMicros
}

func (s *Sum) add(r usage.Record) {
	addTo(&s.Total, r)
	switch r.Class {
	case models.ClassLocal:
		addTo(&s.Local, r)
	case models.ClassHosted:
		addTo(&s.Hosted, r)
	case models.ClassTrusted:
		addTo(&s.Trusted, r)
	}
}

func plus(a, b usage.Total) usage.Total {
	return usage.Total{
		Calls: a.Calls + b.Calls, InputTokens: a.InputTokens + b.InputTokens, OutputTokens: a.OutputTokens + b.OutputTokens,
		CacheReadTokens: a.CacheReadTokens + b.CacheReadTokens, CacheWriteTokens: a.CacheWriteTokens + b.CacheWriteTokens,
		CostMicros: a.CostMicros + b.CostMicros,
	}
}

func (s *Sum) merge(o Sum) {
	s.Total = plus(s.Total, o.Total)
	s.Local = plus(s.Local, o.Local)
	s.Hosted = plus(s.Hosted, o.Hosted)
	s.Trusted = plus(s.Trusted, o.Trusted)
}

// Capped is the part of the cost that counts against the monthly cap:
// what was spent on models that are not local.
func (s Sum) Capped() int64 { return s.Hosted.CostMicros + s.Trusted.CostMicros }

// Row is one line of a breakdown.
type Row struct {
	// Key names the line: a model's registry name, a chat's id, or a kind
	// of work. It is empty for the line of calls in no chat the person
	// still has, and for the line that adds up the rest.
	Key string `json:"key"`
	// Label is what a person reads: the model's label, the chat's title,
	// or the kind of work in words.
	Label string `json:"label"`
	// Class is the model's class when the calls were made, in the
	// breakdown by model. A model whose class changed has a line for each.
	Class models.Class `json:"class,omitempty"`
	// Rest is how many lines this one adds up, past the breakdown's
	// limit. It is zero for a line of its own.
	Rest int `json:"rest,omitempty"`
	Sum
}

// The kinds of work a record can be put under besides its own kind. A
// record's own kind is the kind of task it was routed for: chat, title,
// summary (a chat's oldest turns, and recaps), memory, embedding.
const (
	// WorkTask is a call made for a background task (P8-01).
	WorkTask = "background_task"
	// WorkSchedule is a call made for a background task a schedule
	// started (P8-03).
	WorkSchedule = "scheduled_task"
)

// workLabels are the kinds of work in words. A kind that is not here is
// shown as it is.
var workLabels = map[string]string{
	"chat":       "Chat replies",
	"title":      "Chat titles",
	"summary":    "Summaries and recaps",
	"memory":     "Memory pass",
	"embedding":  "Embeddings",
	"decision":   "Decisions",
	WorkTask:     "Background tasks",
	WorkSchedule: "Scheduled tasks",
}

// WorkLabel is a kind of work in words.
func WorkLabel(kind string) string {
	if l, ok := workLabels[kind]; ok {
		return l
	}
	return kind
}

// The labels of the lines that are not one model, chat or kind.
const (
	labelNoChat = "Not in a chat you still have"
	labelChat   = "Untitled chat"
)

func restLabel(n int, what string) string { return fmt.Sprintf("%d more %s", n, what) }

// Breakdowns are a period's records summed three ways. Each way's lines
// add up to Total, field by field.
type Breakdowns struct {
	Total  Sum   `json:"total"`
	Models []Row `json:"models"`
	Chats  []Row `json:"chats"`
	Kinds  []Row `json:"kinds"`
	// Rows is the most lines a breakdown shows before the rest are added
	// up in one more line.
	Rows int `json:"rows"`
}

// Records reads the usage records of a period, oldest first: the records
// at or after its first moment and before the day after its last.
func Records(ctx context.Context, s *tenant.Store, p Period) ([]usage.Record, error) {
	if p.end.IsZero() || !p.end.After(p.start) {
		return nil, fmt.Errorf("usage report: the period %s to %s was not worked out by Resolve", p.From, p.To)
	}
	var out []usage.Record
	// A record's month is its time's, so the month index finds the rows
	// and the times keep the days of the range.
	for m := time.Date(p.start.Year(), p.start.Month(), 1, 0, 0, 0, 0, time.UTC); m.Before(p.end); m = m.AddDate(0, 1, 0) {
		recs, err := usage.List(ctx, s, usage.Filter{Month: usage.MonthOf(m)})
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			if !r.At.Before(p.start) && r.At.Before(p.end) {
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// Sum adds up some records into the three breakdowns. Each record is put
// in exactly one line of each, so the lines add up to the total. A
// breakdown shows its rows costliest lines and adds up the rest in one
// more line; rows below one is taken as one.
func Summarise(ctx context.Context, s *tenant.Store, recs []usage.Record, rows int) (Breakdowns, error) {
	if rows < 1 {
		rows = 1
	}
	p, err := newPlacer(ctx, s)
	if err != nil {
		return Breakdowns{}, err
	}
	defer p.close()
	labels, err := modelLabels(ctx, s)
	if err != nil {
		return Breakdowns{}, err
	}
	out := Breakdowns{Rows: rows}
	byModel, byChat, byKind := lines{}, lines{}, lines{}
	for _, r := range recs {
		pl, err := p.place(ctx, r)
		if err != nil {
			return Breakdowns{}, err
		}
		out.Total.add(r)

		label := labels[r.Model]
		if label == "" {
			label = r.Model
		}
		byModel.add(r.Model+"\x00"+string(r.Class), Row{Key: r.Model, Label: label, Class: r.Class}, r)

		chat := Row{Key: pl.chatID, Label: pl.chatTitle}
		if pl.chatID == "" {
			chat.Label = labelNoChat
		}
		byChat.add(pl.chatID, chat, r)

		byKind.add(pl.work, Row{Key: pl.work, Label: WorkLabel(pl.work)}, r)
	}
	out.Models = byModel.rows(rows, "models")
	out.Chats = byChat.rows(rows, "chats")
	out.Kinds = byKind.rows(rows, "kinds of work")
	return out, nil
}

// modelLabels are the labels of the models in the tenant's registry, by
// name. A model that has left the registry is shown by its name.
func modelLabels(ctx context.Context, s *tenant.Store) (map[string]string, error) {
	entries, err := models.List(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("usage report: reading the model registry: %w", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		out[e.Model.Name] = e.Model.Label
	}
	return out, nil
}

// lines is one breakdown while it is summed.
type lines map[string]*Row

func (l lines) add(key string, first Row, r usage.Record) {
	row, ok := l[key]
	if !ok {
		row = &first
		l[key] = row
	}
	row.Sum.add(r)
}

// rows are the lines costliest first, the most limit of them, and one more
// that adds up the rest.
func (l lines) rows(limit int, what string) []Row {
	out := make([]Row, 0, len(l))
	for _, r := range l {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b Row) int {
		return cmp.Or(
			cmp.Compare(b.CostMicros, a.CostMicros),
			cmp.Compare(b.Calls, a.Calls),
			cmp.Compare(a.Label, b.Label),
			cmp.Compare(a.Key, b.Key),
			cmp.Compare(a.Class, b.Class),
		)
	})
	if len(out) <= limit {
		return out
	}
	rest := Row{Rest: len(out) - limit, Label: restLabel(len(out)-limit, what)}
	for _, r := range out[limit:] {
		rest.Sum.merge(r.Sum)
	}
	return append(out[:limit:limit], rest)
}
