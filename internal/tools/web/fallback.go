package web

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The fallback: one search tries the person's own ready providers, then
// the install's, in the order web.strategy gives, at most web.max_attempts
// of them, and moves on, as the model chain does (decision 0160), for a
// quota, a rate limit, an unavailable or timed-out provider, or a refused
// key. It does not move on for a request the provider called malformed:
// that would hide a bug. One search's results all come from one provider.

// finder runs searches across providers.
type finder struct {
	o       Options
	rot     Rotation
	now     func() time.Time
	pick    func() float64
	install *Install
	logf    func(format string, args ...any)
}

func newFinder(o Options) *finder {
	f := &finder{o: o, rot: o.Rotation, now: o.Now, pick: o.Rand, logf: func(string, ...any) {}}
	if f.now == nil {
		f.now = time.Now
	}
	if f.pick == nil {
		f.pick = rand.Float64
	}
	if o.Install != nil {
		if inst, err := OpenInstall(o.Install); err == nil {
			f.install = inst
		}
	}
	return f
}

// adapter makes the adapter for one entry. A Searcher in the options
// stands for every provider, as a test may want.
func (f *finder) adapter(p Provider, k Kind) Adapter {
	if f.o.Searcher != nil {
		return searcherAdapter{f.o.Searcher}
	}
	return k.New(p, f.o)
}

// places are the person's own providers, then the install's.
func (f *finder) places(s *tenant.Store) []place {
	out := []place{own(s)}
	if f.install != nil {
		out = append(out, f.install.place)
	}
	return out
}

// underTest holds the providers whose soft test is running, so there is
// never more than one test at a time of one provider.
var underTest sync.Map

// claim takes a provider's soft test, and reports false when one is
// already running. release gives it back.
func claim(pl place, name string) bool {
	_, running := underTest.LoadOrStore(pl.id+"/"+name, true)
	return !running
}

func release(pl place, name string) { underTest.Delete(pl.id + "/" + name) }

// passed is a provider a search tried before the one that answered, and
// why it was passed over.
type passed struct {
	Name string
	Why  Why
}

// found is what one search came to: the results, the provider that gave
// them, the providers tried before it, and every key that was sent, so the
// caller can take each out of what it shows.
type found struct {
	results []Result
	by      string
	passed  []passed
	keys    []string
}

// whyWords are the reasons a record gives, in a few words.
var whyWords = map[Why]string{
	WhyQuota:       "out of quota",
	WhyRate:        "rate limited",
	WhyUnavailable: "unavailable",
	WhyRefused:     "key refused",
	WhyMalformed:   "request malformed",
}

// record is the line that ends a search's result: which provider answered,
// and each one tried before it with why it was passed over. The audit log
// keeps it with the result, and Answered reads it back for the step row.
func (got found) record() string {
	if len(got.passed) == 0 {
		return "Answered by " + got.by + "."
	}
	parts := make([]string, len(got.passed))
	for i, p := range got.passed {
		parts[i] = fmt.Sprintf("%s (%s)", p.Name, whyWords[p.Why])
	}
	return fmt.Sprintf("Answered by %s, after %s.", got.by, strings.Join(parts, ", "))
}

// Answering is what a search's result says about its providers.
type Answering struct {
	// By is the provider that answered.
	By string
	// After are the providers tried before it, each with why it was passed
	// over, in a few words.
	After []Passed
}

// Passed is a provider a search tried and passed over.
type Passed struct {
	Name string
	Why  string
}

var (
	recordLine = regexp.MustCompile(`^Answered by ([a-z0-9][a-z0-9_-]*)(?:, after (.+))?\.$`)
	recordPart = regexp.MustCompile(`^([a-z0-9][a-z0-9_-]*) \(([a-z ]+)\)$`)
)

// Answered reads which provider answered a web search, and which it was
// tried after, from the result's last line. The step row says so when it
// was not the first tried (P7-08).
func Answered(text string) (Answering, bool) {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	last := text[strings.LastIndex(text, "\n")+1:]
	m := recordLine.FindStringSubmatch(last)
	if m == nil {
		return Answering{}, false
	}
	a := Answering{By: m[1]}
	if m[2] != "" {
		for _, part := range strings.Split(m[2], ", ") {
			pm := recordPart.FindStringSubmatch(part)
			if pm == nil {
				return Answering{}, false
			}
			a.After = append(a.After, Passed{Name: pm[1], Why: pm[2]})
		}
	}
	return a, true
}

