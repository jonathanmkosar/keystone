# 0120. An entry may trust one certificate authority of its own, and nothing turns the check off

November 24, 2026 · decided in P3-06

## The question

The Mac mini gateway serves https with a certificate from its own authority, which no machine trusts by default. Keystone has to call it. Where does the trust come from, and how far does it reach?

## The decision

**A field of the registry entry, `ca_cert`.** It holds one authority's certificate, as PEM text or as the path of a PEM file on the machine Keystone runs on. The adapter reads it when the provider is built.

**It reaches that entry's calls and no others.** Each provider has its own transport. Nothing is added to the machine's trust store or to another entry.

**An entry with `ca_cert` trusts that authority only.** The usual authorities are left out of its pool. An entry without the field trusts the usual ones.

**Nothing turns the check off.** There is no setting, no field and no option for it. The certificate is checked in full either way: signature, dates and host name. A test fails if the adapter's code names the switch.

**The registry checks what it can without reading the certificate.** `ca_cert` is refused on an `http` endpoint, on the `fake` adapter, when it is too long, and when it holds a private key. The adapter refuses a value with no certificate in it.

## Why

- The plan says adding a model is adding an entry. A gateway with its own certificate should be one entry too, and not an entry plus a change to the machine.
- A setting would be one value for the install. Two gateways with two authorities, or two tenants with one each, need two values. The entry is already per model and per tenant.
- The usual way round a private certificate is to switch the check off. That makes every call to the gateway readable by whoever sits between, and the gateway is where sensitive content is allowed to go. Naming the authority costs one field and keeps the check.
- Leaving the usual authorities out of the pool means a certificate from a public authority for the same name is not accepted for this entry. The owner said which authority signs the gateway, and that is the only one believed.
- A path as well as text, because a certificate that is renewed on disk should not need the entry rewritten, and because PEM text is awkward to paste into a form. Text as well as a path, because a registry entry that holds everything can be copied to another machine.
- A certificate is public. It can sit in the entry, in the database and in a backup. A private key cannot, so the registry refuses one by name before it is stored.

## What it costs

- `internal/models`, a guard for rule 3, has one more field and one more check. The check reads no file and no setting, and the field cannot change a model's class.
- A path is read when the provider is built, which is on every `Open`. A file that has gone missing shows up then, as a refusal that names the path, and not when the entry is stored.
- An entry with `ca_cert` cannot also reach an endpoint signed by a public authority. It never needs to: one entry has one endpoint.
- The registry cannot tell that PEM text is a real certificate. The adapter can, and says so when the model is opened.
