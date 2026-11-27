// Package routing decides which models one call may be sent to, and in
// what order. A rule names the model for each kind of task, a chain names
// the models to fall back to, and a call marked sensitive is kept to the
// models that may read sensitive content.
//
// Plan works the route out from the rules and the tenant's models. Chain
// is a seam.Provider over the route's opened models: it calls the first,
// and moves to the next when one fails before any of its reply has been
// passed on.
//
// This package is a guard for fixed rule 3: content from a sensitive space
// reaches only models the registry marks local or trusted. The answer for
// one model is models.Model.MayReadSensitive, and this package is where a
// route is held to it. It imports neither the settings registry nor the
// tenant settings, itself or through another package: the rules, the
// sensitive mark and the gate are arguments, so no setting can widen a
// route. The fixed rules check in verify fails if that changes.
//
// The package holds no state between calls and reads no store.
// docs/architecture/routing.md describes it.
package routing

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/models"
)

// Kind is the kind of task a call is made for. The rules name a model for
// each kind but chat.
type Kind string

const (
	// KindChat is a reply in a chat. It uses the model the person picked.
	KindChat Kind = "chat"
	// KindTitle is a chat's title.
	KindTitle Kind = "title"
	// KindSummary is a summary of a chat or of part of one.
	KindSummary Kind = "summary"
	// KindMemory is the memory pass after a turn.
	KindMemory Kind = "memory"
	// KindEmbedding turns text into a vector. Only a model with the
	// embedding capability takes it, and such a model takes nothing else.
	KindEmbedding Kind = "embedding"
	// KindDecision is a decision call (P14-02), such as the router's
	// verdict. Only a model with the decision capability takes it, and such
	// a model takes nothing else. A sensitive decision call is held to the
	// models that may read sensitive content, as every kind is.
	KindDecision Kind = "decision"
)

// Kinds returns every kind of task, in the order the plan lists them.
func Kinds() []Kind {
	return []Kind{KindChat, KindTitle, KindSummary, KindMemory, KindEmbedding, KindDecision}
}

// Rules are the routing rules in force for one tenant. A caller reads
// them from settings and passes them in.
type Rules struct {
	// Models names the model for a kind of task, by its registry name. A
	// kind with no name, or with a name the tenant cannot use, gets the
	// model the ask names. A name for KindChat is ignored: chat uses the
	// model picked.
	Models map[Kind]string
	// Fallback names the models to try, in order, after the first.
	Fallback []string
	// MaxFallbacks is the most models a route holds after its first. Zero
	// means a route is one model and nothing falls back.
	MaxFallbacks int
}

// Ask is one call to be routed.
type Ask struct {
	Kind Kind
	// Model is the registry name of the model the caller would use were
	// there no rule: for chat, the model the person picked.
	Model string
	// Sensitive says the call carries content from a sensitive space.
	Sensitive bool
	// Needs are capabilities the call cannot do without, such as vision
	// for a message with an image.
	Needs []models.Capability
}

// Why a model the rules named is not in a route.
const (
	// WithheldSensitive: the call is sensitive and the model may not read
	// sensitive content.
	WithheldSensitive = "sensitive"
	// WithheldUnknown: the tenant has no usable model of that name.
	WithheldUnknown = "unknown"
	// WithheldCapability: the model lacks something the call needs.
	WithheldCapability = "capability"
	// WithheldLimit: the route already holds as many models as it may.
	WithheldLimit = "limit"
)

// Withheld is one model the rules named that the route leaves out.
type Withheld struct {
	Name string
	// Why is one of the Withheld constants.
	Why string
}

// Route is the models one call may be sent to, in the order to try them.
type Route struct {
	Kind      Kind
	Sensitive bool
	// Models is never empty in a route Plan returned without an error.
	Models []models.Model
	// Withheld says which named models were left out, and why.
	Withheld []Withheld
}

var (
	// ErrNoModel is wrapped when no model is left to take the call.
	ErrNoModel = errors.New("no model can take the call")
	// ErrSensitive is wrapped, with ErrNoModel, when a model was there
	// and every one was withheld because the call is sensitive.
	ErrSensitive = errors.New("the call is sensitive, and only a local or trusted model may read it")
)

// Plan works out the route for one call from the rules and the models the
// tenant can use. The order is the kind's rule model, then the model the
// ask names, then the fallback chain, with each model once.
//
// A sensitive ask keeps only models for which MayReadSensitive is true.
// Nothing in the rules can put another model back.
func Plan(rules Rules, ask Ask, usable []models.Model) (Route, error) {
	if !slices.Contains(Kinds(), ask.Kind) {
		return Route{}, fmt.Errorf("routing: %q is not a kind of task", ask.Kind)
	}
	byName := map[string]models.Model{}
	for _, m := range usable {
		byName[m.Name] = m
	}
	var names []string
	if ask.Kind != KindChat {
		names = append(names, rules.Models[ask.Kind])
	}
	names = append(names, ask.Model)
	names = append(names, rules.Fallback...)

	route := Route{Kind: ask.Kind, Sensitive: ask.Sensitive}
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		m, ok := byName[name]
		why := ""
		switch {
		case !ok:
			why = WithheldUnknown
		case ask.Sensitive && !m.MayReadSensitive():
			why = WithheldSensitive
		case !fits(m, ask):
			why = WithheldCapability
		case len(route.Models) > max(rules.MaxFallbacks, 0):
			why = WithheldLimit
		}
		if why != "" {
			route.Withheld = append(route.Withheld, Withheld{Name: name, Why: why})
			continue
		}
		route.Models = append(route.Models, m)
	}
	if len(route.Models) == 0 {
		for _, w := range route.Withheld {
			if w.Why == WithheldSensitive {
				return route, fmt.Errorf("%w: %w", ErrNoModel, ErrSensitive)
			}
		}
		return route, fmt.Errorf("%w: no model named for a %s call is in the registry and able to take it", ErrNoModel, ask.Kind)
	}
	return route, nil
}

// fits reports whether a model can do what the call needs. An embedding
// model takes embedding calls and nothing else, and so does a decision
// model take decision calls.
func fits(m models.Model, ask Ask) bool {
	if m.Has(models.CapEmbedding) != (ask.Kind == KindEmbedding) {
		return false
	}
	if m.Has(models.CapDecision) != (ask.Kind == KindDecision) {
		return false
	}
	for _, c := range ask.Needs {
		if !m.Has(c) {
			return false
		}
	}
	return true
}

// Names splits a list of registry names as a setting holds it: separated
// by commas, with spaces around a name ignored and an empty name dropped.
func Names(list string) []string {
	var out []string
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}
