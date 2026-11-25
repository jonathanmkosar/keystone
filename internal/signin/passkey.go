package signin

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The server side of WebAuthn, for passkeys. The browser hands over the
// public key and the authenticator data as fields of their own, so nothing
// here reads CBOR (decision 0012). A passkey is always asked to verify the
// person (a fingerprint, a face or a PIN), because it stands in for the
// password and not beside it.

const (
	challengeBytes = 32
	// maxChallenges is how many unanswered prompts of one kind a tenant
	// keeps. One person needs one or two; the bound stops the table growing
	// when prompts are asked for and never answered.
	maxChallenges     = 16
	maxPasskeyName    = 100
	maxCredentialID   = 1023
	minRSABits        = 2048
	purposeAdd        = "add"
	purposeSignIn     = "sign-in"
	typeCreate        = "webauthn.create"
	typeGet           = "webauthn.get"
	flagUserPresent   = 0x01
	flagUserVerified  = 0x04
	flagAttestedData  = 0x40
	authDataFixedSize = 37 // the site's hash, the flags and the counter
)

var (
	// ErrPasskeysOff is any passkey step while signin.passkeys is false.
	ErrPasskeysOff = errors.New("passkeys are switched off")
	// ErrNoPasskey is a passkey id this tenant does not have.
	ErrNoPasskey = errors.New("there is no such passkey")
)

// PasskeyAlgorithms returns the signature kinds AddPasskey takes, as the
// numbers a browser is given to choose from, best first: ES256, Ed25519
// and RS256.
func PasskeyAlgorithms() []int { return []int{-7, -8, -257} }

// Passkey is one passkey a tenant has added. None of it is secret.
type Passkey struct {
	ID       string
	Name     string
	Created  time.Time
	LastUsed time.Time // zero until it is first used
}

// PasskeyPrompt is what a browser needs to make a passkey or to use one.
type PasskeyPrompt struct {
	// Challenge is random, used once, and comes back inside the answer.
	Challenge []byte
	RPID      string
	// UserID is the tenant's id. It is given as the user id when a passkey
	// is made, and comes back as the user handle when one is used.
	UserID []byte
	// Credentials are the ids of the passkeys the tenant already has: the
	// ones to leave out when adding, and the ones to allow when signing in.
	Credentials [][]byte
	Expires     time.Time
}

// PasskeyCreation is the browser's answer to a prompt to make a passkey.
type PasskeyCreation struct {
	CredentialID      []byte // the credential's rawId
	ClientDataJSON    []byte // response.clientDataJSON
	AuthenticatorData []byte // response.getAuthenticatorData()
	PublicKey         []byte // response.getPublicKey()
}

// PasskeyAssertion is the browser's answer to a prompt to use a passkey.
type PasskeyAssertion struct {
	CredentialID      []byte // the credential's rawId
	ClientDataJSON    []byte // response.clientDataJSON
	AuthenticatorData []byte // response.authenticatorData
	Signature         []byte // response.signature
	UserHandle        []byte // response.userHandle
}

func checkPasskeyOptions(o Options) error {
	switch {
	case !o.Passkeys:
		return ErrPasskeysOff
	case o.PasskeyRPID == "":
		return errors.New("signin.passkey_rp_id is empty")
	case o.PasskeyOrigin == "":
		return errors.New("signin.passkey_origin is empty")
	case o.PasskeyChallengeLifetime <= 0:
		return errors.New("signin.passkey_challenge_lifetime must be longer than nothing")
	}
	return nil
}

func refused(why string) error { return fmt.Errorf("%w: %s", ErrRefused, why) }

// BeginAddPasskey starts adding a passkey. The API calls it only for a
// signed-in session, and sends the prompt to that browser.
func BeginAddPasskey(s *tenant.Store, o Options) (PasskeyPrompt, error) {
	return begin(s, purposeAdd, o)
}

// BeginPasskeySignIn starts a sign-in with a passkey.
func BeginPasskeySignIn(s *tenant.Store, o Options) (PasskeyPrompt, error) {
	return begin(s, purposeSignIn, o)
}

