# 0683. A changed key is seen by a fingerprint kept with the refused state

November 27, 2026 · decided in P7-06

## The question

A provider whose key was refused stays `refused` until "the key is changed", with no soft tests. A key can be changed in several ways that know nothing of search: `tools/tenant secret set`, the Models and keys screen, a restore. How does Keystone see the change?

## The decision

When a key is refused, the state keeps a fingerprint of it: the first 8 bytes of SHA-256 over a fixed label and the key, as hex. Each search, and the screens, compare the fingerprint of the key in the vault now; a different one puts the provider back to `ready`.

## Why

- **It works however the key was stored**, with no hook in the vault or in the routes that store secrets.
- **It is not the key.** Search keys are long random strings; 64 bits of a salted hash cannot be turned back into one, and `no keys in output` looks for the key, its hex and its base64 in every file and finds none.

## What it costs

- A hash of a key is in the database. A person who stores a weak, guessable value as a key could have it confirmed by someone who has both the database and a guess.
