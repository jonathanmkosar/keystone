package web

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Where providers are kept. A person's own are in their database, with
// their keys in their vault; the install's are in data/admin/search.db,
// with their keys in the install vault, data/admin/secrets.bin. Both have
// the same four tables (the tenant migration
// 20261128T010000_search_providers.sql, and installSchema below), and the
// same code reads both.

// installFile is the install's search database, in data/admin/.
const installFile = "search.db"

// installSchema is the install's tables: the tenant migration's, word for
// word.
const installSchema = `
CREATE TABLE IF NOT EXISTS search_providers (
	name          TEXT PRIMARY KEY,
	position      INTEGER NOT NULL,
	kind          TEXT NOT NULL,
	endpoint      TEXT NOT NULL DEFAULT '',
	key_name      TEXT NOT NULL DEFAULT '',
	monthly_quota INTEGER NOT NULL DEFAULT 0 CHECK (monthly_quota >= 0),
	free_quota    INTEGER NOT NULL DEFAULT 0 CHECK (free_quota >= 0),
	resets        TEXT NOT NULL DEFAULT '',
	cost_micros   INTEGER NOT NULL DEFAULT 0 CHECK (cost_micros >= 0),
	enabled       INTEGER NOT NULL DEFAULT 1,
	added_at      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS search_states (
	name  TEXT PRIMARY KEY,
	state TEXT NOT NULL,
	data  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS search_counts (
	name        TEXT NOT NULL,
	month       TEXT NOT NULL,
	searches    INTEGER NOT NULL DEFAULT 0,
	cost_micros INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (name, month)
);
CREATE TABLE IF NOT EXISTS search_meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);`

// The two groups of providers, as the results and the screens name them.
const (
	GroupOwn     = "own"
	GroupInstall = "install"
)

// place is one set of providers: a person's or the install's. use opens
// its database for one piece of work; a person's is the store's own, and
// the install's is opened and closed each time, so no file is held open.
type place struct {
	group string
	// id names the place for in-process locks: own:<tenant id>, or
	// install:<folder>.
	id    string
	vault *secrets.Vault
	use   func(ctx context.Context, write bool, f func(db *sql.DB) error) error
}

// own is a person's place.
func own(s *tenant.Store) place {
	return place{group: GroupOwn, id: "own:" + s.TenantID(), vault: s.Secrets(),
		use: func(_ context.Context, _ bool, f func(*sql.DB) error) error { return f(s.DB()) }}
}

// placeLocks keeps one lock for each place, so the read, change and write
// of a provider's state or of the list are never interleaved in this
// process.
var placeLocks sync.Map

