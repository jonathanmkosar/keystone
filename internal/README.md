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
| `adapters/anthropic` | models | The adapter for the Anthropic Messages API, and for gateways that speak it |
| `testtenant` | core | Loads the invented test tenant from `fixtures/test-tenant` |
| `budget` | models | Counts a call's tokens and refuses one that cannot fit the model's window |
| `adapters/responses` | models | The adapter for the OpenAI Responses API, over HTTP with the standard library |
| `knowledge/waymarks` | knowledge | The Waymarks adapter: a client of one Waymarks instance for each tenant |
| `adapters/chatcompletions` | models | The adapter for OpenRouter, LiteLLM, Ollama and the Mac mini gateway |
| `turn` | chats | Runs a turn: the message, the model call, the stream, stop and restart |
| `knowledge/personmap` | knowledge | The person's profile and the map of their knowledge store, in the two versions fixed rule 3 needs |
| `contextbuild` | chats | Builds what one model call carries, under the model's window, and records every part of it |
| `routing` | models | Which models a call may go to: task rules, the privacy filter and the fallback chain |
| `usage` | models | A record for each model call, a month's sums, and the monthly spend cap |
| `settingsapi` | interface | The routes behind the settings screens: settings, secret names, the model registry and the profile |
| `tools` | agents | The tool registry and the one place a tool runs; `tools/approval`, the test for fixed rule 2; `tools/builtin`, the tools that come with Keystone |
| `tools/web` | agents | `web_search` with Tavily and `web_fetch` of public pages, numbered sources, and results marked as outside content |
| `mcp` | agents | A Model Context Protocol client, standard library only: initialize, list tools and call one, over stdio and the streamable HTTP transport; `mcp/mcptest`, a test server for tests and verify |
| `connectors` | agents | Each person's connectors, what a turn is offered from them by name until loaded, the pool of sessions, the address and command rules, and the routes |
| `google` | agents | The Google OAuth client (PKCE, refresh, revoke) and the Gmail and Calendar tools as plain data and functions, for the built-in connectors; `google/googletest`, a stand-in Google for tests and verify |
| `knowledge/recall` | knowledge | Recall on every message, the three knowledge lookups, and the sensitive mark a chat keeps |
| `backup` | core | The sealed file one tenant's backup is written to: the passphrase's key, chunks a cut or changed file cannot pass, and names that cannot leave the folder |
| `compact` | chats | When a long chat's oldest turns are summarised and which, the summary request, the stored summaries, and whether one may go to a call |
| `knowledge/memory` | knowledge | The memory pass: the request for an exchange's durable facts, reading the answer, where the facts may land, the queue of passes, the incognito flag and the memory reserve |
| `inspector` | chats | The turn inspector's calls and totals from a turn's records, and what a person takes out of a chat's next turn |
| `jobs` | agents | Background tasks: rounds of turns in a chat of their own, resumed after a restart, with a budget, the task routes, `start_background_task`, and the note of the result |
| `recap` | chats | Each chat's recap (a summary, its topics and decisions): the request and the answer, when a chat is due, keeping and searching recaps, and fixed rule 3's answer for past chats |
| `schedules` | agents | Scheduled tasks: schedules in the person's time zone, the scheduler that starts each run as a background task in a chat of its own, a missed run once, the pause switch, and the schedule routes |