// noneAnswered opens the result of a search that no provider could answer.
const noneAnswered = "Web search found no provider that could answer, so the search has no results. Try again later. Each provider:"

// find runs one search.
func (f *finder) find(ctx context.Context, s *tenant.Store, q Query) (found, error) {
	now := f.now()
	if err := moveTavily(ctx, s, now); err != nil && ctx.Err() == nil {
		f.logf("web search: tenant %s: moving provider.tavily to an own provider: %v", s.TenantID(), err)
	}
	var (
		got     found
		groups  [2][]candidate
		trial   *candidate
		waiting []string // a line for each provider not tried, and why
		usable  int
	)
	for gi, pl := range f.places(s) {
		list, err := pl.list(ctx)
		switch {
		case err != nil && pl.group == GroupInstall:
			// The install's providers failing to read leaves the person's own.
			f.logf("web search: reading the install's search providers: %v", err)
			waiting = append(waiting, "the install's providers could not be read")
			continue
		case err != nil:
			return got, errors.New("web search could not read its providers, so nothing was sent")
		}
		for _, p := range list {
			if !p.Enabled {
				continue
			}
			k, ok := kinds[p.Kind]
			if !ok {
				waiting = append(waiting, fmt.Sprintf("%s: %s providers are not in this build", p.Name, p.Kind))
				continue
			}
			key, keyOK, err := pl.keyOf(p)
			switch {
			case err != nil:
				waiting = append(waiting, p.Name+": its key could not be read from the vault, so nothing was sent to it")
				continue
			case !keyOK && pl.group == GroupOwn:
				waiting = append(waiting, fmt.Sprintf("%s: no key is stored as %s", p.Name, p.Key))
				continue
			case !keyOK:
				waiting = append(waiting, p.Name+": the admin has stored no key for it")
				continue
			}
			usable++
			print := keyPrint(key)
			st, err := pl.change(ctx, p.Name, func(st State) State { return settle(p, st, now, print) })
			if err != nil {
				return got, errors.New("web search could not read its providers, so nothing was sent")
			}
			c := candidate{place: pl, p: p, kind: k, adapter: f.adapter(p, k), key: key, print: print, st: st}
			_, checks := c.adapter.(Checker)
			switch {
			case st.name() == StateReady:
				groups[gi] = append(groups[gi], c)
			case trial == nil && testDue(st, now) && !checks && claim(pl, p.Name):
				// Without a free check, this search is the soft test: it
				// goes to the provider first, alone.
				c.trial = true
				trial = &c
			default:
				waiting = append(waiting, p.Name+": "+stateWords(pl, p, st, now))
			}
		}
	}
	if usable == 0 {
		return got, errors.New(NotSetUp)
	}
	var tries []candidate
	if trial != nil {
		tries = append(tries, *trial)
	}
	tries = append(tries, f.rot.order(groups[0], f.pick)...)
	tries = append(tries, f.rot.order(groups[1], f.pick)...)
	if len(tries) > f.rot.MaxAttempts {
		for _, c := range tries[f.rot.MaxAttempts:] {
			waiting = append(waiting, c.p.Name+": not tried, since web.max_attempts providers were tried before it")
		}
		tries = tries[:f.rot.MaxAttempts]
	}
	// What a search learns is kept even when the person stops the turn.
	keep := context.WithoutCancel(ctx)
	var tried []string
	for _, c := range tries {
		got.keys = append(got.keys, c.key)
		ans, err := c.adapter.Search(ctx, c.key, q)
		if err == nil {
			f.answered(keep, c, ans.Quota, now)
			got.results, got.by = ans.Results, c.p.Name
			return got, nil
		}
		if ctx.Err() != nil {
			if c.trial {
				release(c.place, c.p.Name)
			}
			return got, ctx.Err()
		}
		fl := failureOf(err)
		if fl.Why == WhyMalformed {
			if c.trial {
				release(c.place, c.p.Name)
			}
			return got, fmt.Errorf("%s: %s. The search was not sent to another provider, because the request itself was refused", c.p.Name, fl.Text)
		}
		f.notAnswered(keep, c, fl, now)
		got.passed = append(got.passed, passed{Name: c.p.Name, Why: fl.Why})
		tried = append(tried, c.p.Name+": "+failWords(c, fl))
	}
	return got, errors.New(noneAnswered + "\n" + strings.Join(append(tried, waiting...), "\n"))
}

