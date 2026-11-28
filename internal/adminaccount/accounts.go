package adminaccount

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/signin"
)

const (
	maxName     = 100
	secretBytes = 32
)

var (
	// ErrNoAccount is an account id or name that is not in the database.
	ErrNoAccount = errors.New("there is no such admin account")
	// ErrNameTaken is a name another account already has.
	ErrNameTaken = errors.New("another admin account has that name")

	// The sign-in errors are a person's, so a caller answers both alike.
	ErrRefused     = signin.ErrRefused
	ErrWait        = signin.ErrWait
	ErrNoSession   = signin.ErrNoSession
	ErrBadPassword = signin.ErrBadPassword
)

var (
	idShape    = regexp.MustCompile(`^[a-f0-9]{16}$`)
	tokenShape = regexp.MustCompile(`^ksa_[a-f0-9]{64}$`)
)

// IsToken reports whether s has the form of an admin session token. It
// proves nothing: a token is a session only once Check finds it.
func IsToken(s string) bool { return tokenShape.MatchString(s) }

// IsID reports whether s has the form of an account id.
func IsID(s string) bool { return idShape.MatchString(s) }

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Account is one admin account. None of it is secret.
type Account struct {
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	Added time.Time `json:"added"`
	// LastSignIn is when the account last signed in, and is zero until it
	// first does.
	LastSignIn time.Time `json:"last_sign_in,omitzero"`
}

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxName {
		return "", fmt.Errorf("an admin account's name is 1 to %d characters", maxName)
	}
	return name, nil
}

