package connectors

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Options are the values connectors run with, from settings/agents.json.
type Options struct {
	Max            int           // agents.connectors_max
	ConnectTimeout time.Duration // agents.connector_connect_timeout
	IdleTimeout    time.Duration // agents.connector_idle_timeout
	MaxTools       int           // agents.connector_max_tools
	MaxBytes       int64         // agents.connector_max_bytes
	LineChars      int           // agents.connector_line_chars
	LoadMax        int           // agents.load_tools_max
	RetryAfter     time.Duration // agents.connector_retry_after
}

// Keys are the settings ReadOptions reads.
var Keys = []string{
	"agents.connectors_max", "agents.connector_connect_timeout", "agents.connector_idle_timeout", "agents.connector_max_tools",
	"agents.connector_max_bytes", "agents.connector_line_chars", "agents.load_tools_max", "agents.connector_retry_after",
}

// ReadOptions reads the options from wherever setting values are kept.
func ReadOptions(get func(key string) (string, bool)) (Options, error) {
	v := map[string]string{}
	for _, key := range Keys {
		s, ok := get(key)
		if !ok {
			return Options{}, fmt.Errorf("the %s setting is missing", key)
		}
		v[key] = s
	}
	var o Options
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
	num("agents.connectors_max", &o.Max)
	dur("agents.connector_connect_timeout", &o.ConnectTimeout)
	dur("agents.connector_idle_timeout", &o.IdleTimeout)
	num("agents.connector_max_tools", &o.MaxTools)
	num("agents.connector_line_chars", &o.LineChars)
	num("agents.load_tools_max", &o.LoadMax)
	dur("agents.connector_retry_after", &o.RetryAfter)
	if err == nil {
		if o.MaxBytes, err = strconv.ParseInt(v["agents.connector_max_bytes"], 10, 64); err != nil {
			err = fmt.Errorf("agents.connector_max_bytes: %w", err)
		}
	}
	if err != nil {
		return Options{}, err
	}
	return o, o.check()
}

func (o Options) check() error {
	switch {
	case o.Max < 1:
		return errors.New("agents.connectors_max must be 1 or more")
	case o.ConnectTimeout <= 0 || o.IdleTimeout <= 0 || o.RetryAfter < 0:
		return errors.New("agents.connector_connect_timeout and agents.connector_idle_timeout must be longer than nothing, and agents.connector_retry_after cannot be negative")
	case o.MaxTools < 1 || o.LoadMax < 1:
		return errors.New("agents.connector_max_tools and agents.load_tools_max must be 1 or more")
	case o.MaxBytes < 1024:
		return errors.New("agents.connector_max_bytes must be 1024 or more")
	case o.LineChars < 20:
		return errors.New("agents.connector_line_chars must be 20 or more")
	}
	return nil
}
