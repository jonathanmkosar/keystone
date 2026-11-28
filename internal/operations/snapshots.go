package operations

import (
	"context"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/figures"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// Snapshots are where each limit stood at a moment, one row per limit, in
// the tenant's own database (operations_snapshots, migration
// 20261126T001900_operations.sql). The sampler writes them; a trend reads
// them. A run's budget is not kept: its usage records are its trend.

// timeLayout sorts as text in the order of time, as the other tables'.
const timeLayout = "2006-01-02T15:04:05.000Z"

// Snapshot is one limit at one moment.
type Snapshot struct {
	At   time.Time `json:"at"`
	Key  string    `json:"key"`
	Used int64     `json:"used"`
	Of   int64     `json:"of"`
}

// Keep writes snapshots. A second one for the same limit at the same moment
// takes the place of the first.
func Keep(ctx context.Context, s *tenant.Store, snaps []Snapshot) error {
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, n := range snaps {
		if n.Key == "" {
			return fmt.Errorf("operations: a snapshot needs the key of its limit")
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO operations_snapshots(at, key, used, of) VALUES(?, ?, ?, ?)",
			n.At.UTC().Format(timeLayout), n.Key, n.Used, n.Of); err != nil {
			return fmt.Errorf("operations: %w", err)
		}
	}
	return tx.Commit()
}

// Snapshots returns one limit's snapshots at or after since, oldest first.
func Snapshots(ctx context.Context, s *tenant.Store, key string, since time.Time) ([]Point, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT at, used, of FROM operations_snapshots WHERE key = ? AND at >= ? ORDER BY at",
		key, since.UTC().Format(timeLayout))
	if err != nil {
		return nil, fmt.Errorf("operations: %w", err)
	}
	defer rows.Close()
	var out []Point
	for rows.Next() {
		var p Point
		var at string
		if err := rows.Scan(&at, &p.Used, &p.Of); err != nil {
			return nil, fmt.Errorf("operations: %w", err)
		}
		if p.At, err = time.Parse(timeLayout, at); err != nil {
			return nil, fmt.Errorf("operations: a snapshot of %s has a time that cannot be read", key)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Newest returns the time of the tenant's newest snapshot, and false when
// it has none: figures.Newest, which the admin's install reads too.
func Newest(ctx context.Context, s *tenant.Store) (time.Time, bool, error) {
	return figures.Newest(ctx, s)
}

// Sample records where each of a tenant's limits stands now, but a run's
// budget, and removes the snapshots older than keep. A limit that could not
// be read is not recorded.
func Sample(ctx context.Context, s *tenant.Store, reg []settings.Setting, keep time.Duration, now time.Time) error {
	cfg, err := ReadSettings(ctx, s, reg)
	if err != nil {
		return err
	}
	readings, err := measure(ctx, s, reg, cfg, now)
	if err != nil {
		return err
	}
	var snaps []Snapshot
	for _, r := range readings {
		if r.Problem == "" {
			snaps = append(snaps, Snapshot{At: now, Key: r.Key, Used: r.Used, Of: r.Of})
		}
	}
	if err := Keep(ctx, s, snaps); err != nil {
		return err
	}
	if _, err := s.DB().ExecContext(ctx, "DELETE FROM operations_snapshots WHERE at < ?", now.Add(-keep).UTC().Format(timeLayout)); err != nil {
		return fmt.Errorf("operations: %w", err)
	}
	return nil
}

// Goer runs a function in a goroutine that ends when the server stops, and
// is waited for: the turn runner's Go.
type Goer interface {
	Go(f func(ctx context.Context)) bool
}

// Start starts the sampler under g: at once, and then every
// c.SnapshotEvery, each tenant whose newest snapshot is older than that is
// sampled. A restart therefore takes no second snapshot within the
// interval. It reports false, and starts nothing, when it cannot.
func Start(g Goer, in *tenant.Install, c Config, log *api.Log, now func() time.Time) bool {
	if g == nil || in == nil || c.SnapshotEvery <= 0 {
		return false
	}
	if now == nil {
		now = time.Now
	}
	reg, err := settings.Registry()
	if err != nil {
		log.Printf("operations: the settings registry does not read, so no snapshots are taken: %v", err)
		return false
	}
	return g.Go(func(ctx context.Context) {
		for {
			SampleDue(ctx, in, reg, c, now(), log)
			select {
			case <-ctx.Done():
				return
			case <-time.After(c.SnapshotEvery):
			}
		}
	})
}

// SampleDue samples every tenant of the install whose newest snapshot is
// older than c.SnapshotEvery, and returns how many it sampled. A tenant
// that cannot be sampled is in the log, and the others go on.
func SampleDue(ctx context.Context, in *tenant.Install, reg []settings.Setting, c Config, now time.Time, log *api.Log) int {
	n := 0
	for _, t := range in.Tenants() {
		if ctx.Err() != nil {
			return n
		}
		err := func() error {
			s, err := in.Store(t.ID)
			if err != nil {
				return err
			}
			defer s.Close()
			last, ok, err := Newest(ctx, s)
			if err != nil {
				return err
			}
			if ok && now.Sub(last) < c.SnapshotEvery {
				return nil
			}
			n++
			return Sample(ctx, s, reg, c.SnapshotKeep, now)
		}()
		if err != nil && ctx.Err() == nil {
			log.Printf("operations: tenant %s: taking a snapshot: %v", t.ID, err)
		}
	}
	return n
}
