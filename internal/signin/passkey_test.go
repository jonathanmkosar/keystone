package signin

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// device stands in for a phone or a laptop that holds a passkey. It builds
// the same bytes a browser hands to the server, from the WebAuthn rules,
// with a key made for the test. It is not a browser: what a real one sends
// is proven with the sign-in screen.
type device struct {
	credentialID []byte
	publicKey    []byte // SubjectPublicKeyInfo, as getPublicKey gives it
	sign         func(signed []byte) []byte
	count        uint32
	flags        byte
	rpID         string
	origin       string
	userHandle   []byte
}

func newDevice(t *testing.T, kind string, o Options) *device {
	t.Helper()
	d := &device{
		credentialID: make([]byte, 20),
		flags:        flagUserPresent | flagUserVerified,
		rpID:         o.PasskeyRPID,
		origin:       o.PasskeyOrigin,
	}
	if _, err := rand.Read(d.credentialID); err != nil {
		t.Fatal(err)
	}
	var public any
	switch kind {
	case "es256", "p384":
		curve := elliptic.P256()
		if kind == "p384" {
			curve = elliptic.P384()
		}
		key, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		public = &key.PublicKey
		d.sign = func(signed []byte) []byte {
			digest := sha256.Sum256(signed)
			sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
			if err != nil {
				t.Fatal(err)
			}
			return sig
		}
	case "rs256", "rsa1024":
		bits := 2048
		if kind == "rsa1024" {
			bits = 1024
		}
		key, err := rsa.GenerateKey(rand.Reader, bits)
		if err != nil {
			t.Fatal(err)
		}
		public = &key.PublicKey
		d.sign = func(signed []byte) []byte {
			digest := sha256.Sum256(signed)
			sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
			if err != nil {
				t.Fatal(err)
			}
			return sig
		}
	case "ed25519":
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		public = pub
		d.sign = func(signed []byte) []byte { return ed25519.Sign(key, signed) }
	default:
		t.Fatalf("no device of kind %q", kind)
	}
	var err error
	if d.publicKey, err = x509.MarshalPKIXPublicKey(public); err != nil {
		t.Fatal(err)
	}
	return d
}

func clientData(t *testing.T, typ string, challenge []byte, origin string, crossOrigin bool) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type":        typ,
		"challenge":   base64.RawURLEncoding.EncodeToString(challenge),
		"origin":      origin,
		"crossOrigin": crossOrigin,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// authenticatorData lays the bytes out as parseAuthData's comment says.
func (d *device) authenticatorData(attested bool) []byte {
	site := sha256.Sum256([]byte(d.rpID))
	out := append([]byte{}, site[:]...)
	flags := d.flags
	if attested {
		flags |= flagAttestedData
	}
	out = append(out, flags)
	out = binary.BigEndian.AppendUint32(out, d.count)
	if attested {
		out = append(out, make([]byte, 16)...) // the kind of device: all zero
		out = binary.BigEndian.AppendUint16(out, uint16(len(d.credentialID)))
		out = append(out, d.credentialID...)
		// A real device puts the key here as CBOR. The server does not
		// read it, so the test writes an empty CBOR map.
		out = append(out, 0xa0)
	}
	return out
}

// create answers a prompt to make a passkey.
func (d *device) create(t *testing.T, p PasskeyPrompt) PasskeyCreation {
	t.Helper()
	d.userHandle = p.UserID
	return PasskeyCreation{
		CredentialID:      d.credentialID,
		ClientDataJSON:    clientData(t, typeCreate, p.Challenge, d.origin, false),
		AuthenticatorData: d.authenticatorData(true),
		PublicKey:         d.publicKey,
	}
}

// get answers a prompt to use the passkey.
func (d *device) get(t *testing.T, p PasskeyPrompt) PasskeyAssertion {
	t.Helper()
	a := PasskeyAssertion{
		CredentialID:      d.credentialID,
		ClientDataJSON:    clientData(t, typeGet, p.Challenge, d.origin, false),
		AuthenticatorData: d.authenticatorData(false),
		UserHandle:        d.userHandle,
	}
	a.Signature = d.sign(signedBytes(a))
	return a
}

