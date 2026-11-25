package main

import (
	"sort"

	"github.com/jonathanmkosar/keystone/internal/adapters/anthropic"
)

// keysInTheAnthropicAdapter is a surface of the "no keys in output" check.
// The adapter is given value as its key and called against stand-in
// endpoints that fail in each way a real one can, several of them
// repeating the key in what they answer. It returns every error and event
// the adapter gave back, and the provider as printed. The stand-ins answer
// inside this process, so no connection is opened.
func keysInTheAnthropicAdapter(_, _, value string) ([]keyOutput, error) {
	outputs, err := anthropic.KeyOutputs(value)
	names := make([]string, 0, len(outputs))
	for name := range outputs {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []keyOutput
	for _, name := range names {
		out = append(out, keyOutput{name, outputs[name]})
	}
	return out, err
}
