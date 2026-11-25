package chatcompletions

import (
	"encoding/json"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The cached part of the prompt, in each form an endpoint reports it
// (P5-03). The counts are invented.
func TestTheCachedPartOfThePromptIsRead(t *testing.T) {
	for name, c := range map[string]struct {
		usage string
		want  seam.Usage
	}{
		"prompt_tokens_details": {
			`{"prompt_tokens": 420, "completion_tokens": 96, "prompt_tokens_details": {"cached_tokens": 300}}`,
			seam.Usage{InputTokens: 120, OutputTokens: 96, CacheReadTokens: 300},
		},
		"prompt_cache_hit_tokens": {
			`{"prompt_tokens": 420, "completion_tokens": 96, "prompt_cache_hit_tokens": 256, "prompt_cache_miss_tokens": 164}`,
			seam.Usage{InputTokens: 164, OutputTokens: 96, CacheReadTokens: 256},
		},
		"both, and the details are believed": {
			`{"prompt_tokens": 420, "completion_tokens": 96, "prompt_tokens_details": {"cached_tokens": 300}, "prompt_cache_hit_tokens": 256}`,
			seam.Usage{InputTokens: 120, OutputTokens: 96, CacheReadTokens: 300},
		},
		"details that say nothing was cached, and the other form that says some was": {
			`{"prompt_tokens": 420, "completion_tokens": 96, "prompt_tokens_details": {"cached_tokens": 0}, "prompt_cache_hit_tokens": 256}`,
			seam.Usage{InputTokens: 164, OutputTokens: 96, CacheReadTokens: 256},
		},
		"no cache at all": {
			`{"prompt_tokens": 42, "completion_tokens": 9}`,
			seam.Usage{InputTokens: 42, OutputTokens: 9},
		},
		"more cached than the prompt, which is not believed past the prompt": {
			`{"prompt_tokens": 42, "completion_tokens": 9, "prompt_cache_hit_tokens": 500}`,
			seam.Usage{InputTokens: 0, OutputTokens: 9, CacheReadTokens: 42},
		},
	} {
		var u wireUsage
		if err := json.Unmarshal([]byte(c.usage), &u); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		r := &reply{usage: &u}
		if got := r.count(); got != c.want {
			t.Errorf("%s: %+v, want %+v", name, got, c.want)
		}
	}
}
