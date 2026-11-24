# 0006. Secrets are encrypted per tenant, under a master key the operating system holds

November 24, 2026 · decided in P0-03

## The question

Provider keys and connector tokens have to be stored somewhere. How are they kept so that a copy of the data folder, or of the repo, gives nothing away?

## The decision

Three layers.

1. Each tenant has its own data key. The tenant's secrets are encrypted with it and stored in `secrets.bin` in the tenant's folder.
2. Each tenant's data key is itself encrypted ("wrapped") by one install master key.
3. The install master key is held by the operating system and is never written to the data folder. On Windows, the first target, that is DPAPI. `keystone.json` holds a reference to the key, not the key.

Secrets are read on the server at the moment a call needs them. The browser never receives one, and none appears in a log, the audit log, an API response or the journal. `verify` scans for both.

The keeper for macOS and Linux, and what an install does where the system offers none, are chosen in P2-03 and recorded there. The code reaches the keeper through one small interface, so adding a system is one file.

## Why

- A copied or backed-up data folder holds only encrypted secrets. Without the machine's own key store they cannot be read.
- One tenant's data key does not open another tenant's secrets.
- Nothing secret is ever in the repo, so the repo can be public.

## What it costs

- A tenant folder restored on another machine cannot read its secrets. The restore runbook says to enter the keys again, or to move the master key on purpose. The chats and files restore as they are.
- DPAPI ties the master key to the Windows account Keystone runs as. Running Keystone as a different account means moving the key.
- Tests cannot use the real keeper. They use an in-memory one, and the Windows keeper has its own test that runs on Windows only.
