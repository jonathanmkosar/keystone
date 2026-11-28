package web

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The five states of a provider, and what moves it between them. The
// functions here are plain: each takes a state and the time and returns
// the next state, and the caller keeps it.

// StateName says whether a provider is in the rotation, and why not.
type StateName string

const (
	// StateReady: in the rotation.
	StateReady StateName = "ready"
	// StateCooling: a rate limit on a short window, until Until.
	StateCooling StateName = "cooling"
	// StateOut: the quota is spent, until a known reset or a soft test.
	StateOut StateName = "out"
	// StateFailing: web.fail_after failures of its own in a row, until a
	// soft test.
	StateFailing StateName = "failing"
	// StateRefused: the key was refused, until the key is changed. No soft
	// test: a refused key does not come back by itself.
	StateRefused StateName = "refused"
)

// State is one provider's state. The zero State is ready, with nothing
// counted.
type State struct {
	State StateName `json:"state,omitempty"`
	// Since is when it entered this state.
	Since time.Time `json:"since,omitempty"`
	// Until is when cooling ends, or the known reset of an out provider.
	// Zero for an out provider whose reset is not known.
	Until time.Time `json:"until,omitempty"`
	// Failures counts the provider's own failures in a row.
	Failures int `json:"failures,omitempty"`
	// TestDue is when the next soft test of an out or failing provider may
	// run, and TestWait the wait that set it, which doubles after each
	// failed test.
	TestDue  time.Time     `json:"test_due,omitempty"`
	TestWait time.Duration `json:"test_wait,omitempty"`

	// Left is what the provider last reported is left, when LeftKnown;
	// LeftUsed is Used at that moment, so what is left now is Left less
	// the searches since.
	Left      int  `json:"left,omitempty"`
	LeftKnown bool `json:"left_known,omitempty"`
	LeftUsed  int  `json:"left_used,omitempty"`

	// Used is Keystone's own count of successful searches since the
	// quota's last reset, which was at Window.
	Used   int       `json:"used,omitempty"`
	Window time.Time `json:"window,omitempty"`

	// KeyPrint is a fingerprint of the key that was refused, so a changed
	// key is seen, whichever way it was stored. It is not the key.
	KeyPrint string `json:"key_print,omitempty"`
	// Why is a short reason, for the screens.
	Why string `json:"why,omitempty"`
}

// name is the state's name, with the zero value ready.
func (s State) name() StateName {
	if s.State == "" {
		return StateReady
	}
	return s.State
}

// Rotation is the fallback's settings: the web. settings in
// settings/agents.json.
type Rotation struct {
	Strategy      string        // web.strategy
	MaxAttempts   int           // web.max_attempts
	QuotaReserve  int           // web.quota_reserve
	CoolingFor    time.Duration // web.cooling_for
	CoolingMax    time.Duration // web.cooling_max
	FailAfter     int           // web.fail_after
	FailCooldown  time.Duration // web.fail_cooldown
	TestFirst     time.Duration // web.test_first
	TestMax       time.Duration // web.test_max
	TestTick      time.Duration // web.test_tick
	UnknownWeight float64       // web.unknown_weight
}

// The strategies, web.strategy.
const (
	StrategyFreeFirst = "free_first"
	StrategyOrder     = "order"
	StrategyWeighted  = "weighted"
)

// RotationKeys are the settings ReadOptions reads into Rotation.
var RotationKeys = []string{
	"web.strategy", "web.max_attempts", "web.quota_reserve", "web.cooling_for", "web.cooling_max",
	"web.fail_after", "web.fail_cooldown", "web.test_first", "web.test_max", "web.test_tick", "web.unknown_weight",
}

// readRotation reads the web. settings.
func readRotation(get func(key string) (string, bool)) (Rotation, error) {
	v := map[string]string{}
	for _, key := range RotationKeys {
		s, ok := get(key)
		if !ok {
			return Rotation{}, fmt.Errorf("the %s setting is missing", key)
		}
		v[key] = strings.TrimSpace(s)
	}
	r := Rotation{Strategy: v["web.strategy"]}
	var err error
	num := func(key string, into *int) {
		if err == nil {
			if *into, err = strconv.Atoi(v[key]); err != nil {
				err = fmt.Errorf("%s: %w", key, err)
			}
		}
	}
	dur := func(key string, into *time.Duration) {
		if err == nil {
			if *into, err = time.ParseDuration(v[key]); err != nil {
				err = fmt.Errorf("%s: %w", key, err)
			}
		}
	}
	num("web.max_attempts", &r.MaxAttempts)
	num("web.quota_reserve", &r.QuotaReserve)
	num("web.fail_after", &r.FailAfter)
	dur("web.cooling_for", &r.CoolingFor)
	dur("web.cooling_max", &r.CoolingMax)
	dur("web.fail_cooldown", &r.FailCooldown)
	dur("web.test_first", &r.TestFirst)
	dur("web.test_max", &r.TestMax)
	dur("web.test_tick", &r.TestTick)
	if err == nil {
		if r.UnknownWeight, err = strconv.ParseFloat(v["web.unknown_weight"], 64); err != nil {
			err = fmt.Errorf("web.unknown_weight: %w", err)
		}
	}
	if err != nil {
		return Rotation{}, err
	}
	return r, r.check()
}