func (p place) lock() func() {
	m, _ := placeLocks.LoadOrStore(p.id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

const timeLayout = "2006-01-02T15:04:05.000Z"

// list returns the place's providers, in their order.
func (p place) list(ctx context.Context) ([]Provider, error) {
	var out []Provider
	err := p.use(ctx, false, func(db *sql.DB) error {
		if db == nil {
			return nil
		}
		rows, err := db.QueryContext(ctx, `SELECT name, kind, endpoint, key_name, monthly_quota, free_quota, resets, cost_micros, enabled, trusted
			FROM search_providers ORDER BY position, name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var pr Provider
			var cost int64
			if err := rows.Scan(&pr.Name, &pr.Kind, &pr.Endpoint, &pr.Key, &pr.MonthlyQuota, &pr.FreeQuota, &pr.Resets, &cost, &pr.Enabled, &pr.Trusted); err != nil {
				return err
			}
			pr.CostPerSearch = float64(cost) / 1e6
			out = append(out, pr)
		}
		return rows.Err()
	})
	return out, err
}

// state returns a provider's kept state. A provider with none is ready.
func (p place) state(ctx context.Context, db *sql.DB, name string) (State, error) {
	if db == nil {
		return State{}, nil
	}
	var raw string
	err := db.QueryRowContext(ctx, "SELECT data FROM search_states WHERE name = ?", name).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return State{}, fmt.Errorf("the state of the search provider %s does not read: %w", name, err)
	}
	return s, nil
}

func (p place) saveState(ctx context.Context, db *sql.DB, name string, s State) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO search_states(name, state, data) VALUES(?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET state = excluded.state, data = excluded.data`, name, string(s.name()), string(raw))
	return err
}

// change reads a provider's state, applies f, and writes what f returns
// when it differs, under the place's lock.
func (p place) change(ctx context.Context, name string, f func(State) State) (State, error) {
	defer p.lock()()
	var out State
	err := p.use(ctx, true, func(db *sql.DB) error {
		was, err := p.state(ctx, db, name)
		if err != nil {
			return err
		}
		out = f(was)
		if sameState(was, out) {
			return nil
		}
		return p.saveState(ctx, db, name, out)
	})
	return out, err
}

func sameState(a, b State) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// month is the calendar month a count is kept under, in UTC.
func month(t time.Time) string { return t.UTC().Format("2006-01") }

// count adds one search, and what it cost, to the provider's month.
func (p place) count(ctx context.Context, name string, now time.Time, cost int64) error {
	return p.use(ctx, true, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO search_counts(name, month, searches, cost_micros) VALUES(?, ?, 1, ?)
			ON CONFLICT(name, month) DO UPDATE SET searches = searches + 1, cost_micros = cost_micros + excluded.cost_micros`,
			name, month(now), cost)
		return err
	})
}

// Count is one provider's searches and cost in one month.
type Count struct {
	Searches   int   `json:"searches"`
	CostMicros int64 `json:"cost_micros"`
}

func (p place) counts(ctx context.Context, now time.Time) (map[string]Count, error) {
	out := map[string]Count{}
	err := p.use(ctx, false, func(db *sql.DB) error {
		if db == nil {
			return nil
		}
		rows, err := db.QueryContext(ctx, "SELECT name, searches, cost_micros FROM search_counts WHERE month = ?", month(now))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			var c Count
			if err := rows.Scan(&name, &c.Searches, &c.CostMicros); err != nil {
				return err
			}
			out[name] = c
		}
		return rows.Err()
	})
	return out, err
}

// keyOf reads a provider's key from its place's vault. A kind that takes
// no key has none, and that is not missing.
func (p place) keyOf(pr Provider) (key string, ok bool, err error) {
	if pr.Key == "" {
		return "", true, nil
	}
	key, ok, err = p.vault.Get(pr.Key)
	key = strings.TrimSpace(key)
	return key, ok && key != "", err
}

// keyPrint is a short fingerprint of a key, kept with a refused state so
// a changed key is seen. It is not the key, and the key cannot be read
// back from it.
func keyPrint(key string) string {
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("keystone search key fingerprint v1\x00" + key))
	return hex.EncodeToString(sum[:8])
}

// View is one provider as the screens show it: its entry, its state, what
// is left, and its searches and cost this month. It never holds a key, only
// whether one is stored.
type View struct {
	Provider
	Group  string    `json:"group"`
	State  StateName `json:"state"`
	Why    string    `json:"why,omitempty"`
	Until  time.Time `json:"until,omitempty"`
	KeySet bool      `json:"key_set"`
	// Left is what is left of the quota, and LeftFrom where that comes
	// from: "provider" or "count". Nil when it is not known.
	Left     *int   `json:"left,omitempty"`
	LeftFrom string `json:"left_from,omitempty"`
	// Searches and CostMicros are this calendar month's, in UTC.
	Searches   int   `json:"searches"`
	CostMicros int64 `json:"cost_micros"`
}

// Providers is one set of search providers, a person's own (Own) or the
// install's (OpenInstall), and what the screens do with them (P7-08). No
// method returns a key.
type Providers struct {
	place place
	// others lists the names in the other set, which a new provider's name
	// must not take. It may be nil.
	others func(ctx context.Context) ([]string, error)
	// ready makes the place ready to keep a key: the install's folder. It
	// may be nil.
	ready func() error
	now   func() time.Time
}

// Own is a person's own search providers. install, which may be nil, is
// the install's, whose names a person's new provider may not take.
func Own(s *tenant.Store, install *Install) *Providers {
	ps := &Providers{place: own(s), now: time.Now}
	if install != nil {
		ps.others = func(ctx context.Context) ([]string, error) { return names(install.place.list(ctx)) }
	}
	return ps
}

func names(list []Provider, err error) ([]string, error) {
	out := make([]string, len(list))
	for i, p := range list {
		out[i] = p.Name
	}
	return out, err
}

// List returns every provider in its order, as the screens show it.
func (ps *Providers) List(ctx context.Context) ([]View, error) {
	list, err := ps.place.list(ctx)
	if err != nil {
		return nil, err
	}
	now := ps.now()
	counts, err := ps.place.counts(ctx, now)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(list))
	for _, pr := range list {
		key, keyOK, err := ps.place.keyOf(pr)
		if err != nil {
			return nil, errors.New("the vault that holds the search keys cannot be read")
		}
		var st State
		if err := ps.place.use(ctx, false, func(db *sql.DB) error {
			st, err = ps.place.state(ctx, db, pr.Name)
			return err
		}); err != nil {
			return nil, err
		}
		st = settle(pr, st, now, keyPrint(key))
		v := View{Provider: pr, Group: ps.place.group, State: st.name(), Why: st.Why, Until: st.Until, KeySet: keyOK && pr.Key != "",
			Searches: counts[pr.Name].Searches, CostMicros: counts[pr.Name].CostMicros}
		if left, from := leftOf(pr, st); from != "" {
			v.Left, v.LeftFrom = &left, from
		}
		out = append(out, v)
	}
	return out, nil
}

// Add keeps a new provider, last in the order, and stores its key when one
// is given. Its key name is set here: search.<name>.
func (ps *Providers) Add(ctx context.Context, pr Provider, key string) error {
	if k, ok := kinds[pr.Kind]; ok && !k.NoKey {
		pr.Key = KeyFor(pr.Name)
	}
	if err := pr.Check(); err != nil {
		return err
	}
	if err := ps.checkTrusted(pr); err != nil {
		return err
	}
	if ps.others != nil {
		taken, err := ps.others(ctx)
		if err != nil {
			return err
		}
		if contains(taken, pr.Name) {
			return fmt.Errorf("the name %s is taken by another search provider", pr.Name)
		}
	}
	if key = strings.TrimSpace(key); key != "" {
		if pr.Key == "" {
			return errors.New("this kind of provider takes no key")
		}
		if err := ps.setKey(pr.Key, key); err != nil {
			return err
		}
	}
	defer ps.place.lock()()
	return ps.place.use(ctx, true, func(db *sql.DB) error {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM search_providers WHERE name = ?", pr.Name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("there is already a search provider named %s", pr.Name)
		}
		_, err := db.ExecContext(ctx, `INSERT INTO search_providers(name, position, kind, endpoint, key_name, monthly_quota, free_quota, resets, cost_micros, enabled, added_at, trusted)
			VALUES(?, (SELECT coalesce(max(position), -1) + 1 FROM search_providers), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			pr.Name, pr.Kind, pr.Endpoint, pr.Key, pr.MonthlyQuota, pr.FreeQuota, pr.Resets, costMicros(pr.CostPerSearch), pr.Enabled, ps.now().UTC().Format(timeLayout), pr.Trusted)
		return err
	})
}

// Update changes a provider's entry, found by its name. Its kind and key
// name stay as they are.
func (ps *Providers) Update(ctx context.Context, pr Provider) error {
	defer ps.place.lock()()
	return ps.place.use(ctx, true, func(db *sql.DB) error {
		var kind, keyName string
		err := db.QueryRowContext(ctx, "SELECT kind, key_name FROM search_providers WHERE name = ?", pr.Name).Scan(&kind, &keyName)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("there is no search provider named %s", pr.Name)
		}
		if err != nil {
			return err
		}
		pr.Kind, pr.Key = kind, keyName
		if err := pr.Check(); err != nil {
			return err
		}
		if err := ps.checkTrusted(pr); err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `UPDATE search_providers SET endpoint = ?, monthly_quota = ?, free_quota = ?, resets = ?, cost_micros = ?, enabled = ?, trusted = ? WHERE name = ?`,
			pr.Endpoint, pr.MonthlyQuota, pr.FreeQuota, pr.Resets, costMicros(pr.CostPerSearch), pr.Enabled, pr.Trusted, pr.Name)
		return err
	})
}

// SetKey stores a provider's key. A refused provider comes back with a new
// key by itself, at its next search.
func (ps *Providers) SetKey(ctx context.Context, name, key string) error {
	list, err := ps.place.list(ctx)
	if err != nil {
		return err
	}
	for _, pr := range list {
		if pr.Name == name {
			if pr.Key == "" {
				return errors.New("this kind of provider takes no key")
			}
			if key = strings.TrimSpace(key); key == "" {
				return errors.New("a key cannot be empty")
			}
			return ps.setKey(pr.Key, key)
		}
	}
	return fmt.Errorf("there is no search provider named %s", name)
}

func (ps *Providers) setKey(name, key string) error {
	if ps.ready != nil {
		if err := ps.ready(); err != nil {
			return err
		}
	}
	return ps.place.vault.Set(name, key)
}

// Remove removes a provider, its state and, when it is kept as
// search.<name>, its key. Its monthly counts stay, as a record.
func (ps *Providers) Remove(ctx context.Context, name string) error {
	unlock := ps.place.lock()
	var keyName string
	err := ps.place.use(ctx, true, func(db *sql.DB) error {
		err := db.QueryRowContext(ctx, "SELECT key_name FROM search_providers WHERE name = ?", name).Scan(&keyName)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("there is no search provider named %s", name)
		}
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, "DELETE FROM search_providers WHERE name = ?", name); err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, "DELETE FROM search_states WHERE name = ?", name)
		return err
	})
	unlock()
	if err != nil {
		return err
	}
	if keyName == KeyFor(name) {
		return ps.place.vault.Delete(keyName)
	}
	return nil
}