// answered keeps what a provider's answer says: the provider is ready,
// unless its quota is now spent, and its count and cost for the month go
// up. A search beyond the free quota costs the entry's cost per search.
func (f *finder) answered(ctx context.Context, c candidate, q Quota, now time.Time) {
	if c.trial {
		defer release(c.place, c.p.Name)
	}
	st, err := c.place.change(ctx, c.p.Name, func(st State) State {
		return f.rot.succeeded(c.p, settle(c.p, st, now, c.print), q, now)
	})
	if err != nil {
		f.logf("web search: keeping the state of %s: %v", c.p.Name, err)
		return
	}
	var cost int64
	if c.p.CostPerSearch > 0 && !(c.p.FreeQuota > 0 && st.Used <= c.p.FreeQuota) {
		cost = costMicros(c.p.CostPerSearch)
	}
	if err := c.place.count(ctx, c.p.Name, now, cost); err != nil {
		f.logf("web search: counting a search of %s: %v", c.p.Name, err)
	}
}

// notAnswered keeps what a failure says. A failed soft test leaves the
// provider as it was and doubles the wait before the next, unless the key
// was refused.
func (f *finder) notAnswered(ctx context.Context, c candidate, fl *Failure, now time.Time) {
	if c.trial {
		defer release(c.place, c.p.Name)
	}
	st, err := c.place.change(ctx, c.p.Name, func(st State) State {
		st = settle(c.p, st, now, c.print)
		if c.trial && fl.Why != WhyRefused {
			return f.rot.testFailed(st, now)
		}
		return f.rot.failed(c.p, st, fl, c.print, now)
	})
	if err != nil {
		f.logf("web search: keeping the state of %s: %v", c.p.Name, err)
		return
	}
	if st.name() == StateRefused && c.place.group == GroupInstall {
		// The owner of an install key is the admin, who sees it on the
		// admin account's Web search (P7-08) and here.
		f.logf("web search: the install's search provider %s had its key refused", c.p.Name)
	}
}

// failWords says why a provider tried in this search did not answer. A
// refused key is named by where it is kept, never by its value.
func failWords(c candidate, fl *Failure) string {
	if fl.Why != WhyRefused {
		return fl.Text
	}
	if c.place.group == GroupInstall {
		return c.kind.Title + " refused the key the admin stored for it. The admin has to store a new one"
	}
	return fmt.Sprintf("%s refused the key stored as %s. Check the key, and store it again if it changed", c.kind.Title, c.p.Key)
}

// stateWords says why a provider is out of the rotation.
func stateWords(pl place, p Provider, st State, now time.Time) string {
	switch st.name() {
	case StateCooling:
		return "rate limited until " + when(st.Until, now)
	case StateOut:
		if !st.Until.IsZero() {
			return "out of quota until " + when(st.Until, now)
		}
		return "out of quota"
	case StateFailing:
		return fmt.Sprintf("unavailable: it failed %d times in a row", st.Failures)
	case StateRefused:
		if pl.group == GroupInstall {
			return "the key the admin stored for it was refused"
		}
		return fmt.Sprintf("the key stored as %s was refused. Store a new one", p.Key)
	}
	return string(st.name())
}

// when writes a time for a person: the hour within a day, else the date.
func when(t, now time.Time) string {
	t = t.UTC()
	switch {
	case t.Sub(now) < 24*time.Hour:
		return t.Format("15:04 UTC")
	case t.Year() != now.UTC().Year():
		return t.Format("January 2, 2006")
	}
	return t.Format("January 2")
}