func begin(s *tenant.Store, purpose string, o Options) (PasskeyPrompt, error) {
	if err := checkPasskeyOptions(o); err != nil {
		return PasskeyPrompt{}, err
	}
	t := now().Truncate(time.Second)
	p := PasskeyPrompt{
		Challenge: make([]byte, challengeBytes),
		RPID:      o.PasskeyRPID,
		UserID:    []byte(s.TenantID()),
		Expires:   t.Add(o.PasskeyChallengeLifetime).Truncate(time.Second),
	}
	if _, err := rand.Read(p.Challenge); err != nil {
		return PasskeyPrompt{}, err
	}
	db := s.DB()
	if _, err := db.Exec("DELETE FROM signin_challenges WHERE expires_at <= ?", stamp(t)); err != nil {
		return PasskeyPrompt{}, err
	}
	if _, err := db.Exec("INSERT INTO signin_challenges(challenge, purpose, expires_at) VALUES(?, ?, ?)",
		p.Challenge, purpose, stamp(p.Expires)); err != nil {
		return PasskeyPrompt{}, err
	}
	if _, err := db.Exec(`DELETE FROM signin_challenges WHERE purpose = ? AND rowid NOT IN (
		SELECT rowid FROM signin_challenges WHERE purpose = ? ORDER BY rowid DESC LIMIT ?)`,
		purpose, purpose, maxChallenges); err != nil {
		return PasskeyPrompt{}, err
	}
	rows, err := db.Query("SELECT credential_id FROM signin_passkeys ORDER BY created_at, id")
	if err != nil {
		return PasskeyPrompt{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id []byte
		if err := rows.Scan(&id); err != nil {
			return PasskeyPrompt{}, err
		}
		p.Credentials = append(p.Credentials, id)
	}
	return p, rows.Err()
}

// AddPasskey stores the passkey a browser made in answer to BeginAddPasskey.
// name is the person's label for it, such as the device it lives on.
func AddPasskey(s *tenant.Store, name string, c PasskeyCreation, o Options) (Passkey, error) {
	if err := checkPasskeyOptions(o); err != nil {
		return Passkey{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxPasskeyName {
		return Passkey{}, fmt.Errorf("a passkey's name is 1 to %d characters", maxPasskeyName)
	}
	if err := checkClientData(s, c.ClientDataJSON, typeCreate, purposeAdd, o); err != nil {
		return Passkey{}, err
	}
	ad, err := parseAuthData(c.AuthenticatorData, o.PasskeyRPID)
	if err != nil {
		return Passkey{}, err
	}
	if len(ad.credentialID) == 0 {
		return Passkey{}, refused("the authenticator data names no new credential")
	}
	if !bytes.Equal(ad.credentialID, c.CredentialID) {
		return Passkey{}, refused("the credential id and the authenticator data disagree")
	}
	if _, err := parseKey(c.PublicKey); err != nil {
		return Passkey{}, err
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Passkey{}, err
	}
	pk := Passkey{ID: hex.EncodeToString(raw[:]), Name: name, Created: now().Truncate(time.Second)}
	res, err := s.DB().Exec(`INSERT INTO signin_passkeys(id, credential_id, public_key, sign_count, name, created_at)
		VALUES(?, ?, ?, ?, ?, ?) ON CONFLICT(credential_id) DO NOTHING`,
		pk.ID, c.CredentialID, c.PublicKey, int64(ad.signCount), name, stamp(pk.Created))
	if err != nil {
		return Passkey{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Passkey{}, err
	} else if n == 0 {
		return Passkey{}, refused("this passkey has already been added")
	}
	return pk, nil
}

// PasskeySignIn checks a browser's answer to BeginPasskeySignIn and opens a
// session, as SignIn does for a password. The passkey must be one this
// tenant added: another tenant's is not in this database, and is refused.
func PasskeySignIn(s *tenant.Store, a PasskeyAssertion, o Options) (string, Session, error) {
	if err := checkOptions(o); err != nil {
		return "", Session{}, err
	}
	if err := checkPasskeyOptions(o); err != nil {
		return "", Session{}, err
	}
	if err := checkClientData(s, a.ClientDataJSON, typeGet, purposeSignIn, o); err != nil {
		return "", Session{}, err
	}
	// The device says whose passkey it is: the user id it was given when
	// the passkey was made, which is the tenant's id. The signature does
	// not cover it, so this is a second check and not the first. The first
	// is that the passkey's key is in this tenant's database.
	if string(a.UserHandle) != s.TenantID() {
		return "", Session{}, refused("the passkey does not say it belongs to this tenant")
	}
	var (
		id     string
		spki   []byte
		stored int64
	)
	db, t := s.DB(), now()
	err := db.QueryRow("SELECT id, public_key, sign_count FROM signin_passkeys WHERE credential_id = ?", a.CredentialID).
		Scan(&id, &spki, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return "", Session{}, refused("the passkey is not known here")
	}
	if err != nil {
		return "", Session{}, err
	}
	ad, err := parseAuthData(a.AuthenticatorData, o.PasskeyRPID)
	if err != nil {
		return "", Session{}, err
	}
	key, err := parseKey(spki)
	if err != nil {
		return "", Session{}, err
	}
	// What the device signed: its own data, then the hash of the client data.
	clientHash := sha256.Sum256(a.ClientDataJSON)
	signed := append(append([]byte{}, a.AuthenticatorData...), clientHash[:]...)
	if !verify(key, signed, a.Signature) {
		return "", Session{}, refused("the signature does not match the passkey")
	}
	// A device that counts its uses must count upward. A count that stands
	// still or goes back means a copy of the passkey is in use. Passkeys
	// that sync between devices do not count, and always say zero.
	count := int64(ad.signCount)
	if (count != 0 || stored != 0) && count <= stored {
		return "", Session{}, refused("the passkey's use count went backwards, so it may have been copied")
	}
	if _, err := db.Exec("UPDATE signin_passkeys SET sign_count = ?, last_used_at = ? WHERE id = ?", count, stamp(t), id); err != nil {
		return "", Session{}, err
	}
	return newSession(s, MethodPasskey, o, t)
}

// Passkeys lists the tenant's passkeys, oldest first.
func Passkeys(s *tenant.Store) ([]Passkey, error) {
	rows, err := s.DB().Query("SELECT id, name, created_at, last_used_at FROM signin_passkeys ORDER BY created_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Passkey
	for rows.Next() {
		var pk Passkey
		var created, used string
		if err := rows.Scan(&pk.ID, &pk.Name, &created, &used); err != nil {
			return nil, err
		}
		if pk.Created, err = time.Parse(stampLayout, created); err != nil {
			return nil, err
		}
		if used != "" {
			if pk.LastUsed, err = time.Parse(stampLayout, used); err != nil {
				return nil, err
			}
		}
		out = append(out, pk)
	}
	return out, rows.Err()
}

// RemovePasskey forgets one passkey, by the id Passkeys lists. Sessions it
// opened stay open; SignOutAll ends them.
func RemovePasskey(s *tenant.Store, id string) error {
	res, err := s.DB().Exec("DELETE FROM signin_passkeys WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNoPasskey
	}
	return nil
}

// checkClientData reads what the browser says it was asked: the kind of
// request, the challenge and the page it came from. The challenge must be
// one this tenant's store handed out for this purpose and has not seen come
// back. It is deleted here, so an answer can be used once.
func checkClientData(s *tenant.Store, raw []byte, typ, purpose string, o Options) error {
	var cd struct {
		Type        string `json:"type"`
		Challenge   string `json:"challenge"`
		Origin      string `json:"origin"`
		CrossOrigin bool   `json:"crossOrigin"`
	}
	if err := json.Unmarshal(raw, &cd); err != nil {
		return refused("the client data is not JSON")
	}
	challenge, err := base64.RawURLEncoding.DecodeString(cd.Challenge)
	if err != nil || len(challenge) == 0 {
		return refused("the challenge is not base64url")
	}
	res, err := s.DB().Exec("DELETE FROM signin_challenges WHERE challenge = ? AND purpose = ? AND expires_at > ?",
		challenge, purpose, stamp(now()))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return refused("the challenge is unknown, used or out of date")
	}
	if cd.Type != typ {
		return refused("the answer is for another kind of request")
	}
	if cd.Origin != o.PasskeyOrigin {
		return refused(fmt.Sprintf("the answer came from another page, %q", cd.Origin))
	}
	if cd.CrossOrigin {
		return refused("the answer came from a page inside another site's page")
	}
	return nil
}

type authData struct {
	signCount    uint32
	credentialID []byte // set only when a passkey is being made
}

// parseAuthData reads the authenticator data: 32 bytes of the site's hash,
// one byte of flags, a four-byte use count, and, when a passkey is being
// made, sixteen bytes naming the kind of device, a two-byte length and the
// credential id. The key that follows the id is CBOR and is not read; the
// browser gives the same key in a form the standard library reads.
func parseAuthData(raw []byte, rpID string) (authData, error) {
	if len(raw) < authDataFixedSize {
		return authData{}, refused("the authenticator data is too short")
	}
	site := sha256.Sum256([]byte(rpID))
	if !bytes.Equal(raw[:32], site[:]) {
		return authData{}, refused("the passkey was used for another site")
	}
	flags := raw[32]
	if flags&flagUserPresent == 0 || flags&flagUserVerified == 0 {
		return authData{}, refused("the device did not verify the person")
	}
	ad := authData{signCount: binary.BigEndian.Uint32(raw[33:37])}
	if flags&flagAttestedData != 0 {
		const idAt = authDataFixedSize + 16 + 2
		if len(raw) < idAt {
			return authData{}, refused("the authenticator data is too short")
		}
		n := int(binary.BigEndian.Uint16(raw[idAt-2 : idAt]))
		if n == 0 || n > maxCredentialID || len(raw) < idAt+n {
			return authData{}, refused("the credential id has an impossible length")
		}
		ad.credentialID = raw[idAt : idAt+n]
	}
	return ad, nil
}

// parseKey reads a public key in the form the browser's getPublicKey gives
// (SubjectPublicKeyInfo) and refuses any kind PasskeyAlgorithms does not list.
func parseKey(spki []byte) (any, error) {
	key, err := x509.ParsePKIXPublicKey(spki)
	if err != nil {
		return nil, refused("the public key cannot be read")
	}
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		if k.Curve != elliptic.P256() {
			return nil, refused("the public key is on a curve Keystone does not take")
		}
	case *rsa.PublicKey:
		if k.N.BitLen() < minRSABits {
			return nil, refused("the public key is too short")
		}
	case ed25519.PublicKey:
	default:
		return nil, refused("the public key is of a kind Keystone does not take")
	}
	return key, nil
}

func verify(key any, signed, sig []byte) bool {
	digest := sha256.Sum256(signed)
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		return ecdsa.VerifyASN1(k, digest[:], sig)
	case *rsa.PublicKey:
		return rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], sig) == nil
	case ed25519.PublicKey:
		return ed25519.Verify(k, signed, sig)
	}
	return false
}
