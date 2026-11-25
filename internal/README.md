# Inside Keystone

One folder per part. Each lane owns its own folders, listed in `docs/plan/PARALLEL.md`, so several agents can work at once.

| Folder | Lane | Holds |
| --- | --- | --- |
| `project` | core | Helpers for the project scripts in `tools/` |
| `tenant` | core | The tenant registry, each tenant's database and folders, and the migrations |
| `signin` | core | Passwords, passkeys and sessions, kept in each tenant's database |
| `secrets` | core | Each tenant's `secrets.bin`, and the keepers of the install master key |
| `audit` | core | The append-only audit log and the wrapper that records every API call |
| `tenantsettings` | core | A tenant's own values over the registry's defaults |
| `api` | core | The API shell: what a route is, the session cookie, errors, server-sent events and the OpenAPI document |
| `app` | core | The one list of every route Keystone serves. A lane that brings routes adds them here |
| `seam` | models | Keystone's own format for a model call, and the `Provider` interface |
| `adapters/fake` | models | The scripted model used by the tests and `verify` |
| `chat` | chats | Chats, messages as a tree of branches, and search |
| `knowledge` | knowledge | The knowledge store interface, the `none` adapter and the contract suite |
| `knowledge/builtin` | knowledge | The built-in knowledge store, kept in the tenant's own database |
| `models` | models | Each tenant's model registry, and the classes fixed rule 3 reads |
| `adapters` | models | Turns a registry entry into a provider, with its key from the vault |
| `testtenant` | core | Loads the invented test tenant from `fixtures/test-tenant` |
| `budget` | models | Counts a call's tokens and refuses one that cannot fit the model's window |
