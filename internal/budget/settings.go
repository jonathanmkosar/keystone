package budget

import (
	"fmt"
	"strconv"

	"github.com/jonathanmkosar/keystone/settings"
)

// The settings this package's Options come from, in settings/models.json.
const (
	KeyCharsPerToken = "budget.chars_per_token"
	KeyMarginPercent = "budget.margin_percent"
	KeyMessageTokens = "budget.message_tokens"
	KeyToolTokens    = "budget.tool_tokens"
	KeyImageTokens   = "budget.image_tokens"
	KeyRefFileTokens = "budget.ref_file_tokens"
)

// FromSettings reads the options from the defaults in a settings registry.
// value, when it is not nil, is asked first for each key, so a caller that
// holds an install's or a tenant's own values can pass them in. It returns
// the stored text and whether there is one.
func FromSettings(reg []settings.Setting, value func(key string) (string, bool)) (Options, error) {
	text := func(key string) (string, error) {
		st, ok := settings.Lookup(reg, key)
		if !ok {
			return "", fmt.Errorf("%s is missing from the settings registry", key)
		}
		if value != nil {
			if v, ok := value(key); ok {
				if err := settings.CheckValue(st, v); err != nil {
					return "", err
				}
				return v, nil
			}
		}
		return st.Default, nil
	}
	var o Options
	var firstErr error
	whole := func(key string) int {
		v, err := text(key)
		if err == nil {
			var n int
			if n, err = strconv.Atoi(v); err == nil {
				return n
			}
			err = fmt.Errorf("%s: %q is not a whole number", key, v)
		}
		if firstErr == nil {
			firstErr = err
		}
		return 0
	}
	if v, err := text(KeyCharsPerToken); err != nil {
		firstErr = err
	} else if o.CharsPerToken, err = strconv.ParseFloat(v, 64); err != nil {
		firstErr = fmt.Errorf("%s: %q is not a number", KeyCharsPerToken, v)
	}
	o.MarginPercent = whole(KeyMarginPercent)
	o.MessageTokens = whole(KeyMessageTokens)
	o.ToolTokens = whole(KeyToolTokens)
	o.ImageTokens = whole(KeyImageTokens)
	o.RefFileTokens = whole(KeyRefFileTokens)
	if firstErr != nil {
		return Options{}, firstErr
	}
	return o, CheckOptions(o)
}