// Order puts the providers in the order given, which must name each one
// once.
func (ps *Providers) Order(ctx context.Context, order []string) error {
	defer ps.place.lock()()
	return ps.place.use(ctx, true, func(db *sql.DB) error {
		have, err := names(ps.place.list(ctx))
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, n := range order {
			if !contains(have, n) || seen[n] {
				return errors.New("the order must name each search provider once")
			}
			seen[n] = true
		}
		if len(seen) != len(have) {
			return errors.New("the order must name each search provider once")
		}
		for i, n := range order {
			if _, err := db.ExecContext(ctx, "UPDATE search_providers SET position = ? WHERE name = ?", i, n); err != nil {
				return err
			}
		}
		return nil
	})
}

// Install is the install's search providers, kept in data/admin/ for every
// person. Only the admin sets them, and nothing returns their keys: not to
// a person, and not to the admin once a key is written.
type Install struct {
	*Providers
	folder string
}

// installs keeps one Install for each data folder, so the server's tool
// and its sweep share their locks.
var installs sync.Map

// OpenInstall returns the install's search providers. It writes nothing:
// data/admin/search.db is made when the first provider is added.
func OpenInstall(in *tenant.Install) (*Install, error) {
	if in == nil {
		return nil, errors.New("no data folder")
	}
	folder := in.AdminFolder()
	if v, ok := installs.Load(folder); ok {
		return v.(*Install), nil
	}
	vault := in.InstallVault()
	path := filepath.Join(folder, installFile)
	use := func(ctx context.Context, write bool, f func(db *sql.DB) error) error {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			if !write {
				return f(nil)
			}
			if err := os.MkdirAll(folder, 0o700); err != nil {
				return err
			}
		}
		db, err := openInstallDB(path)
		if err != nil {
			return err
		}
		defer db.Close()
		return f(db)
	}
	inst := &Install{folder: folder, Providers: &Providers{now: time.Now, place: place{group: GroupInstall, id: "install:" + folder, vault: vault, use: use},
		ready: func() error { return os.MkdirAll(folder, 0o700) }}}
	// A new install provider's name may not be any person's own.
	inst.others = func(ctx context.Context) ([]string, error) {
		var all []string
		for _, t := range in.Tenants() {
			s, err := in.Store(t.ID)
			if err != nil {
				return nil, err
			}
			n, err := names(own(s).list(ctx))
			s.Close()
			if err != nil {
				return nil, err
			}
			all = append(all, n...)
		}
		return all, nil
	}
	v, _ := installs.LoadOrStore(folder, inst)
	return v.(*Install), nil
}

// openInstallDB opens data/admin/search.db as a person's database is
// opened, and makes its tables.
func openInstallDB(path string) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(path) +
		"?_txlock=immediate&_pragma=journal_mode(wal)&_pragma=synchronous(normal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(installSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("data/admin/%s: %w", installFile, err)
	}
	if err := installTrusted(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("data/admin/%s: %w", installFile, err)
	}
	return db, nil
}
