package adminaccount

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Setup codes (P15-02). An admin adds a person, or gives one a new code, and
// the person enters the code at /setup to set their own password: the admin
// never sees or sets it. A code is good once, for admin.setup_code_for, for
// the one person it was made for. Only its hash is kept, here in the admin
// database beside the account that made it, so a copied admin database
// holds no code that works.

// ErrNoCode is a setup code that does not work: not one Keystone made, used
// already, replaced by a newer one, or expired. The reason is never told
// apart, to the person or anyone else.
var ErrNoCode = errors.New("that setup code does not work")

// codeAlphabet is 32 letters and digits with none that look alike (no I, O,
// 0 or 1), so a code read aloud or copied by hand survives.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// codeLength is the letters in a code: 20 of 32, 100 bits.
const codeLength = 20

// NormalizeCode is a code as it is kept: upper case, with the spaces and
// dashes a person may type left out.
func NormalizeCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if r == '-' || r == ' ' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hashCode(code string) []byte {
	sum := sha256.Sum256([]byte("keystone setup code\x00" + NormalizeCode(code)))
	return sum[:]
}

// newCode makes a code, in groups of four for reading: ABCD-EFGH-JKLM-NPQR-STUV.
func newCode() (string, error) {
	raw := make([]byte, codeLength)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, x := range raw {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(codeAlphabet[int(x)%len(codeAlphabet)]) // 256 is a multiple of 32: no bias
	}
	return b.String(), nil
}

// IssueSetupCode makes a setup code for a person, good until ttl after now,
// made by the admin account by, and ends every earlier code of that person
// that was not used. It returns the code, which exists only in this return
// value, and when it expires.
func (s *Store) IssueSetupCode(tenantID, by string, ttl time.Duration, now time.Time) (string, time.Time, error) {
	if ttl <= 0 {
		return "", time.Time{}, errors.New("admin.setup_code_for must be longer than nothing")
	}
	if strings.TrimSpace(tenantID) == "" {
		return "", time.Time{}, errors.New("a setup code is for a person")
	}
	code, err := newCode()
	if err != nil {
		return "", time.Time{}, err
	}
	now = now.UTC().Truncate(time.Second)
	expires := now.Add(ttl)
	tx, err := s.db.Begin()
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE setup_codes SET ended_at = ? WHERE tenant_id = ? AND used_at = '' AND ended_at = ''", stamp(now), tenantID); err != nil {
		return "", time.Time{}, err
	}
	if _, err := tx.Exec("INSERT INTO setup_codes(code_hash, tenant_id, issued_by, created_at, expires_at) VALUES(?, ?, ?, ?, ?)",
		hashCode(code), tenantID, by, stamp(now), stamp(expires)); err != nil {
		return "", time.Time{}, err
	}
	return code, expires, tx.Commit()
}

// SetupCodeFor reads which person a code is for, if it still works at now,
// and uses nothing.
func (s *Store) SetupCodeFor(code string, now time.Time) (string, error) {
	var id string
	err := s.db.QueryRow("SELECT tenant_id FROM setup_codes WHERE code_hash = ? AND used_at = '' AND ended_at = '' AND expires_at > ?",
		hashCode(code), stamp(now)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoCode
	}
	return id, err
}

// UseSetupCode uses a code for the person it is for, in one statement, so
// two uses at the same moment cannot both succeed. It returns ErrNoCode for
// a code that does not work at now, or is another person's.
func (s *Store) UseSetupCode(code, tenantID string, now time.Time) error {
	res, err := s.db.Exec("UPDATE setup_codes SET used_at = ? WHERE code_hash = ? AND tenant_id = ? AND used_at = '' AND ended_at = '' AND expires_at > ?",
		stamp(now), hashCode(code), tenantID, stamp(now))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNoCode
	}
	return nil
}

// SetupWaiting returns, for each person with a code that still works at
// now, when the newest one expires.
func (s *Store) SetupWaiting(now time.Time) (map[string]time.Time, error) {
	rows, err := s.db.Query("SELECT tenant_id, max(expires_at) FROM setup_codes WHERE used_at = '' AND ended_at = '' AND expires_at > ? GROUP BY tenant_id", stamp(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var id, at string
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		t, err := parseStamp(at)
		if err != nil {
			return nil, fmt.Errorf("a setup code's time does not read: %w", err)
		}
		out[id] = t
	}
	return out, rows.Err()
}