func (r Rotation) check() error {
	switch {
	case r.Strategy != StrategyFreeFirst && r.Strategy != StrategyOrder && r.Strategy != StrategyWeighted:
		return errors.New("web.strategy must be free_first, order or weighted")
	case r.MaxAttempts < 1:
		return errors.New("web.max_attempts must be 1 or more")
	case r.QuotaReserve < 0:
		return errors.New("web.quota_reserve cannot be negative")
	case r.FailAfter < 1:
		return errors.New("web.fail_after must be 1 or more")
	case r.CoolingFor <= 0 || r.CoolingMax <= 0 || r.FailCooldown <= 0 || r.TestFirst <= 0 || r.TestMax <= 0 || r.TestTick <= 0:
		return errors.New("web.cooling_for, web.cooling_max, web.fail_cooldown, web.test_first, web.test_max and web.test_tick must each be longer than nothing")
	case r.TestMax < r.TestFirst:
		return errors.New("web.test_max cannot be shorter than web.test_first")
	case r.UnknownWeight < 0 || r.UnknownWeight > 1:
		return errors.New("web.unknown_weight must be from 0 to 1")
	}
	return nil
}

// monthStart is the first moment of t's calendar month, in UTC.
func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// onDay is the day of the month d in the month of t, or that month's last
// day when it is shorter, at midnight UTC.
func onDay(t time.Time, d int) time.Time {
	first := monthStart(t)
	last := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(d, last)-1)
}

// periodStart is when the quota period that holds now began. A rolling
// quota's period begins at first use, so it has none before then.
func periodStart(p Provider, now time.Time) time.Time {
	switch d := resetDayOf(p); {
	case p.Resets == ResetsRolling:
		return time.Time{}
	case d > 0:
		start := onDay(now, d)
		if start.After(now) {
			start = onDay(monthStart(now).AddDate(0, -1, 0), d)
		}
		return start
	default:
		return monthStart(now)
	}
}

// periodEnd is when the period that began at start ends.
func periodEnd(p Provider, start time.Time) time.Time {
	switch d := resetDayOf(p); {
	case p.Resets == ResetsRolling:
		return start.Add(rollingWindow)
	case d > 0:
		return onDay(monthStart(start).AddDate(0, 1, 0), d)
	default:
		return monthStart(start).AddDate(0, 1, 0)
	}
}

// knownReset is when the provider's quota starts again, when the entry
// says: a day of the month, or 30 days from the first use. An entry that
// says nothing has no known reset; Keystone still counts its searches by
// the calendar month.
func knownReset(p Provider, s State) time.Time {
	if p.Resets == "" || s.Window.IsZero() {
		return time.Time{}
	}
	return periodEnd(p, s.Window)
}

// roll starts a new quota period when the last one has ended: Keystone's
// count starts again, and what the provider reported is forgotten.
func roll(p Provider, s State, now time.Time) State {
	if s.Window.IsZero() {
		s.Window = periodStart(p, now)
		return s
	}
	if !now.Before(periodEnd(p, s.Window)) {
		s.Window = periodStart(p, now)
		s.Used, s.LeftKnown, s.Left, s.LeftUsed = 0, false, 0, 0
	}
	return s
}

// leftOf is what is left of the provider's quota, and where that comes
// from: what the provider reported, less the searches since; else
// Keystone's own count against the monthly quota; else it is not known.
func leftOf(p Provider, s State) (left int, from string) {
	switch {
	case s.LeftKnown:
		return max(0, s.Left-(s.Used-s.LeftUsed)), "provider"
	case p.MonthlyQuota > 0:
		return max(0, p.MonthlyQuota-s.Used), "count"
	}
	return 0, ""
}

// freeLeft says the provider has free quota left: a free quota is set and
// what was used this period is under it.
func freeLeft(p Provider, s State) bool { return p.FreeQuota > 0 && s.Used < p.FreeQuota }

// become moves s into a state, at now.
func become(s State, name StateName, why string, now time.Time) State {
	if s.name() != name {
		s.Since = now
	}
	s.State, s.Why = name, why
	if name == StateReady {
		s.Until, s.TestDue, s.TestWait, s.Failures, s.KeyPrint, s.Why = time.Time{}, time.Time{}, 0, 0, "", ""
	}
	return s
}

