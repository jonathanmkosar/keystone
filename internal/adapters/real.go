package adapters

import (
	"maps"

	"github.com/jonathanmkosar/keystone/internal/adapters/responses"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/settings"
)

// Real returns the set of adapters that call a real endpoint: anthropic,
// responses and chat_completions, each with its options read from the
// settings registry's defaults. The server starts with this set. The fake
// is not in it: the test tenant is given the fake by the turn runner, and
// no other tenant's entry can open one on a running server.
func Real(reg []settings.Setting) (Set, error) {
	set := Set{}
	if err := AddAnthropic(set, reg); err != nil {
		return nil, err
	}
	opts, err := responses.OptionsFrom(reg)
	if err != nil {
		return nil, err
	}
	set[models.AdapterResponses] = Responses(opts)
	cc, err := ChatCompletions(reg)
	if err != nil {
		return nil, err
	}
	maps.Copy(set, cc)
	return set, nil
}
