package loadtest

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// The proof that the fast path leaves what a real turn leaves: the same
// chats are sent through the real handler for one person and written by
// the fast path for another, and the two databases are compared, table by
// table and row by row.
//
// Two rows agree when every column is equal once what cannot be equal is
// made so: an id made at random (a chat's, a turn's, a request's) is
// replaced by its place in the order it was made, a time by a mark, and
// the columns in timed, which hold how long something took or which month
// it fell in, are left out. Numbers the database counts, a message's id, a
// turn's place, an audit entry's id, are compared as they are, so the two
// paths must also write in the same order.

// notCompared are the tables the comparison leaves out, and why.
var notCompared = map[string]string{
	"tenant_meta":          "holds the person's own id",
	"signin_password":      "each person's own salt and hash",
	"signin_sessions":      "each person's own session",
	"operations_snapshots": "written by the install's sampler, on a timer",
	"schema_migrations":    "when each migration ran",
	"person_map":           "one row a person, kept by their first real turn: the map of a knowledge store, which no load person has",
	"sqlite_sequence":      "SQLite's own counters, which the compared ids already prove",
}

// timed are the columns left out of a row: how long a call took, and the
// month it fell in.
var timed = map[string][]string{
	"audit_log":     {"duration_ms"},
	"usage_records": {"duration_ms", "month"},
}

var (
	timeShape = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z`)
	idShape   = regexp.MustCompile(`\b[0-9a-f]{16}\b`)
)

// sampleScripts are the fake provider's scripts for a sample of plans: each
// turn's reply and usage, and each hosted chat's title.
func sampleScripts(plans []chatPlan, titlePrompt string) []fake.Script {
	text := func(s string, u seam.Usage) *seam.Response {
		return &seam.Response{Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: s}}}, StopReason: seam.StopEnd, Usage: u}
	}
	var out []fake.Script
	for i, c := range plans {
		for j, t := range c.turns {
			out = append(out, fake.Script{Name: fmt.Sprintf("sample chat %d turn %d", i+1, j+1),
				When: fake.When{UserText: t.user}, Steps: []fake.Step{{Reply: text(t.reply, t.usage)}}})
		}
		if c.model.Name == hostedModel.Name {
			out = append(out, fake.Script{Name: fmt.Sprintf("sample chat %d title", i+1),
				When:  fake.When{SystemContains: titleMark(titlePrompt), UserTextContains: firstRunes(c.turns[0].user, 60)},
				Steps: []fake.Step{{Reply: text(c.title, titleUsage)}}})
		}
	}
	return out
}

// sendSample sends plans through the real handler as a person, a chat at a
// time, and returns the request id of the first turn.
func (g *rig) sendSample(who *person, plans []chatPlan) (string, error) {
	first := ""
	for _, c := range plans {
		chatID, err := g.newChat(who)
		if err != nil {
			return "", err
		}
		for _, t := range c.turns {
			got, err := g.send(who, chatID, turn.SendBody{Text: t.user, Model: c.model.Name})
			if err != nil {
				return "", err
			}
			if first == "" {
				first = got.requestID
			}
		}
	}
	return first, nil
}

// disagreements compares two people's databases and returns every table
// whose rows differ, with the first rows that do.
func disagreements(a, b *tenant.Store) ([]string, error) {
	ra, err := canonicalRows(a)
	if err != nil {
		return nil, err
	}
	rb, err := canonicalRows(b)
	if err != nil {
		return nil, err
	}
	var names []string
	for name := range ra {
		names = append(names, name)
	}
	for name := range rb {
		if _, ok := ra[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var out []string
	for _, name := range names {
		x, y := ra[name], rb[name]
		if len(x) != len(y) {
			out = append(out, fmt.Sprintf("%s: the real path left %d rows and the fast path %d", name, len(x), len(y)))
		}
		shown := 0
		for i := 0; i < len(x) && i < len(y) && shown < 3; i++ {
			if x[i] != y[i] {
				out = append(out, fmt.Sprintf("%s: a row differs\n  real: %s\n  fast: %s", name, differing(x[i], y[i]), differing(y[i], x[i])))
				shown++
			}
		}
	}
	return out, nil
}

// differing is the part of a around where it first differs from b.
func differing(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	from, to := max(i-120, 0), min(i+200, len(a))
	return fmt.Sprintf("...%s[HERE]%s...", a[from:i], a[i:to])
}

// canonicalRows reads every compared table of a person's database as rows
// of text, with ids, times and timed columns made comparable, sorted.
func canonicalRows(s *tenant.Store) (map[string][]string, error) {
	db := s.DB()
	// The ids made at random, by the order they were made in.
	names := map[string]string{s.TenantID(): "<person>"}
	for _, q := range []struct{ query, what string }{
		{"SELECT id FROM chats ORDER BY seq", "chat"},
		{"SELECT id FROM turns ORDER BY seq", "turn"},
		{"SELECT request_id FROM turns ORDER BY seq", "request"},
		{"SELECT request_id FROM audit_log WHERE action = 'POST /api/v1/chats' AND outcome = 'started' ORDER BY id", "made"},
	} {
		ids, err := textColumn(db, q.query)
		if err != nil {
			return nil, err
		}
		for i, id := range ids {
			names[id] = "<" + q.what + " " + strconv.Itoa(i+1) + ">"
		}
	}
	tables, err := textColumn(db, "SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name")
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, table := range tables {
		if _, skip := notCompared[table]; skip || shadow(table, tables) {
			continue
		}
		rows, err := db.Query(`SELECT rowid AS "rowid of the row", * FROM "` + table + `"`)
		if err != nil {
			// A table without a rowid has every column in its key.
			if rows, err = db.Query(`SELECT * FROM "` + table + `"`); err != nil {
				return nil, fmt.Errorf("reading %s: %w", table, err)
			}
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		leave := map[string]bool{}
		for _, c := range timed[table] {
			leave[c] = true
		}
		var list []string
		for rows.Next() {
			vals, ptrs := make([]any, len(cols)), make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return nil, err
			}
			var b strings.Builder
			for i, v := range vals {
				if leave[cols[i]] {
					continue
				}
				b.WriteString(cols[i] + "=")
				switch x := v.(type) {
				case []byte:
					b.WriteString(string(x))
				case nil:
					b.WriteString("NULL")
				default:
					fmt.Fprint(&b, x)
				}
				b.WriteString(" | ")
			}
			row := idShape.ReplaceAllStringFunc(b.String(), func(id string) string {
				if name, ok := names[id]; ok {
					return name
				}
				return "<id>"
			})
			list = append(list, timeShape.ReplaceAllString(row, "<time>"))
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		sort.Strings(list)
		out[table] = list
	}
	return out, nil
}

// shadow says whether a table is one of a full-text index's own, which
// hold the same words in a form that depends on how they were merged. The
// index itself is compared, row by row.
func shadow(table string, all []string) bool {
	for _, suffix := range []string{"_data", "_idx", "_docsize", "_config", "_content"} {
		if base, ok := strings.CutSuffix(table, suffix); ok {
			for _, t := range all {
				if t == base {
					return true
				}
			}
		}
	}
	return false
}

func textColumn(db *sql.DB, query string, args ...any) ([]string, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
