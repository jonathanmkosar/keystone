package connectors

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/google"
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

	// Google is what the Gmail and Calendar connectors run with (P12-02).
	Google GoogleOptions
}

// GoogleOptions are the install's values for the Google connectors. The
// OAuth client id and the redirect address are each person's, read when
// they are needed (agents.google_client_id, agents.google_redirect_url);
// the client secret is in the person's vault (google.client_secret).
type GoogleOptions struct {
	Endpoints google.Endpoints // agents.google_*_endpoint
	Timeout   time.Duration    // agents.google_timeout
	Window    time.Duration    // agents.google_sign_in_window
	ListMax   int              // agents.google_list_max
	// OnThisMachine lets the Google addresses be on this machine, for the
	// stand-in Google of tests and verify. It is not a setting: the server
	// Keystone ships reaches Google at public addresses only.
	OnThisMachine bool
	// tokens keeps access tokens in memory, shared by every copy of the
	// options made from one ReadOptions. Nil means none is kept.
	tokens *tokenCache
}

// Keys are the settings ReadOptions reads.
var Keys = []string{
	"agents.connectors_max", "agents.connector_connect_timeout", "agents.connector_idle_timeout", "agents.connector_max_tools",
	"agents.connector_max_bytes", "agents.connector_line_chars", "agents.load_tools_max", "agents.connector_retry_after",
	"agents.google_auth_endpoint", "agents.google_token_endpoint", "agents.google_revoke_endpoint", "agents.google_gmail_endpoint",
	"agents.google_calendar_endpoint", "agents.google_timeout", "agents.google_sign_in_window", "agents.google_list_max",
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
	dur("agents.google_timeout", &o.Google.Timeout)
	dur("agents.google_sign_in_window", &o.Google.Window)
	num("agents.google_list_max", &o.Google.ListMax)
	if err == nil {
		if o.MaxBytes, err = strconv.ParseInt(v["agents.connector_max_bytes"], 10, 64); err != nil {
			err = fmt.Errorf("agents.connector_max_bytes: %w", err)
		}
	}
	if err != nil {
		return Options{}, err
	}
	o.Google.Endpoints = google.Endpoints{
		Auth: v["agents.google_auth_endpoint"], Token: v["agents.google_token_endpoint"], Revoke: v["agents.google_revoke_endpoint"],
		Gmail: v["agents.google_gmail_endpoint"], Calendar: v["agents.google_calendar_endpoint"],
	}
	o.Google.tokens = newTokenCache()
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
	case o.Google.Timeout <= 0 || o.Google.Window <= 0:
		return errors.New("agents.google_timeout and agents.google_sign_in_window must be longer than nothing")
	case o.Google.ListMax < 1 || o.Google.ListMax > 100:
		return errors.New("agents.google_list_max must be from 1 to 100")
	}
	for key, address := range map[string]string{
		"agents.google_auth_endpoint": o.Google.Endpoints.Auth, "agents.google_token_endpoint": o.Google.Endpoints.Token,
		"agents.google_revoke_endpoint": o.Google.Endpoints.Revoke, "agents.google_gmail_endpoint": o.Google.Endpoints.Gmail,
		"agents.google_calendar_endpoint": o.Google.Endpoints.Calendar,
	} {
		if u, err := url.Parse(address); err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return fmt.Errorf("%s must be an https address", key)
		}
	}
	return nil
}
