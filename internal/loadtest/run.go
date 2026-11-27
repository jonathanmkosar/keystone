package loadtest

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Options is how a run is made.
type Options struct {
	// Dir is the folder the run's throwaway data folder is made in. Empty
	// means the system's temporary folder. The data folder is removed when
	// the run ends.
	Dir string
	// Say is told how the run goes. Nil tells nothing.
	Say func(string)
}

// TableSize is one table's share of a person's database, with its indexes.
type TableSize struct {
	Table string `json:"table"`
	Bytes int64  `json:"bytes"`
	Rows  int64  `json:"rows"`
}

// Sizes is what the load left for the first person.
type Sizes struct {
	Chats    int `json:"chats"`
	Turns    int `json:"turns"`
	Messages int `json:"messages"`
	// Calls is the model calls the generator wrote, and so the usage
	// records; AuditEntries the audit log's rows.
	Calls        int   `json:"calls"`
	AuditEntries int64 `json:"audit_entries"`
	Uploads      int   `json:"uploads"`
	Artifacts    int   `json:"artifacts"`
	// DatabaseBytes is keystone.db and its write-ahead log, FilesBytes the
	// files folder and ArtifactsBytes the artifacts folder, read from disk.
	DatabaseBytes  int64 `json:"database_bytes"`
	FilesBytes     int64 `json:"files_bytes"`
	ArtifactsBytes int64 `json:"artifacts_bytes"`
	// Tables is the database by table, the largest first.
	Tables []TableSize `json:"tables"`
	// OthersDatabaseBytes is each other person's database.
	OthersDatabaseBytes []int64 `json:"others_database_bytes"`
}

// Result is what one run found.
type Result struct {
	Profile Profile   `json:"profile"`
	At      time.Time `json:"at"`
	// Machine is where it ran: the system, the processors, the Go version.
	Machine string `json:"machine"`
	// Generating, Measuring and Took are how long the run's parts lasted.
	Generating time.Duration `json:"generating"`
	Measuring  time.Duration `json:"measuring"`
	Took       time.Duration `json:"took"`

	Measurements []Measurement `json:"measurements"`
	Sizes        Sizes         `json:"sizes"`
	Monitor      []MonitorLine `json:"monitor"`
	// Problems is every measurement over its budget and every figure of
	// the Monitor that did not match. The run passes when it is empty.
	Problems []string `json:"problems"`
}

// Run runs the load test once with a profile and returns what it found. An
// error means the test could not run, and proves nothing either way; a
// result with Problems is a run that did not pass.
func Run(p Profile, o Options) (*Result, error) {
	budgets, err := ReadBudgets()
	if err != nil {
		return nil, err
	}
	say := o.Say
	if say == nil {
		say = func(string) {}
	}
	began := time.Now()
	res := &Result{Profile: p, At: began.UTC().Truncate(time.Second), Machine: machine()}
	g, err := openRig(p, o.Dir, nil)
	if err != nil {
		return nil, fmt.Errorf("the load test could not start: %w", err)
	}
	defer g.close()
	ctx := context.Background()

	say(fmt.Sprintf("writing the %s load: %d chats of about %d turns and a long chat of %d for the first person, %d chats each for %d more",
		p.Name, p.Chats, p.TurnsPerChat, p.LongChatTurns, p.TenantChats, p.Tenants))
	l, err := g.generate(ctx, say)
	if err != nil {
		return nil, fmt.Errorf("the load could not be written: %w", err)
	}
	res.Generating = time.Since(began)
	if l.long.leftOut == 0 {
		res.Problems = append(res.Problems, fmt.Sprintf("the long chat of %d turns fits the window of %s, so packing it leaves nothing out: raise loadtest.long_chat_turns", p.LongChatTurns, localModel.Name))
	}

	lines, err := g.monitor(WhenLoaded)
	if err != nil {
		return nil, fmt.Errorf("the Monitor could not be read: %w", err)
	}
	res.Monitor = append(res.Monitor, lines...)

	measuring := time.Now()
	if res.Measurements, err = g.measure(ctx, l, budgets, say); err != nil {
		return nil, fmt.Errorf("the measurements could not be made: %w", err)
	}
	res.Measuring = time.Since(measuring)

	if lines, err = g.monitor(WhenMeasured); err != nil {
		return nil, fmt.Errorf("the Monitor could not be read: %w", err)
	}
	res.Monitor = append(res.Monitor, lines...)

	if res.Sizes, err = g.sizes(); err != nil {
		return nil, fmt.Errorf("the sizes could not be read: %w", err)
	}
	for _, m := range res.Measurements {
		if m.Over {
			held := "95th percentile"
			if p.OnMedian {
				held = "median"
			}
			res.Problems = append(res.Problems, fmt.Sprintf("%s: its %s is %s, over its budget of %s (loadtest.budget_%s)", m.What, held, round(m.Held), round(m.Limit), m.Name))
		}
	}
	for _, line := range res.Monitor {
		if !line.Match {
			res.Problems = append(res.Problems, fmt.Sprintf("the Monitor, %s: %s is %s, and the test's own count is %s", line.When, line.Figure, line.Reported, line.Own))
		}
	}
	if log := g.log.String(); strings.Contains(log, " 500 ") {
		res.Problems = append(res.Problems, "the server answered a call with 500 during the run")
	}
	res.Took = time.Since(began)
	return res, nil
}

