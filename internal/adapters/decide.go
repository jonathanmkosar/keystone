package adapters

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters/systemone"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// ErrNotDecider is wrapped when a model asked for a decision cannot give
// one: its entry does not have the decision capability, or its adapter
// gives none. The fake and the systemone adapter decide (P14-02).
var ErrNotDecider = errors.New("the model cannot answer decision calls")

// OpenDecider returns a tenant's decision model and the seam.Decider that
// calls it, as Open does for a model that writes replies. The errors are
// Open's, and ErrNotDecider. It does not choose the model: a caller that
// may hold sensitive content chooses with decision.Choose first, so the
// routing guard has the last word.
func OpenDecider(ctx context.Context, s *tenant.Store, set Set, name string) (models.Model, seam.Decider, error) {
	m, p, err := Open(ctx, s, set, name)
	if err != nil {
		return models.Model{}, nil, err
	}
	if !m.Has(models.CapDecision) {
		return models.Model{}, nil, fmt.Errorf("%w: model %s does not have the decision capability", ErrNotDecider, m.Name)
	}
	d, ok := p.(seam.Decider)
	if !ok {
		return models.Model{}, nil, fmt.Errorf("%w: model %s uses the %s adapter, which answers no decision calls", ErrNotDecider, m.Name, m.Adapter)
	}
	return m, d, nil
}

// Decides reports whether an adapter of this build answers decision calls,
// so a choice among decision models can pass over one that could not be
// opened as one. It builds nothing and reads no key.
func Decides(adapter string) bool {
	return adapter == models.AdapterFake || adapter == models.AdapterSystemone
}

// Systemone returns the set that holds the systemone adapter alone, with
// its limits read from the settings registry's defaults.
func Systemone(reg []settings.Setting) (Set, error) {
	opts, err := SystemoneOptions(reg)
	if err != nil {
		return nil, err
	}
	return Set{models.AdapterSystemone: func(m models.Model, key string) (seam.Provider, error) {
		p, err := systemone.New(m, key, opts)
		if err != nil {
			return nil, err
		}
		return p, nil
	}}, nil
}

// The settings the systemone adapter's options are read from.
const (
	SettingSystemoneTimeout         = "systemone.timeout"
	SettingSystemoneMaxRequestBytes = "systemone.max_request_bytes"
	SettingSystemoneMaxQuestions    = "systemone.max_questions"
)

// SystemoneOptions reads the systemone adapter's settings. The adapter
// reads no setting itself.
func SystemoneOptions(reg []settings.Setting) (systemone.Options, error) {
	var opts systemone.Options
	var problems []error
	lookup := func(key string) (string, bool) {
		st, ok := settings.Lookup(reg, key)
		if !ok {
			problems = append(problems, errors.New(key+" is missing from the settings registry"))
		}
		return st.Default, ok
	}
	if v, ok := lookup(SettingSystemoneTimeout); ok {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			problems = append(problems, errors.New(SettingSystemoneTimeout+" must be a length of time more than zero"))
		}
		opts.Timeout = d
	}
	whole := func(key string, into *int) {
		if v, ok := lookup(key); ok {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				problems = append(problems, errors.New(key+" must be a whole number, one or more"))
			}
			*into = n
		}
	}
	whole(SettingSystemoneMaxRequestBytes, &opts.MaxRequestBytes)
	whole(SettingSystemoneMaxQuestions, &opts.MaxQuestions)
	return opts, errors.Join(problems...)
}