func signedBytes(a PasskeyAssertion) []byte {
	hash := sha256.Sum256(a.ClientDataJSON)
	return append(append([]byte{}, a.AuthenticatorData...), hash[:]...)
}

// added makes a device and adds its passkey to the store.
func added(t *testing.T, s *tenant.Store, kind string, o Options) *device {
	t.Helper()
	d := newDevice(t, kind, o)
	p, err := BeginAddPasskey(s, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddPasskey(s, "Test laptop", d.create(t, p), o); err != nil {
		t.Fatal(err)
	}
	return d
}

func prompt(t *testing.T, s *tenant.Store, o Options) PasskeyPrompt {
	t.Helper()
	p, err := BeginPasskeySignIn(s, o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPasskeyAddAndSignIn(t *testing.T) {
	for _, kind := range []string{"es256", "rs256", "ed25519"} {
		t.Run(kind, func(t *testing.T) {
			_, a, _, _ := twoTenants(t)
			o := testOptions()
			advance := fixClock(t)

			d := newDevice(t, kind, o)
			p, err := BeginAddPasskey(a, o)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Challenge) != challengeBytes || p.RPID != o.PasskeyRPID || string(p.UserID) != a.TenantID() || len(p.Credentials) != 0 {
				t.Errorf("the prompt to add is wrong: %+v", p)
			}
			pk, err := AddPasskey(a, "  Test laptop ", d.create(t, p), o)
			if err != nil {
				t.Fatal(err)
			}
			list, err := Passkeys(a)
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 1 || list[0].ID != pk.ID || list[0].Name != "Test laptop" || !list[0].LastUsed.IsZero() {
				t.Fatalf("the passkey list is wrong: %+v", list)
			}

			advance(time.Minute)
			p = prompt(t, a, o)
			if len(p.Credentials) != 1 || string(p.Credentials[0]) != string(d.credentialID) {
				t.Errorf("the prompt to sign in does not name the passkey: %+v", p)
			}
			token, sess, err := PasskeySignIn(a, d.get(t, p), o)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Check(a, token)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != sess.ID || got.Method != MethodPasskey || got.TenantID != a.TenantID() {
				t.Errorf("the session is wrong: %+v", got)
			}
			if list, _ = Passkeys(a); len(list) != 1 || list[0].LastUsed.IsZero() {
				t.Errorf("the passkey's last use was not recorded: %+v", list)
			}

			// A passkey needs no password, and wrong passwords do not shut it out.
			if has, _ := HasPassword(a); has {
				t.Error("adding a passkey set a password")
			}

			// Removing it ends its use.
			if err := RemovePasskey(a, pk.ID); err != nil {
				t.Fatal(err)
			}
			if err := RemovePasskey(a, pk.ID); !errors.Is(err, ErrNoPasskey) {
				t.Errorf("removing a passkey twice: %v", err)
			}
			if _, _, err := PasskeySignIn(a, d.get(t, prompt(t, a, o)), o); !errors.Is(err, ErrRefused) {
				t.Errorf("a removed passkey signed in: %v", err)
			}
		})
	}
}

func TestPasskeyAlgorithmsAreTheThreeThatAreTested(t *testing.T) {
	// ES256, Ed25519 and RS256, by their COSE numbers. Each is added and
	// used in TestPasskeyAddAndSignIn.
	got := PasskeyAlgorithms()
	if len(got) != 3 || got[0] != -7 || got[1] != -8 || got[2] != -257 {
		t.Errorf("PasskeyAlgorithms is %v", got)
	}
}

func TestPasskeySignInRefusals(t *testing.T) {
	_, a, b, _ := twoTenants(t)
	o := testOptions()
	advance := fixClock(t)
	d := added(t, a, "es256", o)
	stranger := newDevice(t, "es256", o)
	stranger.userHandle = d.userHandle

	// Each case changes one thing about a good answer.
	cases := map[string]func(p PasskeyPrompt) PasskeyAssertion{
		"another page": func(p PasskeyPrompt) PasskeyAssertion {
			e := *d
			e.origin = "http://localhost:7901"
			return e.get(t, p)
		},
		"a look-alike page": func(p PasskeyPrompt) PasskeyAssertion {
			e := *d
			e.origin = "http://localhost:7900.test.invalid"
			return e.get(t, p)
		},
		"another site": func(p PasskeyPrompt) PasskeyAssertion {
			e := *d
			e.rpID = "test.invalid"
			return e.get(t, p)
		},
		"a page inside another site's page": func(p PasskeyPrompt) PasskeyAssertion {
			x := d.get(t, p)
			x.ClientDataJSON = clientData(t, typeGet, p.Challenge, d.origin, true)
			x.Signature = d.sign(signedBytes(x))
			return x
		},
		"an answer of the wrong kind": func(p PasskeyPrompt) PasskeyAssertion {
			x := d.get(t, p)
			x.ClientDataJSON = clientData(t, typeCreate, p.Challenge, d.origin, false)
			x.Signature = d.sign(signedBytes(x))
			return x
		},
		"a challenge that was never given": func(p PasskeyPrompt) PasskeyAssertion {
			p.Challenge = make([]byte, challengeBytes)
			return d.get(t, p)
		},
		"a challenge given for adding": func(p PasskeyPrompt) PasskeyAssertion {
			add, err := BeginAddPasskey(a, o)
			if err != nil {
				t.Fatal(err)
			}
			return d.get(t, add)
		},
		"a challenge given to another tenant": func(p PasskeyPrompt) PasskeyAssertion {
			return d.get(t, prompt(t, b, o))
		},
		"a challenge that ran out": func(p PasskeyPrompt) PasskeyAssertion {
			advance(o.PasskeyChallengeLifetime)
			return d.get(t, p)
		},
		"client data that is not JSON": func(p PasskeyPrompt) PasskeyAssertion {
			x := d.get(t, p)
			x.ClientDataJSON = []byte("not json")
			return x
		},
		"the person not verified": func(p PasskeyPrompt) PasskeyAssertion {
			e := *d
			e.flags = flagUserPresent
			return e.get(t, p)
		},
		"the person not present": func(p PasskeyPrompt) PasskeyAssertion {
			e := *d
			e.flags = flagUserVerified
			return e.get(t, p)
		},
		"authenticator data cut short": func(p PasskeyPrompt) PasskeyAssertion {
			x := d.get(t, p)
			x.AuthenticatorData = x.AuthenticatorData[:authDataFixedSize-1]
			x.Signature = d.sign(signedBytes(x))
			return x
		},
		"a signature with one bit changed": func(p PasskeyPrompt) PasskeyAssertion {
			x := d.get(t, p)
			x.Signature[len(x.Signature)-1] ^= 1
			return x
		},
		"no signature": func(p PasskeyPrompt) PasskeyAssertion {
			x := d.get(t, p)
			x.Signature = nil
			return x
		},
		"client data changed after signing": func(p PasskeyPrompt) PasskeyAssertion {
			x := d.get(t, p)
			x.ClientDataJSON = append(x.ClientDataJSON, ' ')
			return x
		},
		"authenticator data changed after signing": func(p PasskeyPrompt) PasskeyAssertion {
			e := *d
			e.count = 7
			x := d.get(t, p)
			x.AuthenticatorData = e.authenticatorData(false)
			return x
		},
		"another device's key under this passkey's id": func(p PasskeyPrompt) PasskeyAssertion {
			e := *stranger
			e.credentialID = d.credentialID
			return e.get(t, p)
		},
		"a passkey that was never added": func(p PasskeyPrompt) PasskeyAssertion {
			return stranger.get(t, p)
		},
		"another tenant's user handle": func(p PasskeyPrompt) PasskeyAssertion {
			e := *d
			e.userHandle = []byte(b.TenantID())
			return e.get(t, p)
		},
		"no user handle": func(p PasskeyPrompt) PasskeyAssertion {
			e := *d
			e.userHandle = nil
			return e.get(t, p)
		},
	}
	for name, change := range cases {
		token, _, err := PasskeySignIn(a, change(prompt(t, a, o)), o)
		if !errors.Is(err, ErrRefused) {
			t.Errorf("%s: signed in: %v", name, err)
		}
		if token != "" {
			t.Errorf("%s: a token was returned with the refusal", name)
		}
	}
	var n int
	if err := a.DB().QueryRow("SELECT count(*) FROM signin_sessions").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d sessions were opened by refused answers", n)
	}

	// After all that, the real device still signs in, and its answer
	// cannot be used a second time.
	answer := d.get(t, prompt(t, a, o))
	if _, _, err := PasskeySignIn(a, answer, o); err != nil {
		t.Fatalf("the real passkey was refused: %v", err)
	}
	if _, _, err := PasskeySignIn(a, answer, o); !errors.Is(err, ErrRefused) {
		t.Errorf("an answer was accepted twice: %v", err)
	}
}

func TestPasskeyUseCountMustClimb(t *testing.T) {
	_, a, _, _ := twoTenants(t)
	o := testOptions()
	d := added(t, a, "es256", o)

	signIn := func(count uint32) error {
		d.count = count
		_, _, err := PasskeySignIn(a, d.get(t, prompt(t, a, o)), o)
		return err
	}
	// A passkey that does not count says zero every time.
	for i := 0; i < 2; i++ {
		if err := signIn(0); err != nil {
			t.Fatalf("a passkey that does not count was refused: %v", err)
		}
	}
	if err := signIn(5); err != nil {
		t.Fatalf("a count of 5 after 0: %v", err)
	}
	for _, back := range []uint32{5, 4, 0} {
		if err := signIn(back); !errors.Is(err, ErrRefused) {
			t.Errorf("a count of %d after 5 was accepted: %v", back, err)
		}
	}
	if err := signIn(6); err != nil {
		t.Errorf("a count of 6 after 5: %v", err)
	}
}

func TestAddPasskeyRefusals(t *testing.T) {
	_, a, b, _ := twoTenants(t)
	o := testOptions()
	d := newDevice(t, "es256", o)

	begin := func() PasskeyPrompt {
		t.Helper()
		p, err := BeginAddPasskey(a, o)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := map[string]func(p PasskeyPrompt) PasskeyCreation{
		"another page": func(p PasskeyPrompt) PasskeyCreation {
			e := *d
			e.origin = "https://test.invalid"
			return e.create(t, p)
		},
		"another site": func(p PasskeyPrompt) PasskeyCreation {
			e := *d
			e.rpID = "test.invalid"
			return e.create(t, p)
		},
		"an answer of the wrong kind": func(p PasskeyPrompt) PasskeyCreation {
			c := d.create(t, p)
			c.ClientDataJSON = clientData(t, typeGet, p.Challenge, d.origin, false)
			return c
		},
		"a challenge given for signing in": func(p PasskeyPrompt) PasskeyCreation {
			return d.create(t, prompt(t, a, o))
		},
		"a challenge given to another tenant": func(p PasskeyPrompt) PasskeyCreation {
			other, err := BeginAddPasskey(b, o)
			if err != nil {
				t.Fatal(err)
			}
			return d.create(t, other)
		},
		"the person not verified": func(p PasskeyPrompt) PasskeyCreation {
			e := *d
			e.flags = flagUserPresent
			return e.create(t, p)
		},
		"authenticator data that names no credential": func(p PasskeyPrompt) PasskeyCreation {
			c := d.create(t, p)
			c.AuthenticatorData = d.authenticatorData(false)
			return c
		},
		"a credential id that disagrees": func(p PasskeyPrompt) PasskeyCreation {
			c := d.create(t, p)
			c.CredentialID = append([]byte{1}, c.CredentialID...)
			return c
		},
		"a credential id longer than the data": func(p PasskeyPrompt) PasskeyCreation {
			c := d.create(t, p)
			c.AuthenticatorData = c.AuthenticatorData[:len(c.AuthenticatorData)-5]
			return c
		},
		"a key that cannot be read": func(p PasskeyPrompt) PasskeyCreation {
			c := d.create(t, p)
			c.PublicKey = []byte("not a key")
			return c
		},
		"no key": func(p PasskeyPrompt) PasskeyCreation {
			c := d.create(t, p)
			c.PublicKey = nil
			return c
		},
		"a key on another curve": func(p PasskeyPrompt) PasskeyCreation {
			return newDevice(t, "p384", o).create(t, p)
		},
		"a short RSA key": func(p PasskeyPrompt) PasskeyCreation {
			return newDevice(t, "rsa1024", o).create(t, p)
		},
	}
	for name, change := range cases {
		if _, err := AddPasskey(a, "Test laptop", change(begin()), o); !errors.Is(err, ErrRefused) {
			t.Errorf("%s: the passkey was added: %v", name, err)
		}
	}
	for _, name := range []string{"", "   ", strings.Repeat("x", maxPasskeyName+1)} {
		if _, err := AddPasskey(a, name, d.create(t, begin()), o); err == nil {
			t.Errorf("a passkey named %q was added", name)
		}
	}
	if list, err := Passkeys(a); err != nil || len(list) != 0 {
		t.Fatalf("refused passkeys were stored: %+v %v", list, err)
	}

	// A good one is added once.
	good := d.create(t, begin())
	if _, err := AddPasskey(a, "Test laptop", good, o); err != nil {
		t.Fatal(err)
	}
	if _, err := AddPasskey(a, "Test laptop", good, o); !errors.Is(err, ErrRefused) {
		t.Errorf("an answer was accepted twice: %v", err)
	}
	if _, err := AddPasskey(a, "Test laptop again", d.create(t, begin()), o); !errors.Is(err, ErrRefused) {
		t.Errorf("the same passkey was added twice: %v", err)
	}
	if list, _ := Passkeys(a); len(list) != 1 {
		t.Errorf("%d passkeys are stored, want 1", len(list))
	}
}

func TestAPasskeyForOneTenantIsRefusedForAnother(t *testing.T) {
	_, a, b, _ := twoTenants(t)
	o := testOptions()
	d := added(t, a, "es256", o)
	other := added(t, b, "es256", o)

	// Tenant one's device answers tenant two's prompt.
	if _, _, err := PasskeySignIn(b, d.get(t, prompt(t, b, o)), o); !errors.Is(err, ErrRefused) {
		t.Errorf("tenant one's passkey signed in to tenant two: %v", err)
	}
	// Tenant one's good answer, to tenant one's prompt, shown to tenant two.
	if _, _, err := PasskeySignIn(b, d.get(t, prompt(t, a, o)), o); !errors.Is(err, ErrRefused) {
		t.Errorf("an answer to tenant one's prompt signed in to tenant two: %v", err)
	}

	// Tenant one's passkey row, copied into tenant two's database. The
	// device still says whose it is, and that is not tenant two.
	var id, name, created string
	var cred, key []byte
	var count int64
	if err := a.DB().QueryRow("SELECT id, credential_id, public_key, sign_count, name, created_at FROM signin_passkeys").
		Scan(&id, &cred, &key, &count, &name, &created); err != nil {
		t.Fatal(err)
	}
	if _, err := b.DB().Exec("INSERT INTO signin_passkeys(id, credential_id, public_key, sign_count, name, created_at) VALUES(?, ?, ?, ?, ?, ?)",
		id, cred, key, count, name, created); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PasskeySignIn(b, d.get(t, prompt(t, b, o)), o); !errors.Is(err, ErrRefused) {
		t.Errorf("a copied passkey row let tenant one's device sign in to tenant two: %v", err)
	}

	// Each still signs in to its own, and the token is its own tenant's.
	token, _, err := PasskeySignIn(a, d.get(t, prompt(t, a, o)), o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(b, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("a passkey session for tenant one was accepted on tenant two's store: %v", err)
	}
	if _, _, err := PasskeySignIn(b, other.get(t, prompt(t, b, o)), o); err != nil {
		t.Errorf("tenant two's own passkey was refused: %v", err)
	}
	// Removing by id reaches only the store it is asked of.
	list, err := Passkeys(a)
	if err != nil || len(list) != 1 {
		t.Fatalf("tenant one's passkeys: %+v %v", list, err)
	}
	otherList, err := Passkeys(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, pk := range otherList {
		if pk.ID != id {
			if err := RemovePasskey(a, pk.ID); !errors.Is(err, ErrNoPasskey) {
				t.Errorf("tenant two's passkey was removed through tenant one's store: %v", err)
			}
		}
	}
}

func TestPasskeysCanBeSwitchedOff(t *testing.T) {
	_, a, _, _ := twoTenants(t)
	on := testOptions()
	d := added(t, a, "es256", on)
	p := prompt(t, a, on)

	off := on
	off.Passkeys = false
	if _, err := BeginAddPasskey(a, off); !errors.Is(err, ErrPasskeysOff) {
		t.Errorf("BeginAddPasskey with passkeys off: %v", err)
	}
	if _, err := BeginPasskeySignIn(a, off); !errors.Is(err, ErrPasskeysOff) {
		t.Errorf("BeginPasskeySignIn with passkeys off: %v", err)
	}
	if _, err := AddPasskey(a, "Test laptop", newDevice(t, "es256", on).create(t, p), off); !errors.Is(err, ErrPasskeysOff) {
		t.Errorf("AddPasskey with passkeys off: %v", err)
	}
	if _, _, err := PasskeySignIn(a, d.get(t, p), off); !errors.Is(err, ErrPasskeysOff) {
		t.Errorf("PasskeySignIn with passkeys off: %v", err)
	}
	// Switching them off does not use up the prompt or lose the passkey.
	if _, _, err := PasskeySignIn(a, d.get(t, p), on); err != nil {
		t.Errorf("the passkey did not work when switched back on: %v", err)
	}
	for name, bad := range map[string]func(*Options){
		"no site":             func(o *Options) { o.PasskeyRPID = "" },
		"no page":             func(o *Options) { o.PasskeyOrigin = "" },
		"no time to answer":   func(o *Options) { o.PasskeyChallengeLifetime = 0 },
		"no session lifetime": func(o *Options) { o.SessionLifetime = 0 },
	} {
		o := on
		bad(&o)
		if _, _, err := PasskeySignIn(a, d.get(t, p), o); err == nil {
			t.Errorf("%s: sign-in went ahead", name)
		}
	}
}

func TestUnansweredPromptsAreBounded(t *testing.T) {
	_, a, _, _ := twoTenants(t)
	o := testOptions()
	advance := fixClock(t)
	d := added(t, a, "es256", o)

	var first, last PasskeyPrompt
	for i := 0; i < maxChallenges+5; i++ {
		last = prompt(t, a, o)
		if i == 0 {
			first = last
		}
	}
	count := func() (n int) {
		t.Helper()
		if err := a.DB().QueryRow("SELECT count(*) FROM signin_challenges").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != maxChallenges {
		t.Errorf("%d unanswered prompts are kept, want %d", n, maxChallenges)
	}
	if _, _, err := PasskeySignIn(a, d.get(t, first), o); !errors.Is(err, ErrRefused) {
		t.Errorf("the oldest prompt was still answerable: %v", err)
	}
	if _, _, err := PasskeySignIn(a, d.get(t, last), o); err != nil {
		t.Errorf("the newest prompt was dropped: %v", err)
	}
	// Prompts that ran out are cleared when the next is asked for.
	advance(o.PasskeyChallengeLifetime + time.Second)
	prompt(t, a, o)
	if n := count(); n != 1 {
		t.Errorf("%d prompts after the old ones ran out, want 1", n)
	}
}
