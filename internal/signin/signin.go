// Package signin holds how a person proves who they are, and the session
// that proof buys.
//
// A tenant is one person, so there is no table of users. Each tenant has at
// most one password and any number of passkeys, and all of it lives in that
// tenant's own database: the password hash, the passkeys and the sessions.
// Every function here works on one tenant's Store. A session token names
// its tenant, is stored only as a hash, and is refused on any store but its
// own tenant's.
package signin

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

const (
	algorithm   = "pbkdf2-sha256"
	saltBytes   = 16
	hashBytes   = 32
	secretBytes = 32
	// maxPasswordBytes bounds what is hashed. It is a limit on input, not a
	// rule about passwords, so it is not a setting.
	maxPasswordBytes = 1024
	tokenPrefix      = "kss_"
	stampLayout      = "2006-01-02T15:04:05Z"

	// MethodPassword and MethodPasskey say how a session was opened.
	MethodPassword = "password"
	MethodPasskey  = "passkey"
)

var (
	// ErrRefused is a sign-in that did not succeed. For a password it never
	// says why. For a passkey it wraps the reason, which is for the log and
	// not for the person at the door.
	ErrRefused = errors.New("sign-in refused")
	// ErrWait is password sign-in refused because of too many wrong
	// passwords in a row. The password was not looked at.
	ErrWait = errors.New("too many wrong passwords in a row: wait, then try again")
	// ErrNoSession is a token that is not a live session of this tenant.
	ErrNoSession = errors.New("there is no such session")
	// ErrBadPassword is a new password that breaks the rules for one.
	ErrBadPassword = errors.New("that password cannot be used")
)

// now is the clock. Tests move it.
var now = func() time.Time { return time.Now().UTC() }

// Options are the values someone might change. Each is a setting in
// settings/core.json under "signin.".
type Options struct {
	PasswordIterations int
	PasswordMinLength  int
	SessionLifetime    time.Duration
	FailuresBeforeWait int
	FailureWait        time.Duration

	Passkeys                 bool
	PasskeyRPID              string
	PasskeyOrigin            string
	PasskeyChallengeLifetime time.Duration
}

// Defaults returns the options as the settings registry ships them.
func Defaults() (Options, error) {
	reg, err := settings.Registry()
	if err != nil {
		return Options{}, err
	}
	return ReadOptions(func(key string) (string, bool) {
		s, ok := settings.Lookup(reg, key)
		return s.Default, ok
	})
}

// ReadOptions builds the options from wherever setting values are kept.
// get returns the value for a setting's key.
func ReadOptions(get func(key string) (string, bool)) (Options, error) {
	var o Options
	var err error
	read := func(key string, into any) {
		if err != nil {
			return
		}
		v, ok := get(key)
		if !ok {
			err = fmt.Errorf("the %s setting is missing", key)
			return
		}
		switch p := into.(type) {
		case *int:
			*p, err = strconv.Atoi(v)
		case *bool:
			*p, err = strconv.ParseBool(v)
		case *time.Duration:
			*p, err = time.ParseDuration(v)
		case *string:
			*p = v
		}
		if err != nil {
			err = fmt.Errorf("%s: %w", key, err)
		}
	}
	read("signin.password_iterations", &o.PasswordIterations)
	read("signin.password_min_length", &o.PasswordMinLength)
	read("signin.session_lifetime", &o.SessionLifetime)
	read("signin.failures_before_wait", &o.FailuresBeforeWait)
	read("signin.failure_wait", &o.FailureWait)
	read("signin.passkeys", &o.Passkeys)
	read("signin.passkey_rp_id", &o.PasskeyRPID)
	read("signin.passkey_origin", &o.PasskeyOrigin)
	read("signin.passkey_challenge_lifetime", &o.PasskeyChallengeLifetime)
	if err != nil {
		return Options{}, err
	}
	return o, checkOptions(o)
}

func checkOptions(o Options) error {
	switch {
	case o.PasswordIterations < 1:
		return errors.New("signin.password_iterations must be 1 or more")
	case o.PasswordMinLength < 1:
		return errors.New("signin.password_min_length must be 1 or more")
	case o.SessionLifetime <= 0:
		return errors.New("signin.session_lifetime must be longer than nothing")
	case o.FailuresBeforeWait < 1:
		return errors.New("signin.failures_before_wait must be 1 or more")
	case o.FailureWait < 0:
		return errors.New("signin.failure_wait cannot be negative")
	}
	return nil
}

