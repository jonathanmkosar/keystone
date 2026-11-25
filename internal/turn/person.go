package turn

import (
	"context"
	"strconv"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/personmap"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
)

// person returns the person's profile and the map of their knowledge store
// for one model call, with the person's own values of the settings. The map
// is the version the call's models may read: with mayReadSensitive false,
// which is so when any model of the call's route may not read sensitive
// content, every sensitive space is left out.
//
// Nothing here stops a chat. A knowledge store that is down, an adapter
// this build does not have, or a setting that does not read gives an empty
// text and a line in the log.
func (r *Runner) person(ctx context.Context, s *tenant.Store, t row, mayReadSensitive bool) (profile, personMap string) {
	get := func(key string) (string, bool) {
		v, err := tenantsettings.Get(ctx, s, r.reg, key)
		return v, err == nil
	}
	number := func(key string) (int, bool) {
		v, ok := get(key)
		if !ok {
			return 0, false
		}
		n, err := strconv.Atoi(v)
		return n, err == nil
	}
	var l personmap.Limits
	var ok [4]bool
	l.ProfileMaxChars, ok[0] = number(personmap.SettingProfileMaxChars)
	l.MapMaxTokens, ok[1] = number(personmap.SettingMapMaxTokens)
	l.TitlesPerKind, ok[2] = number(personmap.SettingMapTitlesPerKind)
	l.TitleMaxChars, ok[3] = number(personmap.SettingMapTitleMaxChars)
	l.CharsPerToken = r.o.Config.Budget.CharsPerToken
	for _, good := range ok {
		if !good {
			r.logf("turns: %s turn %s: the profile and map settings do not read, so neither is sent", t.requestID, t.ID)
			return "", ""
		}
	}

	var ks knowledge.Store
	if name, found := get(knowledge.SettingAdapter); found {
		opened, err := knowledge.Open(ctx, name, s)
		if err != nil {
			r.logf("turns: %s turn %s: the knowledge store did not open, so no map is sent: %v", t.requestID, t.ID, err)
		} else {
			ks = opened
			defer ks.Close()
		}
	}
	p, err := personmap.ForContext(ctx, s, ks, mayReadSensitive, l)
	if err != nil {
		r.logf("turns: %s turn %s: the profile and map could not be read, so neither is sent: %v", t.requestID, t.ID, err)
		return "", ""
	}
	return p.Profile, p.Map
}
