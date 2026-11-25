// Package personmap keeps the two short texts about a person that start
// every call to a model: the profile and the map.
//
// The profile is a few facts and preferences in the person's own words. The
// map is an outline of what their knowledge store holds, made from the
// store's catalog: each space, the kinds of page in it, and a few titles. It
// tells a model what there is to look up. Both are kept in the tenant's own
// database, and the map is built again when the catalog changes.
//
// The map is made in two versions, because of fixed rule 3 in the plan:
// content from a sensitive space reaches only models the registry marks
// local or trusted. The whole version names everything. The open version
// leaves every sensitive space out and says only how many were held back.
// Map.Text is the one way to either text, and it takes a plain yes or no.
//
// The package enforces that rule, so it reads no setting. Its sizes come in
// as Limits, which the caller fills from settings/knowledge.json. It opens
// no connection. docs/architecture/knowledge-profile-and-map.md describes
// it.
package personmap

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The keys of the settings a caller fills Limits from. The first four are in
// settings/knowledge.json. The fifth is the budget's, in
// settings/models.json, so the map is measured the way a call is.
const (
	SettingProfileMaxChars  = "knowledge.profile_max_chars"
	SettingMapMaxTokens     = "knowledge.map_max_tokens"
	SettingMapTitlesPerKind = "knowledge.map_titles_per_kind"
	SettingMapTitleMaxChars = "knowledge.map_title_max_chars"
	SettingCharsPerToken    = "budget.chars_per_token"
)

// The widest each limit may be set.
const (
	MaxProfileChars  = 20000
	MaxMapTokens     = 20000
	MaxTitlesPerKind = 20
	MinTitleChars    = 8
	MaxTitleChars    = 200
)

// Limits are the sizes the profile and the map are held to.
type Limits struct {
	// ProfileMaxChars is the longest profile, in characters.
	ProfileMaxChars int
	// MapMaxTokens is the most tokens either version of the map may be
	// estimated at. Zero means no map.
	MapMaxTokens int
	// CharsPerToken is how many characters count as one token in that
	// estimate. Lower counts more tokens, which is the safe side.
	CharsPerToken float64
	// TitlesPerKind is the most page titles shown under one kind. Zero
	// shows none.
	TitlesPerKind int
	// TitleMaxChars is the longest a space's name, a kind or a title is
	// shown at.
	TitleMaxChars int
}

// ErrLimits means the limits themselves are wrong.
var ErrLimits = errors.New("personmap: bad limits")

// CheckLimits refuses limits nothing can be built with.
func CheckLimits(l Limits) error {
	switch {
	case l.ProfileMaxChars < 1 || l.ProfileMaxChars > MaxProfileChars:
		return fmt.Errorf("%w: %s is from 1 to %d", ErrLimits, SettingProfileMaxChars, MaxProfileChars)
	case l.MapMaxTokens < 0 || l.MapMaxTokens > MaxMapTokens:
		return fmt.Errorf("%w: %s is from 0 to %d", ErrLimits, SettingMapMaxTokens, MaxMapTokens)
	case !(l.CharsPerToken > 0) || math.IsInf(l.CharsPerToken, 0):
		return fmt.Errorf("%w: %s must be more than zero", ErrLimits, SettingCharsPerToken)
	case l.TitlesPerKind < 0 || l.TitlesPerKind > MaxTitlesPerKind:
		return fmt.Errorf("%w: %s is from 0 to %d", ErrLimits, SettingMapTitlesPerKind, MaxTitlesPerKind)
	case l.TitleMaxChars < MinTitleChars || l.TitleMaxChars > MaxTitleChars:
		return fmt.Errorf("%w: %s is from %d to %d", ErrLimits, SettingMapTitleMaxChars, MinTitleChars, MaxTitleChars)
	}
	return nil
}

// EstimateTokens says about how many tokens a text is: its characters over
// charsPerToken, rounded up. It is the budget package's estimate for plain
// text, with no margin added.
func EstimateTokens(text string, charsPerToken float64) int {
	if text == "" {
		return 0
	}
	return int(math.Ceil(float64(utf8.RuneCountInString(text)) / charsPerToken))
}

