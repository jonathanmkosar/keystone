package adminaccount

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/signin"
)

// An admin's passkeys, read and verified by internal/signin's own code, as
// a person's are. Passkeys stay behind signin.passkeys, off as shipped.

const (
	challengeBytes = 32
	maxChallenges  = 16
	maxPasskeyName = 100
	purposeAdd     = "add"
	purposeSignIn  = "sign-in"
)

var (
	// ErrPasskeysOff is any passkey step while signin.passkeys is false.
	ErrPasskeysOff = signin.ErrPasskeysOff
	// ErrNoPasskey is a passkey id the account does not have.
	ErrNoPasskey = signin.ErrNoPasskey
	// ErrPasskeyName is a passkey name that is empty or too long.
	ErrPasskeyName = fmt.Errorf("a passkey's name is 1 to %d characters", maxPasskeyName)
)

func refused(why string) error { return fmt.Errorf("%w: %s", ErrRefused, why) }

// begin hands out a passkey prompt. A prompt to add a passkey is bound to
// the account, and lists the passkeys it has; a sign-in prompt names no
// account and lists none, so the browser offers the admin's own.
func (s *Store) begin(purpose, accountID string, o signin.Options) (signin.PasskeyPrompt, error) {
	if err := signin.CheckPasskeyOptions(o); err != nil {
		return signin.PasskeyPrompt{}, err
	}
	t := now().Truncate(time.Second)
	p := signin.PasskeyPrompt{
		Challenge: make([]byte, challengeBytes),
		RPID:      o.PasskeyRPID,
		Expires:   t.Add(o.PasskeyChallengeLifetime).Truncate(time.Second),
	}
	if _, err := rand.Read(p.Challenge); err != nil {
		return signin.PasskeyPrompt{}, err
	}
	if _, err := s.db.Exec("DELETE FROM challenges WHERE expires_at <= ?", stamp(t)); err != nil {
		return signin.PasskeyPrompt{}, err
	}
	if _, err := s.db.Exec("INSERT INTO challenges(challenge, purpose, account_id, expires_at) VALUES(?, ?, ?, ?)",
		p.Challenge, purpose, accountID, stamp(p.Expires)); err != nil {
		return signin.PasskeyPrompt{}, err
	}
	if _, err := s.db.Exec(`DELETE FROM challenges WHERE purpose = ? AND rowid NOT IN (
		SELECT rowid FROM challenges WHERE purpose = ? ORDER BY rowid DESC LIMIT ?)`, purpose, purpose, maxChallenges); err != nil {
		return signin.PasskeyPrompt{}, err
	}
	if accountID == "" {
		return p, nil
	}
	p.UserID = []byte(accountID)
	rows, err := s.db.Query("SELECT credential_id FROM passkeys WHERE account_id = ? ORDER BY created_at, id", accountID)
	if err != nil {
		return signin.PasskeyPrompt{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id []byte
		if err := rows.Scan(&id); err != nil {
			return signin.PasskeyPrompt{}, err
		}
		p.Credentials = append(p.Credentials, id)
	}
	return p, rows.Err()
}

// spend reads an answer's client data and uses up its prompt, which must be
// one handed out for purpose (and, to add, to this account) and not yet
// answered. A prompt is spent even when the answer is refused.
func (s *Store) spend(raw []byte, create bool, purpose, accountID string, o signin.Options) error {
	challenge, readErr := signin.ReadClientData(raw, create, o)
	if challenge == nil {
		return readErr
	}
	res, err := s.db.Exec("DELETE FROM challenges WHERE challenge = ? AND purpose = ? AND account_id = ? AND expires_at > ?",
		challenge, purpose, accountID, stamp(now()))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return refused("the challenge is unknown, used or out of date")
	}
	return readErr
}

// BeginAddPasskey starts adding a passkey to a signed-in account.
func (s *Store) BeginAddPasskey(accountID string, o signin.Options) (signin.PasskeyPrompt, error) {
	if _, err := s.Account(accountID); err != nil {
		return signin.PasskeyPrompt{}, err
	}
	return s.begin(purposeAdd, accountID, o)
}

