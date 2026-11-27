package loadtest

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Table is a result as Markdown: the run, each measurement against its
// budget, the sizes the load left, and the Monitor's figures beside the
// test's own. The command prints it and writes it into the docs.
func Table(r *Result) string {
	var b strings.Builder
	p, s := r.Profile, r.Sizes
	held := "95th percentile"
	if p.OnMedian {
		held = "median"
	}
	verdict := "Every time is inside its budget, and every figure of the Monitor matches the test's own."
	if len(r.Problems) > 0 {
		verdict = fmt.Sprintf("**%d problems**, listed at the end.", len(r.Problems))
	}
	fmt.Fprintf(&b, "The %s run of %s, on %s. %s\n\n", p.Name, r.At.Format("January 2, 2006 at 15:04 UTC"), r.Machine, verdict)
	fmt.Fprintf(&b, "The load: %s, %s chats, %s turns, %s messages, %s model calls and %s audit entries for the first person (one chat of %s turns), with %d uploads and %d artifacts; %d more people with %d chats each. Seed %d. Writing it took %s, measuring %s, the whole run %s.\n\n",
		span(p.Days), commas(int64(s.Chats)), commas(int64(s.Turns)), commas(int64(s.Messages)), commas(int64(s.Calls)), commas(s.AuditEntries), commas(int64(p.LongChatTurns)),
		s.Uploads, s.Artifacts, p.Tenants, p.TenantChats, p.Seed, r.Generating.Round(time.Second), r.Measuring.Round(time.Second), r.Took.Round(time.Second))

	fmt.Fprintf(&b, "| Measurement | Runs | Median | 95th percentile | Budget | Held to it (%s) |\n| --- | --- | --- | --- | --- | --- |\n", held)
	for _, m := range r.Measurements {
		state := "inside"
		if m.Over {
			state = "**over**"
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %s |\n", m.What, m.Runs, round(m.Median), round(m.P95), m.Limit, state)
	}

	fmt.Fprintf(&b, "\n| On disk, the first person | Size |\n| --- | --- |\n")
	fmt.Fprintf(&b, "| The database, `keystone.db` | %s |\n", size(s.DatabaseBytes))
	fmt.Fprintf(&b, "| The files folder, %d uploads and a project file | %s |\n", s.Uploads, size(s.FilesBytes))
	fmt.Fprintf(&b, "| The artifacts folder, %d artifacts in two versions each | %s |\n", s.Artifacts, size(s.ArtifactsBytes))
	if len(s.OthersDatabaseBytes) > 0 {
		var total int64
		for _, n := range s.OthersDatabaseBytes {
			total += n
		}
		fmt.Fprintf(&b, "| Each other person's database, the mean of %d | %s |\n", len(s.OthersDatabaseBytes), size(total/int64(len(s.OthersDatabaseBytes))))
	}

	fmt.Fprintf(&b, "\n| The database by table, with its indexes | Rows | Size | Share |\n| --- | --- | --- | --- |\n")
	var rest int64
	for i, t := range s.Tables {
		if i >= 10 {
			rest += t.Bytes
			continue
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %d%% |\n", t.Table, commas(t.Rows), size(t.Bytes), percent(t.Bytes, s.DatabaseBytes))
	}
	if rest > 0 {
		fmt.Fprintf(&b, "| every other table | | %s | %d%% |\n", size(rest), percent(rest, s.DatabaseBytes))
	}

	fmt.Fprintf(&b, "\n| The Monitor, `GET /api/v1/operations` | Operations reports | The test's own count | |\n| --- | --- | --- | --- |\n")
	for _, when := range []string{WhenLoaded, WhenMeasured} {
		for _, l := range r.Monitor {
			if l.When != when {
				continue
			}
			state := "matches"
			if !l.Match {
				state = "**differs**"
			}
			fmt.Fprintf(&b, "| %s, %s | %s | %s | %s |\n", upperFirst(l.When), l.Figure, l.Reported, l.Own, state)
		}
	}
	if len(r.Problems) > 0 {
		b.WriteString("\nProblems:\n\n")
		for _, p := range r.Problems {
			b.WriteString("- " + p + "\n")
		}
	}
	return b.String()
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// span is a number of days as the report says it.
func span(days int) string {
	if days >= 365 && days%365 == 0 {
		if days == 365 {
			return "1 year"
		}
		return fmt.Sprintf("%d years", days/365)
	}
	return fmt.Sprintf("%d days", days)
}

func percent(part, whole int64) int64 {
	if whole <= 0 {
		return 0
	}
	return (part*100 + whole/2) / whole
}

// size is a number of bytes as a person reads it.
func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// commas is a whole number with a comma between each three digits.
func commas(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// The marks a result's table is written between, in the docs.
const (
	tableBegin = "<!-- load test: the last full run, written by go run ./tools/loadtest -->"
	tableEnd   = "<!-- load test: end of the last full run -->"
)

// WriteTable puts a result's table into a Markdown file, in place of the
// one between the file's two marks. A file without the marks is refused,
// so nothing else in it is ever written over.
func WriteTable(path string, r *Result) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(raw)
	from, to := strings.Index(text, tableBegin), strings.Index(text, tableEnd)
	if from < 0 || to < from {
		return fmt.Errorf("%s has no place for the table: it needs the lines %q and %q", path, tableBegin, tableEnd)
	}
	text = text[:from] + tableBegin + "\n\n" + Table(r) + "\n" + text[to:]
	return os.WriteFile(path, []byte(text), 0o644)
}