// Map is the outline of one person's knowledge store, in both versions.
type Map struct {
	whole string
	open  string

	// Spaces and Pages count everything in the catalog the map was built
	// from, sensitive spaces too. They are for a screen the person sees,
	// and never for a model.
	Spaces int
	Pages  int
	// Withheld is how many spaces the open version leaves out because they
	// are sensitive.
	Withheld int
	// Built is when the map was made. It is zero for a map with nothing
	// behind it.
	Built time.Time
	// StoreErr is why the map is empty when the knowledge store could not
	// be read. It is nil otherwise.
	StoreErr error
}

// Text returns the map for one model. Pass the model's own answer to
// whether it may read sensitive content, which is Model.MayReadSensitive in
// the registry. With false, the text holds nothing from a sensitive space:
// no name, no kind, no title and no count of pages, only how many spaces
// were held back.
func (m Map) Text(mayReadSensitive bool) string {
	if mayReadSensitive {
		return m.whole
	}
	return m.open
}

// WithheldFor is how many spaces the text for that model leaves out as
// sensitive: none for a model that may read them.
func (m Map) WithheldFor(mayReadSensitive bool) int {
	if mayReadSensitive {
		return 0
	}
	return m.Withheld
}

var now = time.Now

// shape names the limits and the layout a map was built with, so a map kept
// from other limits is built again.
func shape(l Limits) string {
	return fmt.Sprintf("v%d tokens=%d chars=%s titles=%d title=%d", layoutVersion, l.MapMaxTokens,
		strconv.FormatFloat(l.CharsPerToken, 'g', -1, 64), l.TitlesPerKind, l.TitleMaxChars)
}

