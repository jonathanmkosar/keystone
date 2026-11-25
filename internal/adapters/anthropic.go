package adapters

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters/anthropic"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/settings"
)

// Anthropic returns the builder for entries that name the anthropic
// adapter. Whoever starts the server puts it in its set:
//
//	set[models.AdapterAnthropic] = builder
//
// Every provider the builder makes shares one HTTP client.
// docs/architecture/adapter-anthropic.md describes the adapter.
func Anthropic(opts anthropic.Options) (Builder, error) {
	build, err := anthropic.Builder(opts)
	if err != nil {
		return nil, err
	}
	return Builder(build), nil
}

// AddAnthropic puts the anthropic builder into a set, with its options read
// from the settings registry's defaults.
func AddAnthropic(set Set, reg []settings.Setting) error {
	opts, err := AnthropicOptions(reg)
	if err != nil {
		return err
	}
	build, err := Anthropic(opts)
	if err != nil {
		return err
	}
	set[models.AdapterAnthropic] = build
	return nil
}

// AnthropicOptions reads the anthropic adapter's options from the settings
// registry's defaults. The adapter reads no setting itself.
func AnthropicOptions(reg []settings.Setting) (anthropic.Options, error) {
	var opts anthropic.Options
	var problems []string
	text := func(key string) string {
		st, ok := settings.Lookup(reg, key)
		if !ok {
			problems = append(problems, key+" is missing from the settings registry")
		}
		return st.Default
	}
	wait := func(key string) time.Duration {
		d, err := time.ParseDuration(text(key))
		if err != nil || d < 0 {
			problems = append(problems, key+" must be a length of time, zero or more")
		}
		return d
	}
	opts.Version = text("anthropic.version")
	var err error
	if opts.MaxRetries, err = strconv.Atoi(text("anthropic.max_retries")); err != nil || opts.MaxRetries < 0 {
		problems = append(problems, "anthropic.max_retries must be a whole number, zero or more")
	}
	opts.RetryBaseDelay = wait("anthropic.retry_base_delay")
	opts.RetryMaxDelay = wait("anthropic.retry_max_delay")
	opts.HeaderTimeout = wait("anthropic.header_timeout")
	opts.IdleTimeout = wait("anthropic.idle_timeout")
	if opts.CacheControl, err = strconv.ParseBool(text("anthropic.cache_control")); err != nil {
		problems = append(problems, "anthropic.cache_control must be true or false")
	}
	for _, name := range strings.Split(text("anthropic.budget_thinking_models"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			opts.BudgetThinkingModels = append(opts.BudgetThinkingModels, name)
		}
	}
	if len(problems) > 0 {
		return anthropic.Options{}, errors.New(strings.Join(problems, "; "))
	}
	return opts, nil
}
