// Package app is the one list of every route Keystone serves, and the
// place the API's settings are read.
//
// The binary serves app.Handler, the verify checks walk app.Routes, and the
// OpenAPI document is built from the same list, so all three see the same
// routes. A part that brings routes of its own adds them in Routes.
package app

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/settings"
)

// APIVersion is the version of the API, as the OpenAPI document states it.
// It matches the v1 in every route's path, and is not the binary's version.
const APIVersion = "1"

// Config is everything the API runs with.
type Config struct {
	// Install is the data folder. It is not a setting: the caller opens it.
	Install *tenant.Install
	// Log is where the server's own log goes. Nil means nowhere.
	Log io.Writer

	// The rest are settings, in settings/core.json.

	SignIn       signin.Options
	ListPeople   bool          // signin.list_people
	SecureCookie bool          // server.secure_cookie
	MaxBodyBytes int64         // server.max_body_bytes
	Heartbeat    time.Duration // server.stream_heartbeat
	PageLimit    int           // server.page_limit

	// Turns are the settings chats and turns run with, in
	// settings/chats.json.
	Turns turn.Config
	// Adapters are the model adapters this binary has. It is not a setting:
	// whoever starts the server makes the set. Nil means the fake alone.
	Adapters adapters.Set
	// runner works the turns. Handler and Open make it.
	runner *turn.Runner
	// Tools are the tools a turn may offer a model and run. It is not a
	// setting: whoever starts the server may make the registry. Nil means
	// the built-in tools, as Tools returns them.
	Tools *tools.Registry
	// toolsPlaced is turnOptions' own mark.
	toolsPlaced bool
}

// Defaults returns the configuration as the settings registry ships it,
// with no data folder and no log.
func Defaults() (Config, error) {
	reg, err := settings.Registry()
	if err != nil {
		return Config{}, err
	}
	return ReadConfig(func(key string) (string, bool) {
		s, ok := settings.Lookup(reg, key)
		return s.Default, ok
	})
}

// ReadConfig builds the configuration from wherever setting values are
// kept. get returns the value for a setting's key.
func ReadConfig(get func(key string) (string, bool)) (Config, error) {
	var c Config
	var err error
	if c.SignIn, err = signin.ReadOptions(get); err != nil {
		return Config{}, err
	}
	if c.Turns, err = turn.ReadConfig(get); err != nil {
		return Config{}, err
	}
	read := func(key string, parse func(string) error) {
		if err != nil {
			return
		}
		v, ok := get(key)
		if !ok {
			err = fmt.Errorf("the %s setting is missing", key)
			return
		}
		if perr := parse(v); perr != nil {
			err = fmt.Errorf("%s: %w", key, perr)
		}
	}
	read("signin.list_people", func(v string) (e error) { c.ListPeople, e = strconv.ParseBool(v); return })
	read("server.secure_cookie", func(v string) (e error) { c.SecureCookie, e = strconv.ParseBool(v); return })
	read("server.max_body_bytes", func(v string) (e error) { c.MaxBodyBytes, e = strconv.ParseInt(v, 10, 64); return })
	read("server.stream_heartbeat", func(v string) (e error) { c.Heartbeat, e = time.ParseDuration(v); return })
	read("server.page_limit", func(v string) (e error) { c.PageLimit, e = strconv.Atoi(v); return })
	if err != nil {
		return Config{}, err
	}
	switch {
	case c.MaxBodyBytes < 1:
		return Config{}, errors.New("server.max_body_bytes must be 1 or more")
	case c.Heartbeat <= 0:
		return Config{}, errors.New("server.stream_heartbeat must be longer than nothing")
	case c.PageLimit < 1:
		return Config{}, errors.New("server.page_limit must be 1 or more")
	}
	return c, nil
}

// Routes returns every route Keystone serves, in the order they are listed
// in the OpenAPI document. A part that brings routes adds them here:
//
//	all = append(all, chat.Routes(...)...)
func Routes(c Config) []api.Route {
	return routes(c, api.NewLog(c.Log))
}

func routes(c Config, log *api.Log) []api.Route {
	var all []api.Route
	all = append(all, api.ShellRoutes(api.Shell{
		Install:      c.Install,
		SignIn:       c.SignIn,
		ListPeople:   c.ListPeople,
		SecureCookie: c.SecureCookie,
		PageLimit:    c.PageLimit,
		Version:      APIVersion,
		Routes:       func() []api.Route { return all },
		Log:          log,
	})...)
	runner := c.runner
	if runner == nil {
		runner = turn.NewRunner(c.turnOptions(log))
	}
	all = append(all, turn.Routes(runner)...)
	return all
}

// Handler returns the handler for everything under /api/.
func Handler(c Config) (http.Handler, error) {
	log := api.NewLog(c.Log)
	if c.runner == nil {
		c.runner = turn.NewRunner(c.turnOptions(log))
	}
	// A turn that was waiting when the server last stopped runs now.
	c.runner.Recover()
	return api.Handler(api.Options{
		Install:      c.Install,
		Log:          log,
		MaxBodyBytes: c.MaxBodyBytes,
		Heartbeat:    c.Heartbeat,
	}, routes(c, log))
}

// OpenAPI returns the OpenAPI document for the routes Keystone serves. It
// needs no data folder, because the document describes the routes and
// calls none of them.
func OpenAPI() ([]byte, error) {
	c, err := Defaults()
	if err != nil {
		return nil, err
	}
	return api.Document(Routes(c), APIVersion)
}

// turnOptions is what the runner of chats and turns is made with.
func (c Config) turnOptions(log *api.Log) turn.Options {
	// The tool loop's lines (P7-01). They are added ahead of the line
	// below and change nothing in it: the options are made as before, and
	// then given the tools. An integrator may fold this into one literal.
	if !c.toolsPlaced {
		c.toolsPlaced = true
		o := c.turnOptions(log)
		if o.Tools = c.Tools; o.Tools == nil {
			o.Tools = c.toolsOrNone(log)
		}
		return o
	}
	return turn.Options{Install: c.Install, Log: log, Config: c.Turns, PageLimit: c.PageLimit, Adapters: c.Adapters}
}

// Open returns the handler as Handler does, and a function that stops the
// work the handler started and waits for it: the goroutines that run
// turns. A turn that is cut short this way is finished by the next start.
// A caller that removes the data folder afterwards, as a check does, calls
// it first.
func Open(c Config) (http.Handler, func(), error) {
	c.runner = turn.NewRunner(c.turnOptions(api.NewLog(c.Log)))
	h, err := Handler(c)
	if err != nil {
		c.runner.Close()
		return nil, nil, err
	}
	return h, c.runner.Close, nil
}
