package router

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The settings the router works from, in settings/models.json. The router
// reads none of them: the turn reads each for the person and passes the
// values to Load.
const (
	// KeyLadder is the admin's ladder for a person (tenant-admin).
	KeyLadder = "router.ladder"
	// KeyDecisionModel names the decision model (tenant-admin).
	KeyDecisionModel = "router.decision_model"
	// KeyStepNotes describes steps for the verdict (tenant-admin).
	KeyStepNotes = "router.step_notes"
	// KeyMinConfidence is the probability under which a verdict moves up.
	KeyMinConfidence = "router.min_confidence"
	// KeyVerdictTimeout is the longest the router waits for a verdict.
	KeyVerdictTimeout = "router.verdict_timeout"
	// KeyVerdictMaxChars is how much of the newest message a verdict sees.
	KeyVerdictMaxChars = "router.verdict_max_chars"
	// KeyDefaultEffort is the effort when the rules alone decide.
	KeyDefaultEffort = "router.default_effort"
	// KeyMaxEffort is the most effort the router asks for.
	KeyMaxEffort = "router.max_effort"
	// KeyLocalDownFor is how long a local endpoint that failed is passed over.
	KeyLocalDownFor = "router.local_down_for"
)

// Keys returns every setting Load reads, in the order the spec lists them.
func Keys() []string {
	return []string{
		KeyLadder, KeyDecisionModel, KeyStepNotes,
		KeyMinConfidence, KeyVerdictTimeout, KeyVerdictMaxChars,
		KeyDefaultEffort, KeyMaxEffort, KeyLocalDownFor,
	}
}

// Rung is one step of the ladder as the setting writes it: a registry name
// and, with `name<=tokens`, the largest call the step takes.
type Rung struct {
	Name string
	// Limit is the most estimated tokens a call may have for this step.
	// Zero means the step has no limit of its own.
	Limit int
}

// Settings are the router's values for one person, parsed.
type Settings struct {
	// Ladder is smallest first. Empty means Auto is not offered.
	Ladder []Rung
	// DecisionModel is the registry name of the decision model. Empty
	// means the rules alone decide.
	DecisionModel string
	// StepNotes describes a step to the decision model, by registry name.
	StepNotes map[string]string
	// MinConfidence is the probability, 0 to 1, under which the step a
	// verdict chose moves one candidate up.
	MinConfidence float64
	// VerdictTimeout is the longest the verdict may take. More than zero.
	VerdictTimeout time.Duration
	// VerdictMaxChars is how many characters of the newest message the
	// decision model is shown. One or more.
	VerdictMaxChars int
	// DefaultEffort is the effort when the rules alone decide.
	DefaultEffort seam.Effort
	// MaxEffort is the most effort the router ever asks for.
	MaxEffort seam.Effort
	// LocalDownFor is how long after a local endpoint failed with
	// unavailable local steps are passed over. Zero passes over none.
	LocalDownFor time.Duration
}

// ParseLadder reads the ladder setting: registry names separated by commas,
// smallest first, each with an optional size limit, as `name<=100000`.
// Spaces around a name or a limit are ignored and an empty entry is
// dropped. It refuses a limit that is not a whole number above zero, a
// limit with no name, and a name written twice.
func ParseLadder(list string) ([]Rung, error) {
	var ladder []Rung
	seen := map[string]bool{}
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		r := Rung{Name: part}
		if name, limit, ok := strings.Cut(part, "<="); ok {
			r.Name = strings.TrimSpace(name)
			n, err := strconv.Atoi(strings.TrimSpace(limit))
			if err != nil || n < 1 {
				return nil, fmt.Errorf("the ladder's step %q has a size limit that is not a whole number of tokens above zero", part)
			}
			r.Limit = n
		}
		if r.Name == "" {
			return nil, fmt.Errorf("the ladder's step %q has a size limit and no model name", part)
		}
		if seen[r.Name] {
			return nil, fmt.Errorf("the ladder names %s twice", r.Name)
		}
		seen[r.Name] = true
		ladder = append(ladder, r)
	}
	return ladder, nil
}

