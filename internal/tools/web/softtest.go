package web

import (
	"context"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Soft tests. A provider that is out or failing is tested, never more than
// one test at a time per provider:
//
//  1. At a known reset it goes back to ready by itself (settle), and the
//     next real search confirms it.
//  2. With a free check (a Checker, such as Tavily's usage check), the
//     check is run when the provider's next test is due, by the sweep
//     below, every web.test_tick. A check with quota left puts it back.
//  3. Without one, the next real search may go to it first, alone (find).
//     Success puts it back; failure leaves it out.
//
// The first test is web.test_first after it went out (web.fail_cooldown
// for a failing one), and each failed test doubles the wait, up to
// web.test_max. A refused key is never tested: it comes back when the key
// is changed.

// Goer runs a function in a goroutine that ends when the server stops, and
// is waited for: the turn runner's Go.
type Goer interface {
	Go(f func(ctx context.Context)) bool
}

// StartSweep starts the sweep under g: at once, and then every
// web.test_tick. Its first pass also moves each person's provider.tavily
// to their first own provider, so an install that is upgraded carries its
// Tavily keys across at start. It reports false, and starts nothing, when
// it cannot.
func StartSweep(g Goer, in *tenant.Install, o Options, logf func(format string, args ...any)) bool {
	if g == nil || in == nil || o.Rotation.TestTick <= 0 {
		return false
	}
	o.Install = in
	f := newFinder(o)
	if logf != nil {
		f.logf = logf
	}
	return g.Go(func(ctx context.Context) {
		for {
			f.sweep(ctx, in)
			select {
			case <-ctx.Done():
				return
			case <-time.After(o.Rotation.TestTick):
			}
		}
	})
}

// sweep runs one pass: for each person, the move and the free checks of
// their own providers that are due, and then the install's. It returns how
// many checks it ran.
func (f *finder) sweep(ctx context.Context, in *tenant.Install) int {
	n := 0
	for _, t := range in.Tenants() {
		if ctx.Err() != nil {
			return n
		}
		s, err := in.Store(t.ID)
		if err != nil {
			f.logf("web search: tenant %s: %v", t.ID, err)
			continue
		}
		now := f.now()
		if err := moveTavily(ctx, s, now); err != nil && ctx.Err() == nil {
			f.logf("web search: tenant %s: moving provider.tavily to an own provider: %v", t.ID, err)
		}
		n += f.checkDue(ctx, own(s))
		s.Close()
	}
	if f.install != nil {
		n += f.checkDue(ctx, f.install.place)
	}
	return n
}

// checkDue runs the free check of each provider in pl whose test is due,
// and returns how many it ran.
func (f *finder) checkDue(ctx context.Context, pl place) int {
	list, err := pl.list(ctx)
	if err != nil {
		f.logf("web search: reading search providers: %v", err)
		return 0
	}
	n := 0
	for _, p := range list {
		k, ok := kinds[p.Kind]
		if !p.Enabled || !ok {
			continue
		}
		key, keyOK, err := pl.keyOf(p)
		if err != nil || !keyOK {
			continue
		}
		checker, ok := f.adapter(p, k).(Checker)
		if !ok {
			continue
		}
		if f.check(ctx, pl, p, checker, key) {
			n++
		}
	}
	return n
}

// check runs one provider's free check when its test is due and no other
// test of it is running, and keeps what it found. It reports whether a
// check ran.
func (f *finder) check(ctx context.Context, pl place, p Provider, checker Checker, key string) bool {
	now := f.now()
	print := keyPrint(key)
	st, err := pl.change(ctx, p.Name, func(st State) State { return settle(p, st, now, print) })
	if err != nil || !testDue(st, now) || !claim(pl, p.Name) {
		return false
	}
	defer release(pl, p.Name)
	q, cerr := checker.Check(ctx, key)
	if cerr != nil && ctx.Err() != nil {
		return false
	}
	_, err = pl.change(context.WithoutCancel(ctx), p.Name, func(st State) State {
		st = settle(p, st, now, print)
		if !testDue(st, now) {
			return st // a search changed it meanwhile
		}
		if cerr != nil {
			if fl := failureOf(cerr); fl.Why == WhyRefused {
				return f.rot.failed(p, st, fl, print, now)
			}
			return f.rot.testFailed(st, now)
		}
		if q.Known {
			st.Left, st.LeftKnown, st.LeftUsed = q.Left, true, st.Used
			if q.Left <= f.rot.QuotaReserve {
				if !q.Reset.IsZero() {
					st.Until = q.Reset
				}
				return f.rot.testFailed(st, now)
			}
		}
		return become(st, StateReady, "", now)
	})
	if err != nil {
		f.logf("web search: keeping the state of %s: %v", p.Name, err)
	}
	return true
}
