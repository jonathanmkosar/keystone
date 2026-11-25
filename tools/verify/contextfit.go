package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/settings"
)

// checkContextFit is the standing check "context fit": no call leaves for a
// model whose window it cannot fit.
//
// It reads every entry in fixtures/models/examples.json, with the shipped
// budget settings, and proves for each one:
//
//   - with no provider count, a request estimated under the window is
//     accepted, and one over it is refused with the number of tokens over;
//   - a provider's own count is believed over the estimate: a count that
//     fills the window exactly is accepted, and one token more is refused
//     as one token over;
//   - an entry that names the fake adapter is called through the fake
//     provider itself, behind the guard: the request that fits is answered,
//     and the one that does not ends as a too_long error and never reaches
//     the fake, which would have answered it.
//
// Packing the largest test chat under each window joins this check with
// the context builder (P5-01).
func checkContextFit(root string) error {
	reg, err := settings.Registry()
	if err != nil {
		return err
	}
	o, err := budget.FromSettings(reg, nil)
	if err != nil {
		return err
	}
	entries, err := fixtureModels(root)
	if err != nil {
		return err
	}
	scripts, err := fake.Load(os.DirFS(filepath.Join(root, "fixtures", "fake-provider")))
	if err != nil {
		return fmt.Errorf("the fake provider's scripts do not load: %w", err)
	}
	faked, err := fake.New(scripts, fake.Options{})
	if err != nil {
		return err
	}

	ctx := context.Background()
	var problems []string
	fakes := 0
	for _, m := range entries {
		bad := func(format string, args ...any) {
			problems = append(problems, "model "+m.Name+": "+fmt.Sprintf(format, args...))
		}
		room := budget.InputRoom(m, 0)
		if room <= 0 {
			bad("its output limit leaves no room for input")
			continue
		}
		under := textRequest(m.Name, int(float64(room)*o.CharsPerToken/2))
		over := textRequest(m.Name, int(float64(m.ContextWindow)*o.CharsPerToken)+1)

		// The estimate.
		if fit, err := budget.Check(ctx, m, nil, under, o); err != nil {
			bad("a request estimated at %d tokens was refused: %v", fit.Input.Tokens, err)
		} else if fit.Input.Exact || fit.Input.Margin <= 0 {
			bad("an estimate came back without its margin: %+v", fit.Input)
		}
		if err := refusedBy(budget.Check(ctx, m, nil, over, o)); err != nil {
			bad("a request over the window: %v", err)
		}

		// The provider's own count.
		if fit, err := budget.Check(ctx, m, countsAs(room), under, o); err != nil || !fit.Input.Exact || fit.Room != 0 {
			bad("a provider's count that fills the window was not accepted as it is: %+v, %v", fit, err)
		}
		_, err := budget.Check(ctx, m, countsAs(room+1), under, o)
		var big *budget.TooLarge
		if !errors.As(err, &big) || big.Over != 1 || !strings.Contains(err.Error(), "1 too many") {
			bad("a provider's count one token over the window was not refused as one over: %v", err)
		}

		// The fake provider, behind the guard.
		if m.Adapter != models.AdapterFake {
			continue
		}
		fakes++
		guarded := budget.Guard(m, faked, o)
		if _, err := seam.Call(ctx, guarded, under); err != nil {
			bad("the fake did not answer a request that fits: %v", err)
		}
		_, err = seam.Call(ctx, guarded, over)
		var refusal *seam.Error
		if !errors.As(err, &refusal) || refusal.Kind != seam.ErrTooLong {
			bad("a request over the window reached the fake, or was refused another way: %v", err)
		}
	}
	if fakes == 0 {
		problems = append(problems, "no entry in fixtures/models/examples.json names the fake adapter")
	}
	// The packing: contextpack.go.
	packing, err := packingProblems(entries, o)
	if err != nil {
		return err
	}
	problems = append(problems, packing...)
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// refusedBy reports why a check's outcome is not a proper refusal: a
// *budget.TooLarge that says by how much, in numbers that add up.
func refusedBy(fit budget.Fit, err error) error {
	var big *budget.TooLarge
	switch {
	case err == nil:
		return fmt.Errorf("it was accepted, at %d tokens in a window of %d", fit.Input.Tokens+fit.Output, fit.Window)
	case !errors.As(err, &big) || !errors.Is(err, budget.ErrTooLarge):
		return fmt.Errorf("it was refused another way: %v", err)
	case big.Over <= 0 || big.Over != big.Input.Tokens+big.Output-big.Window:
		return fmt.Errorf("the refusal's numbers do not add up: %+v", *big)
	case !strings.Contains(err.Error(), fmt.Sprintf("%d too many", big.Over)):
		return fmt.Errorf("the refusal does not say by how much: %v", err)
	}
	return nil
}

// fixtureModels reads the entries the registry accepts from
// fixtures/models/examples.json.
func fixtureModels(root string) ([]models.Model, error) {
	raw, err := os.ReadFile(filepath.Join(root, "fixtures", "models", "examples.json"))
	if err != nil {
		return nil, err
	}
	var file struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("fixtures/models/examples.json: %w", err)
	}
	if len(file.Models) == 0 {
		return nil, errors.New("fixtures/models/examples.json holds no models")
	}
	var out []models.Model
	for _, r := range file.Models {
		m, err := models.Decode(r)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// textRequest is a request to one model holding one message of chars
// characters.
func textRequest(model string, chars int) seam.Request {
	return seam.Request{Model: model, Messages: []seam.Message{{
		Role:   seam.RoleUser,
		Blocks: []seam.Block{{Type: seam.BlockText, Text: strings.Repeat("a", max(chars, 1))}},
	}}}
}

// countsAs is a provider that counts every request as the same number of
// tokens, and is never called.
type countsAs int

func (countsAs) Stream(context.Context, seam.Request, func(seam.Event) error) error {
	return errors.New("the context fit check sends nothing")
}

func (n countsAs) CountTokens(context.Context, seam.Request) (int, error) { return int(n), nil }
