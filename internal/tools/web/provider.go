package web

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Search providers (P7-06). A provider is one search service with one key:
// two keys for one service are two providers. A person keeps providers of
// their own, with keys in their own vault, and the admin keeps providers
// for the install, with keys in the install vault. A search tries the
// person's own first, then the install's, and moves on from one that is
// out of quota, rate limited, down or refusing its key.
//
// A kind of provider is added by its adapter, in a file of its own that
// calls register from init: Tavily's is in tavily.go. P7-07 adds Brave,
// Exa, Serper and SearXNG the same way, and nothing else changes.

// The kinds of search provider the spec names. A kind can be used once its
// adapter is registered.
const (
	KindTavily  = "tavily"
	KindBrave   = "brave"
	KindExa     = "exa"
	KindSerper  = "serper"
	KindSearXNG = "searxng"
)

// Kinds lists the kinds the spec names, in its order.
var Kinds = []string{KindTavily, KindBrave, KindExa, KindSerper, KindSearXNG}

// ResetsRolling is the value of Provider.Resets for a quota that runs for
// 30 days from its first use, rather than from a day of the month.
const ResetsRolling = "rolling"

// rollingWindow is how long a rolling quota lasts from its first use.
const rollingWindow = 30 * 24 * time.Hour

// Provider is one search provider's entry, kept like a model registry
// entry: a person's own in their database, the install's in data/admin/.
type Provider struct {
	// Name is short and unique among the person's own providers and the
	// install's: 1 to 40 lower-case letters, digits, dashes and
	// underscores.
	Name string `json:"name"`
	// Kind is tavily, brave, exa, serper or searxng.
	Kind string `json:"kind"`
	// Endpoint is the base address. Empty is the kind's default; SearXNG
	// has none.
	Endpoint string `json:"endpoint,omitempty"`
	// Key is the name of the secret in the vault, never the key itself:
	// search.<name>, or provider.tavily for the provider P7-06 moved from
	// P7-03. Empty for a kind that takes no key.
	Key string `json:"key,omitempty"`
	// MonthlyQuota is how many searches a month the provider allows, when
	// it is known. Zero is unknown.
	MonthlyQuota int `json:"monthly_quota,omitempty"`
	// FreeQuota is how many of those cost nothing. Zero is none.
	FreeQuota int `json:"free_quota,omitempty"`
	// Resets is the day of the month the quota starts again ("1" to "31"),
	// or "rolling" for 30 days from first use. Empty is not known; Keystone
	// then counts by the calendar month, in UTC.
	Resets string `json:"resets,omitempty"`
	// CostPerSearch is in US dollars, for a search beyond the free quota.
	CostPerSearch float64 `json:"cost_per_search,omitempty"`
	// Enabled says whether the provider is used at all.
	Enabled bool `json:"enabled"`
}

// Kind is what an adapter registers for its kind of provider.
type Kind struct {
	// Name is the kind, as an entry names it.
	Name string
	// Title is the service's name as a person reads it, such as "Tavily".
	Title string
	// NoKey says the kind takes no key, as a SearXNG on the owner's machine.
	NoKey bool
	// Unmetered says the provider has no quota at all, so the weighted
	// strategy uses it only after the others.
	Unmetered bool
	// New makes the adapter for one entry, with the web tools' options.
	New func(p Provider, o Options) Adapter
	// CheckEntry checks what only this kind requires of an entry, such as
	// a local address. It may be nil.
	CheckEntry func(p Provider) error
}

// kinds are the registered kinds, by name. Only init functions write it.
var kinds = map[string]Kind{}

// register adds a kind. Each adapter's file calls it from init.
func register(k Kind) { kinds[k.Name] = k }

// Registered reports whether a kind's adapter is in this build.
func Registered(kind string) bool {
	_, ok := kinds[kind]
	return ok
}

// Query is what a search asks a provider.
type Query struct {
	Text  string
	Count int
}

// Quota is what a provider reported is left of its quota.
type Quota struct {
	// Left is the searches left in the current period, when Known.
	Left  int
	Known bool
	// Reset is when the provider said the quota starts again. Zero when it
	// did not say.
	Reset time.Time
}

// Answer is a provider's answer to one search.
type Answer struct {
	Results []Result
	// Quota is what the provider reported is left with this answer, as
	// Brave does in its headers. Most providers report nothing.
	Quota Quota
}

// Adapter is one kind of search provider, set up for one entry.
type Adapter interface {
	// Search sends the query with the key and returns at most q.Count
	// results, best first. A failure should be a *Failure, which says why;
	// any other error counts as the provider being unavailable. No error's
	// text holds the key.
	Search(ctx context.Context, key string, q Query) (Answer, error)
}

