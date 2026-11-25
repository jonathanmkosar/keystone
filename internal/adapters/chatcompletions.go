package adapters

import (
	"errors"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters/chatcompletions"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/settings"
)

// ChatCompletions returns the set that holds the Chat Completions adapter
// alone, with its waits and retry limits read from the settings registry's
// defaults. Each entry gets a provider of its own, built from the entry and
// the key Open read from the vault. Whoever starts the server adds this set
// to the others it wants.
func ChatCompletions(reg []settings.Setting) (Set, error) {
	opts, err := ChatCompletionsOptions(reg)
	if err != nil {
		return nil, err
	}
	return Set{models.AdapterChatCompletions: func(m models.Model, key string) (seam.Provider, error) {
		p, err := chatcompletions.New(m, key, opts)
		if err != nil {
			return nil, err
		}
		return p, nil
	}}, nil
}

// ChatCompletionsOptions reads the Chat Completions adapter's settings. The
// adapter reads no setting itself.
func ChatCompletionsOptions(reg []settings.Setting) (chatcompletions.Options, error) {
	var opts chatcompletions.Options
	var problems []error
	wait := func(key string, into *time.Duration) {
		st, ok := settings.Lookup(reg, key)
		if !ok {
			problems = append(problems, errors.New(key+" is missing from the settings registry"))
			return
		}
		d, err := time.ParseDuration(st.Default)
		if err != nil || d <= 0 {
			problems = append(problems, errors.New(key+" must be a length of time more than zero"))
			return
		}
		*into = d
	}
	wait("chat_completions.connect_timeout", &opts.ConnectTimeout)
	wait("chat_completions.response_timeout", &opts.ResponseTimeout)
	wait("chat_completions.idle_timeout", &opts.IdleTimeout)
	wait("chat_completions.retry_wait", &opts.RetryBase)
	wait("chat_completions.retry_wait_max", &opts.RetryMax)

	const attempts = "chat_completions.max_attempts"
	if st, ok := settings.Lookup(reg, attempts); !ok {
		problems = append(problems, errors.New(attempts+" is missing from the settings registry"))
	} else if n, err := strconv.Atoi(st.Default); err != nil || n < 1 {
		problems = append(problems, errors.New(attempts+" must be a whole number, one or more"))
	} else {
		opts.MaxAttempts = n
	}
	return opts, errors.Join(problems...)
}