// AddPasskey keeps the passkey a browser made in answer to BeginAddPasskey.
func (s *Store) AddPasskey(accountID, name string, c signin.PasskeyCreation, o signin.Options) (signin.Passkey, error) {
	if err := signin.CheckPasskeyOptions(o); err != nil {
		return signin.Passkey{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxPasskeyName {
		return signin.Passkey{}, ErrPasskeyName
	}
	if err := s.spend(c.ClientDataJSON, true, purposeAdd, accountID, o); err != nil {
		return signin.Passkey{}, err
	}
	count, credentialID, err := signin.ReadAuthData(c.AuthenticatorData, o)
	if err != nil {
		return signin.Passkey{}, err
	}
	if len(credentialID) == 0 {
		return signin.Passkey{}, refused("the authenticator data names no new credential")
	}
	if !bytes.Equal(credentialID, c.CredentialID) {
		return signin.Passkey{}, refused("the credential id and the authenticator data disagree")
	}
	if err := signin.CheckPasskeyKey(c.PublicKey); err != nil {
		return signin.Passkey{}, err
	}
	id, err := randomHex(8)
	if err != nil {
		return signin.Passkey{}, err
	}
	pk := signin.Passkey{ID: id, Name: name, Created: now().Truncate(time.Second)}
	res, err := s.db.Exec(`INSERT INTO passkeys(id, account_id, credential_id, public_key, sign_count, name, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?) ON CONFLICT(credential_id) DO NOTHING`,
		pk.ID, accountID, c.CredentialID, c.PublicKey, int64(count), name, stamp(pk.Created))
	if err != nil {
		return signin.Passkey{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return signin.Passkey{}, err
	} else if n == 0 {
		return signin.Passkey{}, refused("this passkey has already been added")
	}
	return pk, nil
}

// BeginPasskeySignIn starts an admin sign-in with a passkey.
func (s *Store) BeginPasskeySignIn(o signin.Options) (signin.PasskeyPrompt, error) {
	return s.begin(purposeSignIn, "", o)
}

// PasskeySignIn checks a browser's answer to BeginPasskeySignIn and opens a
// session, as SignIn does for a password. The passkey must be one an
// account added, and must say it is that account's.
func (s *Store) PasskeySignIn(a signin.PasskeyAssertion, o Options) (string, Session, Account, error) {
	if err := o.check(); err != nil {
		return "", Session{}, Account{}, err
	}
	if err := signin.CheckPasskeyOptions(o.SignIn); err != nil {
		return "", Session{}, Account{}, err
	}
	if err := s.spend(a.ClientDataJSON, false, purposeSignIn, "", o.SignIn); err != nil {
		return "", Session{}, Account{}, err
	}
	var (
		id, accountID string
		spki          []byte
		stored        int64
	)
	err := s.db.QueryRow("SELECT id, account_id, public_key, sign_count FROM passkeys WHERE credential_id = ?", a.CredentialID).
		Scan(&id, &accountID, &spki, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return "", Session{}, Account{}, refused("the passkey is not known here")
	}
	if err != nil {
		return "", Session{}, Account{}, err
	}
	if string(a.UserHandle) != accountID {
		return "", Session{}, Account{}, refused("the passkey does not say it belongs to its account")
	}
	count, _, err := signin.ReadAuthData(a.AuthenticatorData, o.SignIn)
	if err != nil {
		return "", Session{}, Account{}, err
	}
	if err := signin.VerifyPasskey(spki, a.AuthenticatorData, a.ClientDataJSON, a.Signature); err != nil {
		return "", Session{}, Account{}, err
	}
	if (count != 0 || stored != 0) && int64(count) <= stored {
		return "", Session{}, Account{}, refused("the passkey's use count went backwards, so it may have been copied")
	}
	t := now()
	if _, err := s.db.Exec("UPDATE passkeys SET sign_count = ?, last_used_at = ? WHERE id = ?", int64(count), stamp(t), id); err != nil {
		return "", Session{}, Account{}, err
	}
	acc, err := s.Account(accountID)
	if err != nil {
		return "", Session{}, Account{}, err
	}
	token, sess, err := s.newSession(accountID, signin.MethodPasskey, o, t)
	return token, sess, acc, err
}

// Passkeys lists an account's passkeys, oldest first.
func (s *Store) Passkeys(accountID string) ([]signin.Passkey, error) {
	rows, err := s.db.Query("SELECT id, name, created_at, last_used_at FROM passkeys WHERE account_id = ? ORDER BY created_at, id", accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []signin.Passkey{}
	for rows.Next() {
		var pk signin.Passkey
		var created, used string
		if err := rows.Scan(&pk.ID, &pk.Name, &created, &used); err != nil {
			return nil, err
		}
		if pk.Created, err = parseStamp(created); err != nil {
			return nil, err
		}
		if pk.LastUsed, err = parseStamp(used); err != nil {
			return nil, err
		}
		out = append(out, pk)
	}
	return out, rows.Err()
}

// RemovePasskey forgets one of an account's passkeys. Sessions it opened
// stay open.
func (s *Store) RemovePasskey(accountID, id string) error {
	res, err := s.db.Exec("DELETE FROM passkeys WHERE id = ? AND account_id = ?", id, accountID)
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
