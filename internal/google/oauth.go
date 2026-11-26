package google

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The OAuth 2.0 part: the authorization code flow with PKCE (S256), offline
// access for a refresh token, a refresh, and a revoke. The client id and
// secret are the install's, given by the caller; Keystone holds no client
// of its own.

// Client is an OAuth client: its id, a setting, and its secret, from the
// person's vault.
type Client struct {
	ID     string
	Secret string
}

// Grant is what Google gave for a code or a refresh.
type Grant struct {
	// Access is the access token, and Expires when it stops working.
	Access  string
	Expires time.Time
	// Refresh is the refresh token. Google sends one for a code when
	// access_type=offline, and usually none for a refresh.
	Refresh string
	// Scopes are the scopes granted, which may be fewer than were asked.
	Scopes []string
	// Email is the account's address, from the identity token, when Google
	// sent one.
	Email string
}

// NewSecret returns 32 random bytes as URL-safe text: a state or a PKCE
// verifier (43 characters, inside the 43 to 128 PKCE allows).
func NewSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// Challenge is the S256 challenge of a PKCE verifier.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthURL is the address the person's browser is sent to, to sign in and
// allow the scopes. It asks for offline access, so a refresh token comes
// back, shows the consent screen each time, so Google sends a refresh token
// again, and keeps scopes granted before.
func AuthURL(e Endpoints, clientID, redirect, state, challenge string, scopes []string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirect)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(scopes, " "))
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	q.Set("include_granted_scopes", "true")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	sep := "?"
	if strings.Contains(e.Auth, "?") {
		sep = "&"
	}
	return e.Auth + sep + q.Encode()
}

// Exchange trades the code Google sent back for tokens, with the PKCE
// verifier and the same redirect address.
func Exchange(ctx context.Context, hc *http.Client, e Endpoints, c Client, code, verifier, redirect string, max int64) (Grant, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", redirect)
	return tokenCall(ctx, hc, e, c, form, max)
}

// Refresh gets a new access token with a refresh token.
func Refresh(ctx context.Context, hc *http.Client, e Endpoints, c Client, refresh string, max int64) (Grant, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refresh)
	return tokenCall(ctx, hc, e, c, form, max)
}

// ErrSignInGone is a refresh token Google no longer takes: revoked, expired
// or from another client. The person connects again.
var ErrSignInGone = errors.New("Google no longer accepts your sign-in: connect Google again on Settings → Connectors")

// tokenCall posts a form to the token endpoint. The client's id and secret
// go in the form, as Google's endpoint takes them. An error names Google's
// short error code at most, never what was sent or what came back.
func tokenCall(ctx context.Context, hc *http.Client, e Endpoints, c Client, form url.Values, max int64) (Grant, error) {
	form.Set("client_id", c.ID)
	form.Set("client_secret", c.Secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Token, strings.NewReader(form.Encode()))
	if err != nil {
		return Grant{}, errors.New("the Google token address in agents.google_token_endpoint is not an address Keystone can call")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Grant{}, ctx.Err()
		}
		return Grant{}, errors.New("Keystone could not reach Google's sign-in: try again later")
	}
	defer resp.Body.Close()
	raw, err := readMost(resp.Body, max)
	if err != nil {
		return Grant{}, err
	}
	var body struct {
		Access    string `json:"access_token"`
		ExpiresIn int64  `json:"expires_in"`
		Refresh   string `json:"refresh_token"`
		Scope     string `json:"scope"`
		IDToken   string `json:"id_token"`
		Error     string `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	if resp.StatusCode != http.StatusOK {
		switch body.Error {
		case "invalid_grant":
			if form.Get("grant_type") == "refresh_token" {
				return Grant{}, ErrSignInGone
			}
			return Grant{}, errors.New("Google did not take the sign-in's code: it was used, or too much time passed. Connect again")
		case "invalid_client", "unauthorized_client":
			return Grant{}, errors.New("Google did not accept Keystone's OAuth client: check agents.google_client_id and the client secret stored as google.client_secret")
		}
		code := ""
		if shortCode(body.Error) {
			code = " (" + body.Error + ")"
		}
		return Grant{}, fmt.Errorf("Google's sign-in answered %d%s", resp.StatusCode, code)
	}
	if body.Access == "" {
		return Grant{}, errors.New("Google's sign-in gave no access token")
	}
	if body.ExpiresIn <= 0 {
		body.ExpiresIn = 3600
	}
	return Grant{
		Access: body.Access, Expires: time.Now().Add(time.Duration(body.ExpiresIn) * time.Second), Refresh: body.Refresh,
		Scopes: strings.Fields(body.Scope), Email: emailOf(body.IDToken),
	}, nil
}

// shortCode reports whether an OAuth error code is one word that can be
// named: letters and underscores, short.
func shortCode(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r == '_') {
			return false
		}
	}
	return true
}

// emailOf reads the address from an identity token's claims. The token came
// straight from Google's token endpoint over the connection Keystone made,
// so its signature is not checked (OpenID Connect Core 3.1.3.7); it names
// the account and grants nothing.
func emailOf(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(raw, &claims) != nil || !strings.Contains(claims.Email, "@") || len(claims.Email) > 254 || !plainLine(claims.Email) {
		return ""
	}
	return claims.Email
}

// Revoke asks Google to forget a token. A refresh token revoked this way
// takes its access tokens with it.
func Revoke(ctx context.Context, hc *http.Client, e Endpoints, token string) error {
	form := url.Values{}
	form.Set("token", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Revoke, strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("the Google revoke address in agents.google_revoke_endpoint is not an address Keystone can call")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return errors.New("Keystone could not reach Google to revoke the sign-in")
	}
	defer resp.Body.Close()
	_, _ = readMost(resp.Body, 64<<10)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Google answered %d to the revoke", resp.StatusCode)
	}
	return nil
}
