package connectors

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/google"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// ErrAdopt is a Google sign-in that cannot be taken over as given.
var ErrAdopt = errors.New("the Google sign-in cannot be taken over")

// maxRefresh is the longest refresh token taken, well past any Google sends.
const maxRefresh = 16 << 10

// AdoptGoogle takes over a Google sign-in that was made somewhere else on
// this machine with the same client (P12-04): the refresh token goes into
// the person's vault, the account and what it granted are recorded as a
// sign-in made here records them, and the connector of each service the
// scopes cover is added or switched on. A service the scopes do not cover
// is left as it is. It calls nothing: the first use, or a connector check,
// is what proves the token to Google. It returns the services it covers.
func AdoptGoogle(ctx context.Context, s *tenant.Store, email string, scopes []string, refresh string, maxTools int) ([]google.Service, error) {
	// Windows PowerShell puts a byte-order mark before what it pipes, which
	// Google reads as part of the token and refuses.
	email, refresh = strings.TrimSpace(email), strings.TrimSpace(strings.TrimPrefix(refresh, "\xef\xbb\xbf"))
	switch {
	case refresh == "" || len(refresh) > maxRefresh || strings.ContainsAny(refresh, " \t\r\n"):
		return nil, fmt.Errorf("%w: the refresh token is empty, too long, or holds a space", ErrAdopt)
	case email == "" || len(email) > 320 || !strings.Contains(email, "@") || strings.ContainsAny(email, " \t\r\n"):
		return nil, fmt.Errorf("%w: the account's address does not read as one", ErrAdopt)
	}
	var covered []google.Service
	for _, svc := range google.Services() {
		if svc.Granted(scopes) {
			covered = append(covered, svc)
		}
	}
	if len(covered) == 0 {
		return nil, fmt.Errorf("%w: its scopes cover neither Gmail nor Calendar", ErrAdopt)
	}
	if err := s.Secrets().Set(GoogleSecret, refresh); err != nil {
		return nil, err
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO google_accounts(id, email, scopes, connected_at) VALUES(1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET email = excluded.email, scopes = excluded.scopes, connected_at = excluded.connected_at`,
		email, strings.Join(scopes, " "), stamp()); err != nil {
		return nil, err
	}
	for _, svc := range covered {
		if err := ensureGoogle(ctx, s, svc, maxTools); err != nil {
			return nil, err
		}
	}
	return covered, nil
}

// ensureGoogle adds a service's connector, or switches it on again, and
// keeps its tools. A connector of another kind under the name is left alone
// and ErrExists is returned.
func ensureGoogle(ctx context.Context, s *tenant.Store, svc google.Service, maxTools int) error {
	c := Connector{Name: string(svc), Label: svc.Label(), Transport: TransportGoogle, Secret: GoogleSecret, Enabled: true, Outside: true}
	k, err := Get(ctx, s, c.Name)
	switch {
	case errors.Is(err, ErrNotFound):
		// Built in, so it is not counted against agents.connectors_max.
		if k, err = Add(ctx, s, c, ByPerson, 0); err != nil {
			return err
		}
	case err != nil:
		return err
	case k.Transport != TransportGoogle:
		return ErrExists
	default:
		c.Label, c.Outside = k.Label, k.Outside // the person's own choices stay
		if k, err = Replace(ctx, s, c); err != nil {
			return err
		}
	}
	return saveGoogleTools(ctx, s, k.Connector, maxTools)
}
