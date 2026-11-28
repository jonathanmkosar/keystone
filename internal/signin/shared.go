package signin

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// The rules a person's sign-in follows, for a sign-in kept somewhere else:
// the admin accounts in data/admin/admin.db (P15-01, decision 0650). An
// admin's password is hashed, checked and refused under the same settings
// and the same code as a person's, and an admin's passkey is read and
// verified the same way. What is kept where, and the count of wrong tries,
// belong to the caller.

// PasswordAlgorithm names the hash a password is kept under.
const PasswordAlgorithm = algorithm

// CheckPasswordRules reports whether password may be set as a new password
// under o, with ErrBadPassword when it breaks a rule.
func CheckPasswordRules(password string, o Options) error {
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
		return fmt.Errorf("%w: it is not UTF-8 text", ErrBadPassword)
	}
	return nil
}

// HashPassword hashes a password with a fresh salt, under iterations.
func HashPassword(password string, iterations int) (salt, hash []byte, err error) {
	if iterations < 1 {
		return nil, nil, fmt.Errorf("a password hash needs 1 iteration or more")
	}
	return hashPassword(password, iterations)
}

// PasswordMatches reports whether password is the one hashed as hash, with
// salt and iterations. The comparison takes the same time whatever it
// finds. A password longer than any that could be set never matches.
func PasswordMatches(password string, iterations int, salt, hash []byte) (bool, error) {
	if len(password) > maxPasswordBytes {
		return false, nil
	}
	if iterations < 1 || len(hash) == 0 {
		return false, fmt.Errorf("the stored password is in a form this binary does not know")
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(hash))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, hash) == 1, nil
}

// CheckPasskeyOptions reports whether passkeys may be used under o:
// ErrPasskeysOff while signin.passkeys is false.
func CheckPasskeyOptions(o Options) error { return checkPasskeyOptions(o) }

// ReadClientData reads a passkey answer's client data: the challenge, and
// whether the answer is of the right kind (made, when create is true, or
// used) from the right page. The challenge is returned whenever it can be
// read, even with an error, so the caller can spend it either way: a
// prompt is answered once.
func ReadClientData(raw []byte, create bool, o Options) ([]byte, error) {
	var cd struct {
		Type        string `json:"type"`
		Challenge   string `json:"challenge"`
		Origin      string `json:"origin"`
		CrossOrigin bool   `json:"crossOrigin"`
	}
	if err := json.Unmarshal(raw, &cd); err != nil {
		return nil, refused("the client data is not JSON")
	}
	challenge, err := base64.RawURLEncoding.DecodeString(cd.Challenge)
	if err != nil || len(challenge) == 0 {
		return nil, refused("the challenge is not base64url")
	}
	typ := typeGet
	if create {
		typ = typeCreate
	}
	switch {
	case cd.Type != typ:
		return challenge, refused("the answer is for another kind of request")
	case cd.Origin != o.PasskeyOrigin:
		return challenge, refused(fmt.Sprintf("the answer came from another page, %q", cd.Origin))
	case cd.CrossOrigin:
		return challenge, refused("the answer came from a page inside another site's page")
	}
	return challenge, nil
}

// ReadAuthData reads a passkey answer's authenticator data: the use count
// and, when a passkey is being made, its credential id. It refuses data for
// another site, and a device that did not verify the person.
func ReadAuthData(raw []byte, o Options) (signCount uint32, credentialID []byte, err error) {
	ad, err := parseAuthData(raw, o.PasskeyRPID)
	if err != nil {
		return 0, nil, err
	}
	return ad.signCount, ad.credentialID, nil
}

// CheckPasskeyKey refuses a public key of a kind PasskeyAlgorithms does not
// list, or one too weak.
func CheckPasskeyKey(spki []byte) error {
	_, err := parseKey(spki)
	return err
}

// VerifyPasskey checks a passkey's signature over its authenticator data
// and the hash of the client data, with the public key it was added with.
func VerifyPasskey(spki, authData, clientDataJSON, signature []byte) error {
	key, err := parseKey(spki)
	if err != nil {
		return err
	}
	clientHash := sha256.Sum256(clientDataJSON)
	signed := append(append([]byte{}, authData...), clientHash[:]...)
	if !verify(key, signed, signature) {
		return refused("the signature does not match the passkey")
	}
	return nil
}