// ParseStepNotes reads the step notes setting: `name=words`, separated by
// semicolons. Spaces around a name or its words are ignored and an empty
// entry is dropped. It refuses an entry with no `=`, no name or no words,
// and a name written twice.
func ParseStepNotes(list string) (map[string]string, error) {
	notes := map[string]string{}
	for _, part := range strings.Split(list, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, words, ok := strings.Cut(part, "=")
		name, words = strings.TrimSpace(name), strings.TrimSpace(words)
		if !ok || name == "" || words == "" {
			return nil, fmt.Errorf("the step note %q is not written as name=words", short(part))
		}
		if _, dup := notes[name]; dup {
			return nil, fmt.Errorf("the step notes describe %s twice", name)
		}
		notes[name] = words
	}
	return notes, nil
}

// Load reads the router's settings through value, which returns a key's
// value as the person's settings hold it. Every key of Keys must be there.
// It refuses a value that does not read as its setting's type, or that
// Check refuses.
func Load(value func(key string) (string, bool)) (Settings, error) {
	var s Settings
	var problems []error
	get := func(key string) (string, bool) {
		v, ok := value(key)
		if !ok {
			problems = append(problems, errors.New(key+" has no value"))
		}
		return strings.TrimSpace(v), ok
	}
	if v, ok := get(KeyLadder); ok {
		ladder, err := ParseLadder(v)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", KeyLadder, err))
		}
		s.Ladder = ladder
	}
	s.DecisionModel, _ = get(KeyDecisionModel)
	if v, ok := get(KeyStepNotes); ok {
		notes, err := ParseStepNotes(v)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", KeyStepNotes, err))
		}
		s.StepNotes = notes
	}
	if v, ok := get(KeyMinConfidence); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			problems = append(problems, errors.New(KeyMinConfidence+" is not a number"))
		}
		s.MinConfidence = f
	}
	duration := func(key string, into *time.Duration) {
		if v, ok := get(key); ok {
			d, err := time.ParseDuration(v)
			if err != nil {
				problems = append(problems, errors.New(key+" is not a length of time"))
			}
			*into = d
		}
	}
	duration(KeyVerdictTimeout, &s.VerdictTimeout)
	duration(KeyLocalDownFor, &s.LocalDownFor)
	if v, ok := get(KeyVerdictMaxChars); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			problems = append(problems, errors.New(KeyVerdictMaxChars+" is not a whole number"))
		}
		s.VerdictMaxChars = n
	}
	if v, ok := get(KeyDefaultEffort); ok {
		s.DefaultEffort = seam.Effort(v)
	}
	if v, ok := get(KeyMaxEffort); ok {
		s.MaxEffort = seam.Effort(v)
	}
	if len(problems) > 0 {
		return Settings{}, fmt.Errorf("router: %w", errors.Join(problems...))
	}
	if err := s.Check(); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// Check refuses settings the router cannot work from.
func (s Settings) Check() error {
	var problems []string
	if !(s.MinConfidence >= 0 && s.MinConfidence <= 1) {
		problems = append(problems, KeyMinConfidence+" must be from 0 to 1")
	}
	if s.VerdictTimeout <= 0 {
		problems = append(problems, KeyVerdictTimeout+" must be a length of time above zero")
	}
	if s.VerdictMaxChars < 1 {
		problems = append(problems, KeyVerdictMaxChars+" must be one or more")
	}
	if s.DefaultEffort == "" || !s.DefaultEffort.Known() {
		problems = append(problems, KeyDefaultEffort+" must be one of low, medium, high, xhigh or max")
	}
	if s.MaxEffort == "" || !s.MaxEffort.Known() {
		problems = append(problems, KeyMaxEffort+" must be one of low, medium, high, xhigh or max")
	}
	if s.LocalDownFor < 0 {
		problems = append(problems, KeyLocalDownFor+" cannot be negative")
	}
	if len(problems) > 0 {
		return fmt.Errorf("router: %s", strings.Join(problems, "; "))
	}
	return nil
}

// capped is an effort no higher than the most the router asks for. An
// effort that is empty or unknown is the default, capped.
func (s Settings) capped(e seam.Effort) seam.Effort {
	if e == "" || !e.Known() {
		e = s.DefaultEffort
	}
	if rank(e) > rank(s.MaxEffort) {
		return s.MaxEffort
	}
	return e
}

// rank is an effort's place in seam.Efforts, lowest first, or -1.
func rank(e seam.Effort) int {
	for i, x := range seam.Efforts {
		if x == e {
			return i
		}
	}
	return -1
}

// short trims text for an error message or a reason.
func short(s string) string {
	const most = 40
	if r := []rune(s); len(r) > most {
		return string(r[:most]) + "..."
	}
	return s
}
