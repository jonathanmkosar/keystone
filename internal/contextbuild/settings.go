package contextbuild

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/jonathanmkosar/keystone/settings"
)

// The settings this package's Options come from, in settings/chats.json.
const (
	KeySystemText      = "chats.context_system_text"
	KeyFillPercent     = "chats.context_fill_percent"
	KeyRecentTurnsMax  = "chats.context_recent_turns_max"
	KeyAttachedPercent = "chats.context_attached_percent"
	KeyRecallPercent   = "chats.context_recall_percent"
	KeySummaryPercent  = "chats.context_summary_percent"
	KeyDroppedNote     = "chats.context_dropped_note"
	KeyRecountMax      = "chats.context_recount_max"
)

// Options are the tunables of the packing. They come from the settings
// registry through FromSettings.
type Options struct {
	// SystemText is the standing instructions every call starts with.
	// Empty sends none.
	SystemText string
	// FillPercent is the share of the model's input room the builder may
	// fill, from 1 to 100.
	FillPercent int
	// RecentTurnsMax is the most earlier turns one call carries. Zero
	// means as many as fit.
	RecentTurnsMax int
	// AttachedPercent is the most attachments may take, as a share of the
	// room left once the parts that are never left out are in.
	AttachedPercent int
	// RecallPercent is the most recalled knowledge may take, as a share of
	// the same room. That much is held for it ahead of earlier turns.
	RecallPercent int
	// SummaryPercent is the same for the summary of older history.
	SummaryPercent int
	// DroppedNote is a sentence sent to the model when earlier turns were
	// left out. Empty sends none.
	DroppedNote string
	// RecountMax is how many times a request is packed again when the
	// provider counts more tokens than the estimate.
	RecountMax int
}

// CheckOptions refuses options nothing can be packed with.
func CheckOptions(o Options) error {
	switch {
	case o.FillPercent < 1 || o.FillPercent > 100:
		return errors.New("contextbuild: the fill must be from 1 to 100 percent")
	case o.RecentTurnsMax < 0 || o.RecountMax < 0:
		return errors.New("contextbuild: a count cannot be negative")
	case o.AttachedPercent < 0 || o.AttachedPercent > 100:
		return errors.New("contextbuild: the share for attachments must be from 0 to 100 percent")
	case o.RecallPercent < 0 || o.SummaryPercent < 0 || o.RecallPercent+o.SummaryPercent > 100:
		return errors.New("contextbuild: the shares for recall and the summary cannot be negative, or more than 100 percent together")
	}
	return nil
}

// FromSettings reads the options from the defaults in a settings registry.
// value, when it is not nil, is asked first for each key, so a caller that
// holds a tenant's own values can pass them in. It returns the stored text
// and whether there is one.
func FromSettings(reg []settings.Setting, value func(key string) (string, bool)) (Options, error) {
	var firstErr error
	text := func(key string) string {
		st, ok := settings.Lookup(reg, key)
		if !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s is missing from the settings registry", key)
			}
			return ""
		}
		if value != nil {
			if v, ok := value(key); ok {
				if err := settings.CheckValue(st, v); err != nil && firstErr == nil {
					firstErr = err
				}
				return v
			}
		}
		return st.Default
	}
	whole := func(key string) int {
		v := text(key)
		n, err := strconv.Atoi(v)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %q is not a whole number", key, v)
		}
		return n
	}
	o := Options{
		SystemText:      text(KeySystemText),
		FillPercent:     whole(KeyFillPercent),
		RecentTurnsMax:  whole(KeyRecentTurnsMax),
		AttachedPercent: whole(KeyAttachedPercent),
		RecallPercent:   whole(KeyRecallPercent),
		SummaryPercent:  whole(KeySummaryPercent),
		DroppedNote:     text(KeyDroppedNote),
		RecountMax:      whole(KeyRecountMax),
	}
	if firstErr != nil {
		return Options{}, firstErr
	}
	return o, CheckOptions(o)
}