// AddAccount makes an admin account with no password. The command line is
// its one caller (go run ./tools/admin add): no route makes an account.
func (s *Store) AddAccount(name string) (Account, error) {
	name, err := cleanName(name)
	if err != nil {
		return Account{}, err
	}
	id, err := randomHex(8)
	if err != nil {
		return Account{}, err
	}
	a := Account{ID: id, Name: name, Added: now().Truncate(time.Second)}
	res, err := s.db.Exec("INSERT INTO accounts(id, name, name_key, added_at) VALUES(?, ?, ?, ?) ON CONFLICT(name_key) DO NOTHING",
		a.ID, a.Name, strings.ToLower(a.Name), stamp(a.Added))
	if err != nil {
		return Account{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Account{}, err
	} else if n == 0 {
		return Account{}, ErrNameTaken
	}
	return a, nil
}

const accountColumns = "id, name, added_at, last_sign_in"

func scanAccount(row interface{ Scan(...any) error }) (Account, error) {
	var a Account
	var added, last string
	if err := row.Scan(&a.ID, &a.Name, &added, &last); err != nil {
		return Account{}, err
	}
	var err error
	if a.Added, err = parseStamp(added); err != nil {
		return Account{}, err
	}
	if a.LastSignIn, err = parseStamp(last); err != nil {
		return Account{}, err
	}
	return a, nil
}

// Accounts lists every admin account, oldest first.
func (s *Store) Accounts() ([]Account, error) {
	rows, err := s.db.Query("SELECT " + accountColumns + " FROM accounts ORDER BY added_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Account returns one account by its id.
func (s *Store) Account(id string) (Account, error) {
	if !IsID(id) {
		return Account{}, ErrNoAccount
	}
	a, err := scanAccount(s.db.QueryRow("SELECT "+accountColumns+" FROM accounts WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNoAccount
	}
	return a, err
}

// Find returns one account by its id, or by its name in any case.
func (s *Store) Find(idOrName string) (Account, error) {
	if a, err := s.Account(idOrName); err == nil || !errors.Is(err, ErrNoAccount) {
		return a, err
	}
	a, err := scanAccount(s.db.QueryRow("SELECT "+accountColumns+" FROM accounts WHERE name_key = ?", strings.ToLower(strings.TrimSpace(idOrName))))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNoAccount
	}
	return a, err
}

// RemoveAccount removes an account with its password, passkeys and
// sessions: every session it had ends at once. Its entries in the admin
// audit log stay. The command line is its one caller (go run ./tools/admin
// remove): no route removes an account.
func (s *Store) RemoveAccount(id string) error {
	if !IsID(id) {
		return ErrNoAccount
	}
	res, err := s.db.Exec("DELETE FROM accounts WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNoAccount
	}
	return nil
}

// SetPassword gives an account a password, or replaces the one it has,
// under a person's rules, and ends every session the account had.
func (s *Store) SetPassword(id, password string, o signin.Options) error {
	if err := signin.CheckPasswordRules(password, o); err != nil {
		return err
	}
	if _, err := s.Account(id); err != nil {
		return err
	}
	salt, hash, err := signin.HashPassword(password, o.PasswordIterations)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO passwords(account_id, algorithm, iterations, salt, hash, set_at, failures, last_failure)
		VALUES(?, ?, ?, ?, ?, ?, 0, '')
		ON CONFLICT(account_id) DO UPDATE SET algorithm = excluded.algorithm, iterations = excluded.iterations,
			salt = excluded.salt, hash = excluded.hash, set_at = excluded.set_at, failures = 0, last_failure = ''`,
		id, signin.PasswordAlgorithm, o.PasswordIterations, salt, hash, stamp(now())); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM sessions WHERE account_id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// HasPassword reports whether an account has a password.
func (s *Store) HasPassword(id string) (bool, error) {
	var one int
	err := s.db.QueryRow("SELECT 1 FROM passwords WHERE account_id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Session is one admin sign-in, on one browser.
type Session struct {
	// ID names the session in the logs. It is not the token.
	ID        string
	AccountID string
	// Method is signin.MethodPassword or signin.MethodPasskey.
	Method   string
	Created  time.Time
	Expires  time.Time
	LastUsed time.Time
}

// unknownSalt is hashed against when a sign-in names no account, so the
// answer takes as long as a wrong password does.
var unknownSalt = []byte("keystone admin sign-in, no such account")

// SignIn checks an account's password and opens a session. account is the
// account's id or its name. It returns the session token, which exists only
// in this return value. A wrong password, an account that is not there and
// an account with no password are all ErrRefused. After
// signin.failures_before_wait wrong passwords in a row, the next try is
// ErrWait until signin.failure_wait has passed since the last.
func (s *Store) SignIn(account, password string, o Options) (string, Session, Account, error) {
	if err := o.check(); err != nil {
		return "", Session{}, Account{}, err
	}
	a, err := s.Find(account)
	if errors.Is(err, ErrNoAccount) {
		signin.PasswordMatches(password, o.SignIn.PasswordIterations, unknownSalt, unknownSalt)
		return "", Session{}, Account{}, ErrRefused
	}
	if err != nil {
		return "", Session{}, Account{}, err
	}
	t, err := s.checkPassword(a.ID, password, o.SignIn)
	if err != nil {
		return "", Session{}, a, err
	}
	token, sess, err := s.newSession(a.ID, signin.MethodPassword, o, t)
	return token, sess, a, err
}

// checkPassword is a person's password check, kept in this database: the
// try is counted before the password is looked at, in one statement, so
// tries made at once cannot slip past the limit together.
func (s *Store) checkPassword(id, password string, o signin.Options) (time.Time, error) {
	t := now()
	res, err := s.db.Exec(`UPDATE passwords SET failures = failures + 1, last_failure = ?
		WHERE account_id = ? AND NOT (failures >= ? AND last_failure > ?)`,
		stamp(t), id, o.FailuresBeforeWait, stamp(t.Add(-o.FailureWait)))
	if err != nil {
		return time.Time{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return time.Time{}, err
	} else if n == 0 {
		has, err := s.HasPassword(id)
		if err != nil {
			return time.Time{}, err
		}
		if has {
			return time.Time{}, ErrWait
		}
		return time.Time{}, ErrRefused
	}
	var (
		alg        string
		iterations int
		salt, hash []byte
	)
	if err := s.db.QueryRow("SELECT algorithm, iterations, salt, hash FROM passwords WHERE account_id = ?", id).
		Scan(&alg, &iterations, &salt, &hash); err != nil {
		return time.Time{}, err
	}
	if alg != signin.PasswordAlgorithm {
		return time.Time{}, fmt.Errorf("admin account %s: the stored password is in a form this binary does not know (%s)", id, alg)
	}
	ok, err := signin.PasswordMatches(password, iterations, salt, hash)
	if err != nil {
		return time.Time{}, err
	}
	if !ok {
		return time.Time{}, ErrRefused
	}
	if iterations != o.PasswordIterations {
		if salt, hash, err = signin.HashPassword(password, o.PasswordIterations); err != nil {
			return time.Time{}, err
		}
	}
	if _, err := s.db.Exec("UPDATE passwords SET failures = 0, last_failure = '', iterations = ?, salt = ?, hash = ? WHERE account_id = ?",
		o.PasswordIterations, salt, hash, id); err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// newSession opens a session for an account and returns its token.
func (s *Store) newSession(accountID, method string, o Options, t time.Time) (string, Session, error) {
	secret, err := randomHex(secretBytes)
	if err != nil {
		return "", Session{}, err
	}
	sid, err := randomHex(8)
	if err != nil {
		return "", Session{}, err
	}
	token := TokenPrefix + secret
	t = t.Truncate(time.Second)
	sess := Session{ID: sid, AccountID: accountID, Method: method, Created: t, Expires: t.Add(o.SessionFor).Truncate(time.Second), LastUsed: t}
	if _, err := s.db.Exec("DELETE FROM sessions WHERE expires_at <= ? OR last_used_at <= ?", stamp(t), stamp(t.Add(-o.IdleFor))); err != nil {
		return "", Session{}, err
	}
	if _, err := s.db.Exec("INSERT INTO sessions(id, account_id, token_hash, method, created_at, expires_at, last_used_at) VALUES(?, ?, ?, ?, ?, ?, ?)",
		sess.ID, accountID, hashToken(token), method, stamp(sess.Created), stamp(sess.Expires), stamp(sess.LastUsed)); err != nil {
		return "", Session{}, err
	}
	if _, err := s.db.Exec("UPDATE accounts SET last_sign_in = ? WHERE id = ?", stamp(t), accountID); err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

// Check reports whether token is a live admin session, and counts this as a
// use of it. A token without the admin form is refused before the database
// is read. A session past admin.session_for, or unused for admin.idle_for,
// is deleted and refused. Every refusal is ErrNoSession.
func (s *Store) Check(token string, o Options) (Session, error) {
	if !IsToken(token) {
		return Session{}, ErrNoSession
	}
	if err := o.check(); err != nil {
		return Session{}, err
	}
	var sess Session
	var created, expires, used string
	err := s.db.QueryRow("SELECT id, account_id, method, created_at, expires_at, last_used_at FROM sessions WHERE token_hash = ?", hashToken(token)).
		Scan(&sess.ID, &sess.AccountID, &sess.Method, &created, &expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, err
	}
	for _, f := range []struct {
		in  string
		out *time.Time
	}{{created, &sess.Created}, {expires, &sess.Expires}, {used, &sess.LastUsed}} {
		if *f.out, err = parseStamp(f.in); err != nil {
			return Session{}, err
		}
	}
	t := now()
	if !t.Before(sess.Expires) || !t.Before(sess.LastUsed.Add(o.IdleFor)) {
		if _, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", sess.ID); err != nil {
			return Session{}, err
		}
		return Session{}, ErrNoSession
	}
	sess.LastUsed = t.Truncate(time.Second)
	if _, err := s.db.Exec("UPDATE sessions SET last_used_at = ? WHERE id = ?", stamp(sess.LastUsed), sess.ID); err != nil {
		return Session{}, err
	}
	return sess, nil
}

// SignOut ends the session the token belongs to. A token that is not a
// session is ErrNoSession.
func (s *Store) SignOut(token string) error {
	if !IsToken(token) {
		return ErrNoSession
	}
	res, err := s.db.Exec("DELETE FROM sessions WHERE token_hash = ?", hashToken(token))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNoSession
	}
	return nil
}

// SignOutAll ends every session of one account.
func (s *Store) SignOutAll(accountID string) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE account_id = ?", accountID)
	return err
}