// settle applies what time and a changed key do by themselves: a new
// quota period; cooling ends; an out provider whose reset is known comes
// back at it, for the next real search to confirm; a refused provider whose
// key was changed comes back.
func settle(p Provider, s State, now time.Time, keyPrint string) State {
	s = roll(p, s, now)
	switch s.name() {
	case StateCooling:
		if !now.Before(s.Until) {
			s = become(s, StateReady, "", now)
		}
	case StateOut:
		if !s.Until.IsZero() && !now.Before(s.Until) {
			s = become(s, StateReady, "", now)
		}
	case StateRefused:
		if keyPrint != s.KeyPrint {
			s = become(s, StateReady, "", now)
		}
	}
	return s
}

// out moves s out of the rotation for its quota, until reset when that is
// known, with its first soft test web.test_first away.
func (r Rotation) out(s State, why string, reset, now time.Time) State {
	s = become(s, StateOut, why, now)
	s.Until = reset
	s.TestWait = r.TestFirst
	s.TestDue = now.Add(r.TestFirst)
	return s
}

// spent says whether the quota is spent after a search or a check: what
// is left is at most web.quota_reserve, or Keystone's own count has
// reached the monthly quota.
func (r Rotation) spent(p Provider, s State) bool {
	left, from := leftOf(p, s)
	return (from != "" && left <= r.QuotaReserve) || (p.MonthlyQuota > 0 && s.Used >= p.MonthlyQuota)
}

// reset is when an out provider comes back by itself: the reset the
// provider gave, else the entry's known reset, else, when Keystone's own
// count spent it (the provider reported nothing), the end of the calendar
// month that count runs over. A provider that said it is out, with no
// reset known, waits for a soft test.
func reset(p Provider, s State, given time.Time) time.Time {
	switch {
	case !given.IsZero():
		return given
	case !knownReset(p, s).IsZero():
		return knownReset(p, s)
	case !s.LeftKnown && p.MonthlyQuota > 0 && !s.Window.IsZero():
		return periodEnd(p, s.Window)
	}
	return time.Time{}
}

// succeeded is a search the provider answered.
func (r Rotation) succeeded(p Provider, s State, q Quota, now time.Time) State {
	s = roll(p, s, now)
	if s.Window.IsZero() {
		s.Window = now // a rolling quota's first use
	}
	s.Used++
	switch {
	case q.Known:
		s.Left, s.LeftKnown, s.LeftUsed = q.Left, true, s.Used
	case s.name() != StateReady:
		// An out or failing provider answered, as a soft test: what it said
		// was left when it went out is past, and Keystone's count stands.
		s.Left, s.LeftKnown, s.LeftUsed = 0, false, 0
	}
	s = become(s, StateReady, "", now)
	if r.spent(p, s) {
		s = r.out(s, "out of quota", reset(p, s, q.Reset), now)
	}
	return s
}

// failed is a search the provider did not answer, for the reason f gives.
// keyPrint is the fingerprint of the key that was sent.
func (r Rotation) failed(p Provider, s State, f *Failure, keyPrint string, now time.Time) State {
	s = roll(p, s, now)
	switch f.Why {
	case WhyRate:
		wait := f.Wait
		if wait <= 0 {
			wait = r.CoolingFor
		}
		if wait >= r.CoolingMax {
			// A wait this long is a quota by another name.
			s.Failures = 0
			return r.out(s, "rate limited for longer than web.cooling_max", now.Add(wait), now)
		}
		s = become(s, StateCooling, "rate limited", now)
		s.Until = now.Add(wait)
	case WhyQuota:
		s.Left, s.LeftKnown, s.LeftUsed = 0, true, s.Used
		s.Failures = 0
		s = r.out(s, "out of quota", reset(p, s, f.Reset), now)
	case WhyUnavailable:
		s.Failures++
		if s.Failures >= r.FailAfter && s.name() != StateFailing {
			failures := s.Failures
			s = become(s, StateFailing, "unavailable", now)
			s.Failures = failures
			s.TestWait = r.FailCooldown
			s.TestDue = now.Add(r.FailCooldown)
		}
	case WhyRefused:
		s = become(s, StateRefused, "the key was refused", now)
		s.KeyPrint = keyPrint
	}
	return s
}

// testFailed is a soft test that did not bring the provider back: it stays
// as it is, and the wait before the next test doubles, up to web.test_max.
func (r Rotation) testFailed(s State, now time.Time) State {
	if s.TestWait <= 0 {
		s.TestWait = r.TestFirst
	} else {
		s.TestWait = min(2*s.TestWait, r.TestMax)
	}
	s.TestDue = now.Add(s.TestWait)
	return s
}

// testDue says an out or failing provider's next soft test may run.
func testDue(s State, now time.Time) bool {
	n := s.name()
	return (n == StateOut || n == StateFailing) && !now.Before(s.TestDue)
}
