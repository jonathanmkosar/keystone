package browser

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools/browser/guard"
)

// KeyEnabled is the person's switch: browser use is offered only when it is
// on, and it ships off.
const KeyEnabled = "agents.browser_use"

// Options are the values the browser and its tools run with, read from the
// settings by ReadOptions and passed in by the caller.
type Options struct {
	Program      string        // agents.browser_path; empty finds one
	IdleTimeout  time.Duration // agents.browser_idle_timeout
	PageTimeout  time.Duration // agents.browser_page_timeout
	MaxPages     int           // agents.browser_max_pages
	MaxSteps     int           // agents.browser_max_steps
	TextMaxChars int           // agents.browser_text_max_chars
	MaxElements  int           // agents.browser_max_elements
	Blocked      []string      // agents.browser_blocked_hosts
	Width        int           // agents.browser_width
	Height       int           // agents.browser_height

	// The rest are not settings: a test or a check passes its own.

	// Driver starts the browsers. Nil means Chromium, the real one.
	Driver Driver
	// Enabled says whether browser use is on for a person. Nil reads
	// agents.browser_use from the person's own settings.
	Enabled func(ctx context.Context, s *tenant.Store) bool
	// HostAllowed decides whether the browser may connect to a host. Nil
	// means web_fetch's rule: only a host whose every address is on the
	// public internet. A test that serves pages on this machine passes its
	// own.
	HostAllowed func(ctx context.Context, host string) error
}

// Keys are the settings ReadOptions reads, in settings/agents.json. The
// switch is read for each person, on each call.
var Keys = []string{
	"agents.browser_path", "agents.browser_idle_timeout", "agents.browser_page_timeout", "agents.browser_max_pages",
	"agents.browser_max_steps", "agents.browser_text_max_chars", "agents.browser_max_elements", "agents.browser_blocked_hosts",
	"agents.browser_width", "agents.browser_height",
}

// ReadOptions reads the browser's settings. get returns a setting's value
// by its key.
func ReadOptions(get func(key string) (string, bool)) (Options, error) {
	v := map[string]string{}
	for _, key := range Keys {
		s, ok := get(key)
		if !ok {
			return Options{}, fmt.Errorf("the %s setting is missing", key)
		}
		v[key] = strings.TrimSpace(s)
	}
	o := Options{Program: v["agents.browser_path"], Blocked: guard.ParseList(v["agents.browser_blocked_hosts"])}
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
	dur("agents.browser_idle_timeout", &o.IdleTimeout)
	dur("agents.browser_page_timeout", &o.PageTimeout)
	num("agents.browser_max_pages", &o.MaxPages)
	num("agents.browser_max_steps", &o.MaxSteps)
	num("agents.browser_text_max_chars", &o.TextMaxChars)
	num("agents.browser_max_elements", &o.MaxElements)
	num("agents.browser_width", &o.Width)
	num("agents.browser_height", &o.Height)
	if err != nil {
		return Options{}, err
	}
	return o, o.check()
}

func (o Options) check() error {
	switch {
	case o.IdleTimeout <= 0:
		return errors.New("agents.browser_idle_timeout must be longer than nothing")
	case o.PageTimeout <= 0:
		return errors.New("agents.browser_page_timeout must be longer than nothing")
	case o.MaxPages < 1:
		return errors.New("agents.browser_max_pages must be 1 or more")
	case o.MaxSteps < 1:
		return errors.New("agents.browser_max_steps must be 1 or more")
	case o.TextMaxChars < 1:
		return errors.New("agents.browser_text_max_chars must be 1 or more")
	case o.MaxElements < 0:
		return errors.New("agents.browser_max_elements cannot be negative")
	case o.Width < 320 || o.Width > 3840:
		return errors.New("agents.browser_width must be from 320 to 3840")
	case o.Height < 240 || o.Height > 2160:
		return errors.New("agents.browser_height must be from 240 to 2160")
	}
	return nil
}
