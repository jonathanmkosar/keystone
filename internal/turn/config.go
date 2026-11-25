package turn

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/settings"
)

// The settings a person may set for their own tenant. A turn reads them
// from the tenant's stored values, and the rest come in Config.
const (
	KeyDefaultModel    = "chats.default_model"
	KeyMaxOutputTokens = "chats.turn_max_output_tokens"
	KeyThinkingTokens  = "chats.turn_thinking_tokens"
	KeyTitleAuto       = "chats.title_auto"
	KeyTitleModel      = "chats.title_model"
)

// Config is the values of the install's settings that turns run with, all
// in settings/chats.json and settings/models.json.
type Config struct {
	// Limits are the chat record's own: title length and search.
	Limits chat.Limits
	// Budget is how a request's tokens are counted.
	Budget budget.Options

	QueueMax          int           // chats.turn_queue_max
	MaxSteps          int           // chats.turn_max_steps
	StreamPoll        time.Duration // chats.stream_poll
	StreamBatch       int           // chats.stream_batch
	StopWait          time.Duration // chats.stop_wait
	TitlePrompt       string        // chats.title_prompt
	TitleMaxTokens    int           // chats.title_max_tokens
	TitleInputChars   int           // chats.title_input_chars
	TitleTimeout      time.Duration // chats.title_timeout
	TitleDerivedWords int           // chats.title_derived_words
	TestContextWindow int           // chats.test_model_context_window
	TestOutputLimit   int           // chats.test_model_output_limit

	// ToolLoop is what the tool loop runs with, in settings/agents.json.
	ToolLoop ToolLoop
}

// ReadConfig builds the configuration from wherever setting values are
// kept. get returns the value for a setting's key.
func ReadConfig(get func(key string) (string, bool)) (Config, error) {
	var c Config
	var err error
	read := func(key string, into any) {
		if err != nil {
			return
		}
		v, ok := get(key)
		if !ok {
			err = fmt.Errorf("the %s setting is missing", key)
			return
		}
		switch p := into.(type) {
		case *int:
			*p, err = strconv.Atoi(v)
		case *time.Duration:
			*p, err = time.ParseDuration(v)
		case *string:
			*p = v
		}
		if err != nil {
			err = fmt.Errorf("%s: %w", key, err)
		}
	}
	read("chats.title_max_length", &c.Limits.TitleLength)
	read("chats.search_max_results", &c.Limits.SearchResults)
	read("chats.search_snippet_words", &c.Limits.SnippetWords)
	read("chats.turn_queue_max", &c.QueueMax)
	read("chats.turn_max_steps", &c.MaxSteps)
	read("chats.stream_poll", &c.StreamPoll)
	read("chats.stream_batch", &c.StreamBatch)
	read("chats.stop_wait", &c.StopWait)
	read("chats.title_prompt", &c.TitlePrompt)
	read("chats.title_max_tokens", &c.TitleMaxTokens)
	read("chats.title_input_chars", &c.TitleInputChars)
	read("chats.title_timeout", &c.TitleTimeout)
	read("chats.title_derived_words", &c.TitleDerivedWords)
	read("chats.test_model_context_window", &c.TestContextWindow)
	read("chats.test_model_output_limit", &c.TestOutputLimit)
	if err != nil {
		return Config{}, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return Config{}, err
	}
	if c.Budget, err = budget.FromSettings(reg, get); err != nil {
		return Config{}, err
	}
	if c.ToolLoop, err = readToolLoop(get); err != nil {
		return Config{}, err
	}
	switch {
	case c.Limits.TitleLength < 1:
		return Config{}, errors.New("chats.title_max_length must be 1 or more")
	case c.QueueMax < 0:
		return Config{}, errors.New("chats.turn_queue_max cannot be negative")
	case c.MaxSteps < 1:
		return Config{}, errors.New("chats.turn_max_steps must be 1 or more")
	case c.StreamPoll <= 0:
		return Config{}, errors.New("chats.stream_poll must be longer than nothing")
	case c.StreamBatch < 1:
		return Config{}, errors.New("chats.stream_batch must be 1 or more")
	case c.StopWait < 0 || c.TitleTimeout <= 0:
		return Config{}, errors.New("chats.stop_wait cannot be negative, and chats.title_timeout must be longer than nothing")
	case c.TitleMaxTokens < 1 || c.TitleInputChars < 1 || c.TitleDerivedWords < 1:
		return Config{}, errors.New("chats.title_max_tokens, chats.title_input_chars and chats.title_derived_words must each be 1 or more")
	case c.TestOutputLimit < 1 || c.TestContextWindow < c.TestOutputLimit:
		return Config{}, errors.New("chats.test_model_output_limit must be 1 or more, and no more than chats.test_model_context_window")
	}
	return c, nil
}

// DefaultConfig reads the configuration from the settings registry's
// defaults.
func DefaultConfig() (Config, error) {
	reg, err := settings.Registry()
	if err != nil {
		return Config{}, err
	}
	return ReadConfig(func(key string) (string, bool) {
		s, ok := settings.Lookup(reg, key)
		return s.Default, ok
	})
}

// Options is everything a Runner is made with.
type Options struct {
	// Install is the data folder. The runner opens a store of its own for
	// each chat it works, by the id of the store a call came with.
	Install *tenant.Install
	// Log is the server's own log. Nil means none.
	Log *api.Log
	// Config is the install's settings.
	Config Config
	// PageLimit is the most chats one list returns: server.page_limit.
	PageLimit int
	// Adapters are the builders this binary has. Nil means the fake alone,
	// playing the test tenant's recorded turns.
	Adapters adapters.Set
	// TestProvider answers the test tenant, and nothing else does. Nil
	// means the fake provider playing the shipped recorded turns at the
	// speed the settings give.
	TestProvider seam.Provider
	// Connectors give a turn the tools of the person's connectors (P7-04,
	// connectors.go). Nil means none.
	Connectors Connectors
	// Tools are the tools a turn may offer a model and run (P7-01).
	// Whoever puts the server together makes the registry and adds the
	// tools. Nil means none: nothing is offered, and a tool a model asks
	// for anyway is answered with an error.
	Tools *tools.Registry
}
