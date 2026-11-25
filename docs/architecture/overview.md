# How Keystone is built

The design is in [`docs/plan/PLAN.md`](../plan/PLAN.md). This page says what exists in the code today and where each part is described. It grows with each work item.

## Parts that exist

| Part | Folder | What it does | Doc |
| --- | --- | --- | --- |
| Command line | `cmd/keystone` | The binary. Serves the embedded web page, and prints the settings registry and the version. | [cli.md](cli.md) |
| Tenants | `internal/tenant` | The tenant registry in `keystone.json`, and a store handle that reaches one tenant's database and folders and nothing else. | [tenants.md](tenants.md) |
| Sign-in | `internal/signin` | Passwords, passkeys and sessions, kept in each tenant's own database. Turns a session token into the one tenant's store. No routes yet. | [signin.md](signin.md) |
| Secrets | `internal/secrets` | A tenant's provider keys and connector tokens in `secrets.bin`, encrypted under that tenant's data key, which is wrapped by a master key the operating system holds. | [secrets.md](secrets.md) |
| Knowledge store | `internal/knowledge` | The interface every knowledge store is written to, the tests any store must pass, and the store that holds nothing, so a tenant can run with none. | [knowledge.md](knowledge.md) |
| Web interface | `web` | The web shell, built with React and Vite and compiled into the binary: the frame, the theme switch, routing and the sign-in screen. | [web.md](web.md) |
| Settings registry | `settings` | Every value someone may change: its default, its type, and who may change it. | [settings.md](settings.md) |
| Model seam | `internal/seam` | Keystone's own format for a model call and its streamed reply: content blocks, stream events and usage. The format only, so far. | [seam.md](seam.md) |
| Chat record | `internal/chat` | Chats, their messages as a tree of branches, titles, stars, search over titles and text, and deletion with a chat's files. Functions only, until the API. | [chat.md](chat.md) |
| Fake provider | `internal/adapters/fake` | A scripted model for the tests, `verify` and the test tenant. It plays fixture scripts through the seam's `Provider` interface: text, thinking, tool calls and failures, with no network and no keys. | [fake-provider.md](fake-provider.md) |
| Project helpers | `internal/project` | Finds the repo root, runs commands, reads the plan's items and `STATE.md`. Used by the scripts only. | [project.md](project.md) |
| Project scripts | `tools` | `rebuild`, `verify`, `doctor` and `snapshot`. | [tools.md](tools.md) |
| Audit log | `internal/audit` | An append-only log in each tenant's database, kept free of keys, and the wrapper that records every API call before it runs. | [audit.md](audit.md) |
| Tenant settings | `internal/tenantsettings` | A tenant's own values over the registry's defaults, limited by each setting's scope. The fixed rules cannot be stored. | [tenant-settings.md](tenant-settings.md) |

## Parts still to come

The API, the model registry and the real adapters, turns and context, a knowledge store that holds something, agents and tools, and the web interface itself. Each gets a row above and a doc of its own when its first item is built. `verify` fails if a folder holds Go code and has no doc.

## Decisions

Recorded in [`decisions/`](decisions/), one file each, numbered. A decision that changes the plan updates `PLAN.md` in the same commit.

| # | Decision |
| --- | --- |
| 0001 | [Who may change a setting is a scope with three values](decisions/0001-setting-scopes.md) |
| 0002 | [Keystone is written in Go and built to one binary](decisions/0002-go-one-binary.md) |
| 0003 | [One SQLite database for each tenant, through modernc.org/sqlite](decisions/0003-sqlite-per-tenant.md) |
| 0004 | [The API is HTTP with JSON, and server-sent events for streams](decisions/0004-http-json-sse.md) |
| 0005 | [The web interface is React and Vite, with its built files committed and embedded](decisions/0005-web-built-files-committed.md) |
| 0006 | [Secrets are encrypted per tenant, under a master key the operating system holds](decisions/0006-secrets-wrapped-by-the-os.md) |
| 0007 | [A session token names its tenant](decisions/0007-a-session-token-names-its-tenant.md) |
| 0008 | [Each operating system's key store is reached without an outside module, and a key file is used only when asked for](decisions/0008-keepers-for-each-system.md) |
| 0009 | [The audit log is append-only in the database, and an API call is recorded before it runs](decisions/0009-audit-log-append-only.md) |
| 0010 | [A content block is one struct with a type field](decisions/0010-one-block-type.md) |
| 0011 | [The fake provider finds its script from the request alone](decisions/0011-fake-scripts-matched-by-the-request.md) |
| 0012 | [Passkeys are checked with the standard library, without a CBOR reader](decisions/0012-passkeys-without-a-cbor-reader.md) |
| 0014 | [A fixed rule is a name no setting can have, enforced by code that cannot read settings](decisions/0014-fixed-rules-and-tenant-settings.md) |
| 0015 | [A provider streams through a callback](decisions/0015-a-provider-streams-through-a-callback.md) |
| 0020 | [Messages are a tree, and each parent remembers its current child](decisions/0020-messages-are-a-tree.md) |
| 0030 | [The knowledge store is one interface with capability flags, and having no store is an adapter](decisions/0030-knowledge-store-interface.md) |
| 0031 | [A label that is not `normal` reads as sensitive](decisions/0031-label-reads-as-sensitive.md) |
| 0070 | [The design tokens become CSS variables when the interface is built](decisions/0070-design-tokens-become-css-at-build-time.md) |
| 0071 | [Screens in the interface have real paths, and the server falls back to the page](decisions/0071-client-side-routes-are-real-paths.md) |
| 0072 | [The interface names the design's fonts and does not bundle them yet](decisions/0072-fonts-are-named-not-bundled.md) |

`verify` fails if a record in `decisions/` is not linked here.
