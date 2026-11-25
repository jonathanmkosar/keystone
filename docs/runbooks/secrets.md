# Storing a tenant's secrets

A secret is a provider key or a connector token. How they are kept is in [`docs/architecture/secrets.md`](../architecture/secrets.md).

## Store one

Copy the value, then pipe it in. It is read from standard input, so it does not stay in the shell's history, and it is never printed.

    Get-Clipboard | go run ./tools/tenant secret set <tenant-id> provider.name

The tenant id comes from `go run ./tools/tenant list`. A name is lower-case letters, digits, dots, dashes and underscores.

## See what is stored

    go run ./tools/tenant secret list <tenant-id>

This prints names only. No command prints a value.

## Remove one

    go run ./tools/tenant secret delete <tenant-id> provider.name

## A machine with no key store

If the command says the system offers no key store, the install can keep its master key in a file in your profile folder. This is weaker: anyone who can read that file and the data folder can read every secret. To choose it for a new install, pass the keeper once, on the first secret:

    Get-Clipboard | go run ./tools/tenant --keeper file secret set <tenant-id> provider.name

`keystone.json` then records the choice, and later commands need no flag.

## After a restore, or on another machine

The data folder alone cannot be read. The master key lives in the key store of the account that first stored a secret.

- **Same machine, same account:** nothing to do.
- **Another machine or another account:** the secrets cannot be opened. Delete each tenant's `secrets.bin` and the `master_key` field in `keystone.json`, then store the keys again. Chats and files are not affected.
- **The `file` keeper:** copy `keystone/master.key` from the old profile folder to the same place in the new one, and the secrets open.

A `secrets.bin` that cannot be opened is never replaced by Keystone. It stays as it is until you remove it.

## A Tavily key for web search

`web_search` uses each person's own Tavily key, read from their vault on every search under the name `provider.tavily` ([web-search.md](../architecture/web-search.md)). Without one, a search sends nothing, and the step in the chat says "not set up".

1. Copy the key from your Tavily account. It starts with `tvly-`.
2. Store it for the person, from the repo's folder:

        Get-Clipboard | go run ./tools/tenant secret set <tenant-id> provider.tavily

   Or, signed in as that person, open Settings, then Models and keys, and add a key named `provider.tavily`.
3. Check that it is there, by name only:

        go run ./tools/tenant secret list <tenant-id>

The next search uses it; the server does not need a restart. A key Tavily refuses gives the step "failed" with "Tavily refused the key stored as provider.tavily" in its open row; store the key again. To turn web search off for a person, remove the key:

    go run ./tools/tenant secret delete <tenant-id> provider.tavily

Where searches go, how deep they are and how many results come back are install settings in `settings/agents.json` (`agents.web_search_*`). `web_fetch`, which reads one page, needs no key.