// Checker is an adapter with a free check of its quota, one that costs
// nothing, such as Tavily's usage check. Soft tests use it.
type Checker interface {
	Check(ctx context.Context, key string) (Quota, error)
}

// Why says why a provider did not answer.
type Why string

const (
	// WhyQuota: the quota is spent. The provider goes out of the rotation.
	WhyQuota Why = "quota"
	// WhyRate: a rate limit on a short window. The provider cools.
	WhyRate Why = "rate"
	// WhyUnavailable: the provider's own failure: it could not be reached,
	// it timed out, or it answered with a server error or something that
	// is not an answer.
	WhyUnavailable Why = "unavailable"
	// WhyRefused: the provider refused the key.
	WhyRefused Why = "refused"
	// WhyMalformed: the provider called the request malformed. A search
	// does not move on for this: that would hide a bug.
	WhyMalformed Why = "malformed"
)

// Failure is why a provider did not answer, with words for the person.
type Failure struct {
	Why Why
	// Wait is how long the provider asked to wait, for a rate limit. Zero
	// when it did not say.
	Wait time.Duration
	// Reset is when the provider said its quota starts again. Zero when it
	// did not say.
	Reset time.Time
	// Text is for the model and the person to read. It never holds the
	// key.
	Text string
}

func (f *Failure) Error() string { return f.Text }

// failureOf reads an adapter's error. One that is not a *Failure counts as
// the provider being unavailable.
func failureOf(err error) *Failure {
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	return &Failure{Why: WhyUnavailable, Text: err.Error()}
}

// searcherAdapter lets a Searcher, which Options may give, stand for every
// provider.
type searcherAdapter struct{ s Searcher }

func (a searcherAdapter) Search(ctx context.Context, key string, q Query) (Answer, error) {
	results, err := a.s.Search(ctx, key, q.Text, q.Count)
	return Answer{Results: results}, err
}

var (
	providerName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	resetDay     = regexp.MustCompile(`^([1-9]|[12][0-9]|3[01])$`)
)

// KeyFor is the name of the secret that holds a provider's key in its
// vault: search.<name>.
func KeyFor(name string) string { return "search." + name }

// Check checks an entry before it is kept. Its key is kept as
// search.<name>, except for the Tavily entry moved from P7-03, whose key
// stays provider.tavily.
func (p Provider) Check() error {
	k, ok := kinds[p.Kind]
	switch {
	case !providerName.MatchString(p.Name):
		return fmt.Errorf("%q is not a provider's name: use 1 to 40 lower-case letters, digits, dashes and underscores, starting with a letter or a digit", p.Name)
	case !ok && contains(Kinds, p.Kind):
		return fmt.Errorf("the %s kind of search provider is not in this build yet", p.Kind)
	case !ok:
		return fmt.Errorf("%q is not a kind of search provider: use one of %s", p.Kind, strings.Join(Kinds, ", "))
	case p.MonthlyQuota < 0 || p.FreeQuota < 0:
		return errors.New("a quota cannot be negative")
	case p.MonthlyQuota > 0 && p.FreeQuota > p.MonthlyQuota:
		return errors.New("the free quota cannot be more than the monthly quota")
	case p.Resets != "" && p.Resets != ResetsRolling && !resetDay.MatchString(p.Resets):
		return fmt.Errorf("resets is a day of the month, 1 to 31, or %q", ResetsRolling)
	case p.CostPerSearch < 0 || p.CostPerSearch > 100:
		return errors.New("the cost of a search is from 0 to 100 US dollars")
	}
	if p.Endpoint != "" {
		u, err := url.Parse(p.Endpoint)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			return errors.New("a provider's endpoint must be an http or https address with no user name")
		}
	}
	switch {
	case k.NoKey && p.Key != "":
		return fmt.Errorf("a %s provider takes no key", k.Title)
	case !k.NoKey && p.Key != KeyFor(p.Name) && !(p.Kind == KindTavily && p.Key == KeyName):
		return fmt.Errorf("the key of the provider %s is kept as %s", p.Name, KeyFor(p.Name))
	}
	if k.CheckEntry != nil {
		return k.CheckEntry(p)
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// costMicros is a dollar amount in millionths of a dollar.
func costMicros(usd float64) int64 { return int64(usd*1e6 + 0.5) }

// resetDayOf is the day of the month an entry's quota starts again, or 0.
func resetDayOf(p Provider) int {
	if p.Resets == "" || p.Resets == ResetsRolling {
		return 0
	}
	d, _ := strconv.Atoi(p.Resets)
	return d
}