// catalogHash is the key for a store that has no catalog marker: a hash of
// everything in the catalog the map is made from, labels included.
func catalogHash(entries []knowledge.Entry) string {
	h := sha256.New()
	field := func(s string) {
		h.Write([]byte(strconv.Itoa(len(s))))
		h.Write([]byte{':'})
		h.Write([]byte(s))
	}
	for _, e := range entries {
		field(e.ID)
		field(e.Space)
		field(string(e.Label))
		field(e.Path)
		field(e.Title)
		field(e.Kind)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Load returns the person's map, building it again when the catalog has
// changed since it was last built.
//
// A store with the CatalogMarker capability is asked for its marker, and the
// kept map is used while the marker is the one it was built from. A store
// without it has its catalog read and hashed each time. A change of limits
// builds the map again too.
//
// When the store cannot be read the map is empty, with the reason in
// StoreErr and no error, so a chat carries on without it. The kept map is
// not used then, because a space may have been made sensitive since. The
// error returned is the tenant's own database failing, bad limits, or a
// context that is done.
//
// ks may be nil, which reads as a store that holds nothing.
func Load(ctx context.Context, t *tenant.Store, ks knowledge.Store, l Limits) (Map, error) {
	if err := ctx.Err(); err != nil {
		return Map{}, err
	}
	if t == nil {
		return Map{}, errors.New("personmap: no tenant")
	}
	if err := CheckLimits(l); err != nil {
		return Map{}, err
	}
	if ks == nil || l.MapMaxTokens == 0 {
		return Map{}, nil
	}
	down := func(err error) (Map, error) {
		if ctx.Err() != nil {
			return Map{}, ctx.Err()
		}
		return Map{StoreErr: err}, nil
	}

	// The marker is read before the catalog. If the catalog changes between
	// the two reads, the map is kept under the older marker and is built
	// again next time, which costs one build and shows nothing stale.
	var (
		key     string
		entries []knowledge.Entry
		read    bool
	)
	if ks.Capabilities().CatalogMarker {
		marker, err := ks.CatalogMarker(ctx)
		if err != nil {
			return down(err)
		}
		key = "marker " + marker
	} else {
		var err error
		if entries, err = ks.Catalog(ctx); err != nil {
			return down(err)
		}
		key, read = "catalog "+catalogHash(entries), true
	}

	want := shape(l)
	kept, ok, err := readKept(ctx, t, key, want)
	if err != nil {
		return Map{}, err
	}
	if ok {
		return kept, nil
	}
	if !read {
		if entries, err = ks.Catalog(ctx); err != nil {
			return down(err)
		}
	}
	m := Build(entries, l)
	m.Built = now().UTC().Truncate(time.Second)
	_, err = t.DB().ExecContext(ctx, `INSERT INTO person_map(id, catalog_key, shape, whole_text, open_text, spaces, pages, withheld_spaces, built_at)
		VALUES(1, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET catalog_key = excluded.catalog_key, shape = excluded.shape,
			whole_text = excluded.whole_text, open_text = excluded.open_text, spaces = excluded.spaces,
			pages = excluded.pages, withheld_spaces = excluded.withheld_spaces, built_at = excluded.built_at`,
		key, want, m.whole, m.open, m.Spaces, m.Pages, m.Withheld, m.Built.Format(time.RFC3339))
	if err != nil {
		return Map{}, fmt.Errorf("personmap: keeping the map: %w", err)
	}
	return m, nil
}

// readKept returns the kept map when it was built from this key with this
// shape.
func readKept(ctx context.Context, t *tenant.Store, key, want string) (Map, bool, error) {
	var (
		m              Map
		keptKey, keptS string
		built          string
	)
	err := t.DB().QueryRowContext(ctx, `SELECT catalog_key, shape, whole_text, open_text, spaces, pages, withheld_spaces, built_at
		FROM person_map WHERE id = 1`).Scan(&keptKey, &keptS, &m.whole, &m.open, &m.Spaces, &m.Pages, &m.Withheld, &built)
	if errors.Is(err, sql.ErrNoRows) {
		return Map{}, false, nil
	}
	if err != nil {
		return Map{}, false, fmt.Errorf("personmap: reading the kept map: %w", err)
	}
	if keptKey != key || keptS != want {
		return Map{}, false, nil
	}
	m.Built, _ = time.Parse(time.RFC3339, built)
	return m, true, nil
}

// Prefix is what the start of a call carries about the person: the profile
// and the map, as texts for one model, each with an estimate of its size.
type Prefix struct {
	// Profile is the person's profile, cut to its limit. Empty when they
	// have written none.
	Profile string
	// ProfileTokens is EstimateTokens of Profile.
	ProfileTokens int
	// Map is the map for this model. Empty when the store holds nothing,
	// the person has no store, or the store could not be read.
	Map string
	// MapTokens is EstimateTokens of Map. It is never more than the limit.
	MapTokens int
	// WithheldSpaces is how many sensitive spaces were left out of Map for
	// this model, for the turn inspector.
	WithheldSpaces int
	// MapBuilt is when the map was made.
	MapBuilt time.Time
	// StoreErr is why Map is empty when the knowledge store could not be
	// read. It is nil otherwise.
	StoreErr error
}

// ForContext returns the profile and the map for one call. It is shaped for
// the context builder's slot for them.
//
// t is the tenant's store handle and ks the tenant's opened knowledge
// store, or nil. mayReadSensitive is the answer of the model the call goes
// to, Model.MayReadSensitive in the registry. Both texts are the same from
// one call to the next until the person edits the profile or the catalog
// changes, so they can sit in the part of a call a provider caches.
//
// A knowledge store that cannot be read is not an error: Map is empty and
// StoreErr says why.
func ForContext(ctx context.Context, t *tenant.Store, ks knowledge.Store, mayReadSensitive bool, l Limits) (Prefix, error) {
	if err := CheckLimits(l); err != nil {
		return Prefix{}, err
	}
	p, err := ReadProfile(ctx, t)
	if err != nil {
		return Prefix{}, err
	}
	m, err := Load(ctx, t, ks, l)
	if err != nil {
		return Prefix{}, err
	}
	out := Prefix{
		Profile:        cut(p.Text, l.ProfileMaxChars),
		Map:            m.Text(mayReadSensitive),
		WithheldSpaces: m.WithheldFor(mayReadSensitive),
		MapBuilt:       m.Built,
		StoreErr:       m.StoreErr,
	}
	out.ProfileTokens = EstimateTokens(out.Profile, l.CharsPerToken)
	out.MapTokens = EstimateTokens(out.Map, l.CharsPerToken)
	return out, nil
}