// Session is one sign-in, on one browser or device, for one tenant.
type Session struct {
	// ID names the session in the audit log and on a settings screen. It is
	// not the token and opens nothing.
	ID       string
	TenantID string
	// Method is MethodPassword or MethodPasskey.
	Method  string
	Created time.Time
	Expires time.Time
}

// SetPassword gives the tenant a password, or replaces the one it has, and
// ends every session, so a password changed because it leaked shuts out
// whoever was using it.
func SetPassword(s *tenant.Store, password string, o Options) error {
	if err := checkOptions(o); err != nil {
		return err
	}
	if utf8.RuneCountInString(password) < o.PasswordMinLength {
		return fmt.Errorf("%w: a password is at least %d characters", ErrBadPassword, o.PasswordMinLength)
	}
	if len(password) > maxPasswordBytes {
		return fmt.Errorf("%w: a password is at most %d bytes", ErrBadPassword, maxPasswordBytes)
	}
	if !utf8.ValidString(password) {
		// A browser sends UTF-8. Bytes that are not were mangled on the way
		// in, and the person could never type them again.
		return fmt.Errorf("%w: it is not UTF-8 text", ErrBadPassword)
	}
	salt, hash, err := hashPassword(password, o.PasswordIterations)
	if err != nil {
		return err
	}
	tx, err := s.DB().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO signin_password(id, algorithm, iterations, salt, hash, set_at, failures, last_failure)
		VALUES(1, ?, ?, ?, ?, ?, 0, '')
		ON CONFLICT(id) DO UPDATE SET algorithm = excluded.algorithm, iterations = excluded.iterations,
			salt = excluded.salt, hash = excluded.hash, set_at = excluded.set_at, failures = 0, last_failure = ''`,
		algorithm, o.PasswordIterations, salt, hash, stamp(now())); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM signin_sessions"); err != nil {
		return err
	}
	return tx.Commit()
}

// HasPassword reports whether the tenant has a password.
func HasPassword(s *tenant.Store) (bool, error) {
	var one int
	err := s.DB().QueryRow("SELECT 1 FROM signin_password WHERE id = 1").Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// SignIn checks the tenant's password and opens a session. It returns the
// session token, which exists only in this return value: the database keeps
// a hash of it.
//
// A wrong password, and a tenant with no password, are both ErrRefused.
// After Options.FailuresBeforeWait wrong passwords in a row, the next try
// is ErrWait until Options.FailureWait has passed since the last one.
func SignIn(s *tenant.Store, password string, o Options) (string, Session, error) {
	if err := checkOptions(o); err != nil {
		return "", Session{}, err
	}
	if len(password) > maxPasswordBytes {
		return "", Session{}, ErrRefused
	}
	db, t := s.DB(), now()

	// Count this try before the password is looked at, in one statement, so
	// tries made at the same moment cannot slip past the limit together.
	res, err := db.Exec(`UPDATE signin_password SET failures = failures + 1, last_failure = ?
		WHERE id = 1 AND NOT (failures >= ? AND last_failure > ?)`,
		stamp(t), o.FailuresBeforeWait, stamp(t.Add(-o.FailureWait)))
	if err != nil {
		return "", Session{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return "", Session{}, err
	} else if n == 0 {
		has, err := HasPassword(s)
		if err != nil {
			return "", Session{}, err
		}
		if has {
			return "", Session{}, ErrWait
		}
		return "", Session{}, ErrRefused
	}

	var (
		alg        string
		iterations int
		salt, hash []byte
	)
	if err := db.QueryRow("SELECT algorithm, iterations, salt, hash FROM signin_password WHERE id = 1").
		Scan(&alg, &iterations, &salt, &hash); err != nil {
		return "", Session{}, err
	}
	if alg != algorithm || iterations < 1 || len(hash) == 0 {
		return "", Session{}, fmt.Errorf("tenant %s: the stored password is in a form this binary does not know (%s)", s.TenantID(), alg)
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(hash))
	if err != nil {
		return "", Session{}, err
	}
	if subtle.ConstantTimeCompare(got, hash) != 1 {
		return "", Session{}, ErrRefused
	}

	// The password is right. Clear the count, and move the stored hash to
	// today's iteration setting if it was made under another.
	if iterations != o.PasswordIterations {
		if salt, hash, err = hashPassword(password, o.PasswordIterations); err != nil {
			return "", Session{}, err
		}
	}
	if _, err := db.Exec("UPDATE signin_password SET failures = 0, last_failure = '', iterations = ?, salt = ?, hash = ? WHERE id = 1",
		o.PasswordIterations, salt, hash); err != nil {
		return "", Session{}, err
	}
	return newSession(s, MethodPassword, o, t)
}

func hashPassword(password string, iterations int) (salt, hash []byte, err error) {
	salt = make([]byte, saltBytes)
	if _, err = rand.Read(salt); err != nil {
		return nil, nil, err
	}
	hash, err = pbkdf2.Key(sha256.New, password, salt, iterations, hashBytes)
	return salt, hash, err
}

// tokenShape is a session token: the prefix, the tenant's id, and 32 random
// bytes in hex.
var tokenShape = regexp.MustCompile(`^kss_([a-f0-9]+)_([a-f0-9]{64})$`)

// TenantOf reads the tenant id a token names. It proves nothing: the token
// is only a session once Check has found it in that tenant's database.
func TenantOf(token string) (string, bool) {
	m := tokenShape.FindStringSubmatch(token)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// newSession opens a session on this tenant's store and returns its token.
func newSession(s *tenant.Store, method string, o Options, t time.Time) (string, Session, error) {
	var raw [secretBytes + 8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", Session{}, err
	}
	token := tokenPrefix + s.TenantID() + "_" + hex.EncodeToString(raw[:secretBytes])
	t = t.Truncate(time.Second)
	sess := Session{
		ID:       hex.EncodeToString(raw[secretBytes:]),
		TenantID: s.TenantID(),
		Method:   method,
		Created:  t,
		Expires:  t.Add(o.SessionLifetime).Truncate(time.Second),
	}
	db := s.DB()
	if _, err := db.Exec("DELETE FROM signin_sessions WHERE expires_at <= ?", stamp(t)); err != nil {
		return "", Session{}, err
	}
	if _, err := db.Exec("INSERT INTO signin_sessions(id, token_hash, method, created_at, expires_at) VALUES(?, ?, ?, ?, ?)",
		sess.ID, hashToken(token), method, stamp(sess.Created), stamp(sess.Expires)); err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

// Check reports whether token is a live session of this store's tenant.
// A token that names another tenant is refused before the database is read,
// and a token that names this one is still nothing until its hash is found
// here. Every refusal is ErrNoSession.
func Check(s *tenant.Store, token string) (Session, error) {
	if id, ok := TenantOf(token); !ok || id != s.TenantID() {
		return Session{}, ErrNoSession
	}
	sess := Session{TenantID: s.TenantID()}
	var created, expires string
	err := s.DB().QueryRow("SELECT id, method, created_at, expires_at FROM signin_sessions WHERE token_hash = ?", hashToken(token)).
		Scan(&sess.ID, &sess.Method, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, err
	}
	if sess.Created, err = time.Parse(stampLayout, created); err != nil {
		return Session{}, err
	}
	if sess.Expires, err = time.Parse(stampLayout, expires); err != nil {
		return Session{}, err
	}
	if !now().Before(sess.Expires) {
		if _, err := s.DB().Exec("DELETE FROM signin_sessions WHERE id = ?", sess.ID); err != nil {
			return Session{}, err
		}
		return Session{}, ErrNoSession
	}
	return sess, nil
}

// Resolve turns a presented session token into the one tenant's store. It
// is what the API calls at the top of every route: the tenant comes from
// the token and from nowhere else in the request. The caller closes the
// store. Every refusal is ErrNoSession.
//
// A server that keeps stores open can do the same in two steps: TenantOf to
// pick the store, then Check on it.
func Resolve(in *tenant.Install, token string) (*tenant.Store, Session, error) {
	id, ok := TenantOf(token)
	if !ok {
		return nil, Session{}, ErrNoSession
	}
	known := false
	for _, t := range in.Tenants() {
		if t.ID == id {
			known = true
			break
		}
	}
	if !known {
		return nil, Session{}, ErrNoSession
	}
	s, err := in.Store(id)
	if err != nil {
		return nil, Session{}, err
	}
	sess, err := Check(s, token)
	if err != nil {
		s.Close()
		return nil, Session{}, err
	}
	return s, sess, nil
}

// SignOut ends the session the token belongs to. A token that is not a
// session of this tenant is ErrNoSession, and ends nothing.
func SignOut(s *tenant.Store, token string) error {
	if id, ok := TenantOf(token); !ok || id != s.TenantID() {
		return ErrNoSession
	}
	res, err := s.DB().Exec("DELETE FROM signin_sessions WHERE token_hash = ?", hashToken(token))
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

// SignOutAll ends every session of this tenant, on every device.
func SignOutAll(s *tenant.Store) error {
	_, err := s.DB().Exec("DELETE FROM signin_sessions")
	return err
}

func stamp(t time.Time) string { return t.UTC().Format(stampLayout) }
