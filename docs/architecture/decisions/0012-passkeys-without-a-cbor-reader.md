# 0012. Passkeys are checked with the standard library, without a CBOR reader

November 24, 2026 · decided in P2-02

## The question

A passkey is WebAuthn. When a browser makes one, the answer in its oldest form carries the new public key inside an "attestation object", written in CBOR. Go's standard library does not read CBOR. The usual way is a WebAuthn module, which brings a CBOR module and several others with it. Keystone has one outside module so far and adds another only when the standard library has no reasonable way.

## The decision

No new module. The browser is asked for the same facts in forms the standard library reads.

- The page calls `getPublicKey()` and `getAuthenticatorData()` on the browser's answer and sends both. The public key arrives as SubjectPublicKeyInfo, which `crypto/x509` reads. The authenticator data starts with fields of fixed size: the site's hash, the flags, the use count, and the credential id. The server reads those and stops before the CBOR.
- No attestation is asked for. Attestation says which make of device holds the passkey, and Keystone does not care. Without it, nothing the server trusts has signed the attestation object, so reading the key from there proves no more than reading it from `getPublicKey()`.
- A sign-in needs no CBOR in any case: the client data is JSON, the authenticator data is fixed fields, and the signature is checked with `crypto/ecdsa`, `crypto/ed25519` or `crypto/rsa`.

The rules that go with it:

- Keys may be ES256, Ed25519, or RS256 of 2048 bits or more. Those are the three kinds the WebAuthn rules require a browser to hand over through `getPublicKey()`.
- The device must verify the person every time: a fingerprint, a face or a PIN. A passkey stands in for the password, not beside it.
- The passkey is made with the tenant's id as its user id, and must give that id back as the user handle at sign-in. The signature does not cover the user handle, so this is a second check. The first is that the passkey's key is in that tenant's database and in no other.
- A challenge is kept in the tenant's database, works once, and runs out after `signin.passkey_challenge_lifetime`. A tenant keeps sixteen unanswered prompts of each kind at most.
- A use count that stands still or goes back is refused. A count of zero both times is accepted, because passkeys that sync between devices do not count.
- Passkeys are behind the setting `signin.passkeys`, which is off, until the sign-in screen has proven them in a real browser.

## What it costs

- It is proven against an authenticator written in the tests, not against a browser. The test authenticator builds its bytes from the WebAuthn rules, so a misreading of the rules would be in both. P4-01 closes this when it builds the sign-in screen.
- A browser too old to have `getPublicKey()` and `getAuthenticatorData()` cannot add a passkey. It can still sign in with one, and with a password.
- With no attestation, an install cannot limit passkeys to one make of device.
- A security key that cannot verify the person, or cannot hold the user id, is refused.
- The challenge is kept in the tenant's database, so the tenant must be known before a passkey prompt is sent. A sign-in that starts from the passkey alone, with no name picked first, would need the challenge kept somewhere else. That rests on the same open question as decision 0007.
- If attestation is ever wanted, a CBOR reader comes with it, and this is worth another look.