// machine says where a run was made.
func machine() string {
	parts := []string{runtime.GOOS + "/" + runtime.GOARCH, fmt.Sprintf("%d logical processors", runtime.NumCPU()), runtime.Version()}
	if cpu := strings.TrimSpace(os.Getenv("PROCESSOR_IDENTIFIER")); cpu != "" {
		parts = append(parts, cpu)
	}
	return strings.Join(parts, ", ")
}

// sizes reads what the load left: the first person's counts, their
// database by table, and the folders on disk.
func (g *rig) sizes() (Sizes, error) {
	who := g.people[0]
	gen := who.generated
	s := Sizes{Chats: gen.chats, Turns: gen.turns, Messages: gen.messages, Calls: len(gen.calls), Uploads: gen.uploads, Artifacts: gen.artifacts}
	db := who.store.DB()
	// Everything in the write-ahead log is put in the database file first,
	// so the file is the whole of it.
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return s, err
	}
	var err error
	if s.DatabaseBytes, err = g.databaseBytes(who); err != nil {
		return s, err
	}
	for _, other := range g.people[1:] {
		if _, err := other.store.DB().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return s, err
		}
		n, err := g.databaseBytes(other)
		if err != nil {
			return s, err
		}
		s.OthersDatabaseBytes = append(s.OthersDatabaseBytes, n)
	}
	base := filepath.Join(g.dir, "tenants", who.id)
	if s.FilesBytes, err = folderBytes(filepath.Join(base, "files")); err != nil {
		return s, err
	}
	if s.ArtifactsBytes, err = folderBytes(filepath.Join(base, "artifacts")); err != nil {
		return s, err
	}
	if err := db.QueryRow("SELECT count(*) FROM audit_log").Scan(&s.AuditEntries); err != nil {
		return s, err
	}

	// The database by table: each table's pages with its indexes', and a
	// full-text index's own tables under the index's name.
	owner := map[string]string{}
	rows, err := db.Query("SELECT name, tbl_name FROM sqlite_master WHERE type IN ('table', 'index')")
	if err != nil {
		return s, err
	}
	var tables []string
	for rows.Next() {
		var name, table string
		if err := rows.Scan(&name, &table); err != nil {
			rows.Close()
			return s, err
		}
		owner[name] = table
		if name == table {
			tables = append(tables, table)
		}
	}
	if err := rows.Close(); err != nil {
		return s, err
	}
	for name, table := range owner {
		if shadow(table, tables) {
			for _, suffix := range []string{"_data", "_idx", "_docsize", "_config", "_content"} {
				if base, ok := strings.CutSuffix(table, suffix); ok {
					owner[name] = base
				}
			}
		}
	}
	bytes := map[string]int64{}
	rows, err = db.Query("SELECT name, sum(pgsize) FROM dbstat GROUP BY name")
	if err != nil {
		return s, fmt.Errorf("reading the database's pages by table: %w", err)
	}
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			rows.Close()
			return s, err
		}
		table, ok := owner[name]
		if !ok {
			table = name
		}
		bytes[table] += n
	}
	if err := rows.Close(); err != nil {
		return s, err
	}
	for table, n := range bytes {
		ts := TableSize{Table: table, Bytes: n}
		if !strings.HasPrefix(table, "sqlite_") {
			// A count that cannot be read leaves the rows at zero.
			db.QueryRow(`SELECT count(*) FROM "` + table + `"`).Scan(&ts.Rows)
		}
		s.Tables = append(s.Tables, ts)
	}
	sort.Slice(s.Tables, func(i, j int) bool {
		if s.Tables[i].Bytes != s.Tables[j].Bytes {
			return s.Tables[i].Bytes > s.Tables[j].Bytes
		}
		return s.Tables[i].Table < s.Tables[j].Table
	})
	return s, nil
}

// folderBytes adds up the size of every file under a folder.
func folderBytes(dir string) (int64, error) {
	var n int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			n += info.Size()
		}
		return nil
	})
	return n, err
}

// round is a time as the table shows it: to a tenth of a millisecond under
// ten milliseconds, and to the millisecond above.
func round(d time.Duration) time.Duration {
	if d < 10*time.Millisecond {
		return d.Round(100 * time.Microsecond)
	}
	return d.Round(time.Millisecond)
}
