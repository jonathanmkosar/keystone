# 0261. A backup carries the tenant's data key re-wrapped, and a restore wraps it again under the new install's keeper

November 25, 2026 · decided in P13-04

## The question

A tenant's secrets are sealed with its data key, and the data key is wrapped by the install master key that the operating system holds (decision 0006). DPAPI ties that key to one Windows account on one machine, so `secrets.bin` copied to another machine cannot be opened, and the secrets runbook said to enter every key again there. How does a backup bring the keys along so that they work after a restore on another machine?

## The decision

**The data key is wrapped again, and the secrets themselves are not re-encrypted.** `internal/secrets` gains `Rewrap(file, tenant, from, to)`: it unwraps the data key with one keeper, opens the secrets to check them, wraps the same data key with another keeper, and returns the file. `Vault.Rewrapped(to)` does it for a vault's own file under its lock.

- **A backup** re-wraps the data key with a keeper whose key comes from the backup's passphrase (`secrets.KeyKeeper`). The file in the backup opens with the passphrase and nothing from the machine it came from.
- **A restore** checks that file with the backup's keeper, then re-wraps the data key with the new install's keeper: the one `keystone.json` records, or the one `secrets.keeper` names for a new install. It then opens the restored vault under that keeper before the tenant is put in place.

The data key never leaves the secrets package unwrapped, and no other package handles a key. The wrap stays bound to the tenant's id, so the restored tenant keeps its id.

## Why

- The plan's line was that a copied data folder gives nothing away. A backup sealed under a passphrase keeps that true for the file, and gives the person a way to move on purpose, which decision 0006 named as the missing piece.
- Re-wrapping the data key is the one step that changes. Opening every secret and storing it again under a new data key would be more code with a value in hand, and would fail halfway in more ways.
- Doing it inside `internal/secrets` keeps decision 0006's promise that only that package sees a key.

## What it costs

- This changes the first cost in decision 0006: a tenant restored from a backup can read its secrets on another machine. A copy of the data folder alone still cannot.
- The data key is the same before and after a restore. Someone who once had the data key, from the old machine's key store and data folder together, could read secrets stored later on the new machine. Nothing rotates a data key yet (secrets.md, "Not built yet").
- On a new install, the first restore with secrets is what makes the master key and records the keeper in `keystone.json`, as the first stored secret does. If the restore fails after that step, the record stays. It names a keeper and holds no key.
