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
| Built-in knowledge store | `internal/knowledge/builtin` | The knowledge store Keystone keeps by itself, in the tenant's own database: pages, sources, labels, a full-text index and page vectors. It passes the contract suite with every capability. The server does not open it yet. | [knowledge-builtin.md](knowledge-builtin.md) |
| Waymarks adapter | `internal/knowledge/waymarks` | The knowledge store adapter for Waymarks: an HTTP client of one instance for each tenant, with the address in that tenant's settings and one token for each role in that tenant's secrets. It passes the contract suite against a fake Waymarks and has not met a real one. The server does not open it yet. | [knowledge-waymarks.md](knowledge-waymarks.md) |
| Web interface | `web` | The built interface, compiled into the binary. A placeholder page until P4-01. | [web.md](web.md) |
| Web interface | `web` | The web shell, built with React and Vite and compiled into the binary: the frame, the theme switch, routing and the sign-in screen. | [web.md](web.md) |
| Settings registry | `settings` | Every value someone may change: its default, its type, and who may change it. | [settings.md](settings.md) |
| Model seam | `internal/seam` | Keystone's own format for a model call and its streamed reply: content blocks, stream events and usage. The format only, so far. | [seam.md](seam.md) |
| Chat record | `internal/chat` | Chats, their messages as a tree of branches, titles, stars, search over titles and text, and deletion with a chat's files. Functions only, until the API. | [chat.md](chat.md) |
| Fake provider | `internal/adapters/fake` | A scripted model for the tests, `verify` and the test tenant. It plays fixture scripts through the seam's `Provider` interface: text, thinking, tool calls and failures, with no network and no keys. | [fake-provider.md](fake-provider.md) |
| Project helpers | `internal/project` | Finds the repo root, runs commands, reads the plan's items and `STATE.md`. Used by the scripts only. | [project.md](project.md) |
| Project scripts | `tools` | `rebuild`, `verify`, `doctor` and `snapshot`. | [tools.md](tools.md) |
| Audit log | `internal/audit` | An append-only log in each tenant's database, kept free of keys, and the wrapper that records every API call before it runs. | [audit.md](audit.md) |
| Tenant settings | `internal/tenantsettings` | A tenant's own values over the registry's defaults, limited by each setting's scope. The fixed rules cannot be stored. | [tenant-settings.md](tenant-settings.md) |
| Model registry | `internal/models` | Each tenant's list of models: adapter, endpoint, context window, output limit, capabilities, price, and whether it is local, hosted or trusted. Refuses an entry that is not valid, with the reason. Guards fixed rule 3. | [models.md](models.md) |
| Adapters | `internal/adapters` | Turns a registry entry into a provider that can be called, with the key read by name from the tenant's vault. The fake is the only adapter built. | [models.md](models.md) |
| Anthropic adapter | `internal/adapters/anthropic` | Speaks the Anthropic Messages API over HTTP with the standard library: streaming, tools, thinking, cache marks, token counting, errors as the seam's kinds, and retries. The entry gives the address, so it serves Ollama and LiteLLM as well. Proven on recorded exchanges, and not yet against the real API. | [adapter-anthropic.md](adapter-anthropic.md) |
| Test tenant | `internal/testtenant`, `fixtures/test-tenant` | An invented person with a published password, a few chats and recorded model turns, loaded by `rebuild` and safe to load twice. It is told from a real tenant by a mark in its own database. | [test-tenant.md](test-tenant.md) |
| API shell | `internal/api`, `internal/app` | The HTTP API every client uses. Every route but sign-in sits inside one audit wrapper, gets its tenant's store from the session cookie, and is listed in the OpenAPI document. Server-sent events for streams. | [api.md](api.md) |
| Token budget | `internal/budget` | Counts the tokens a call will use, with the provider's own count where it offers one and an estimate with a margin elsewhere, and refuses a call that cannot fit the model's window before it is sent. | [budget.md](budget.md) |
| Responses adapter | `internal/adapters/responses` | Calls a model that speaks the OpenAI Responses API: streaming, function calls, reasoning, errors sorted into the seam's kinds, and retries. Proven on replayed exchanges; not yet checked against the real API. | [adapter-responses.md](adapter-responses.md) |
| Chat Completions adapter | `internal/adapters/chatcompletions` | Calls any endpoint that speaks the OpenAI-style Chat Completions format: OpenRouter, LiteLLM, Ollama and the Mac mini gateway. Streaming, tool calls, errors mapped to the seam's kinds, retries, and a certificate authority an entry may trust for itself. Proven on recorded exchanges; no live call yet. | [adapter-chat-completions.md](adapter-chat-completions.md) |
| Chats and turns | `internal/turn` | The routes a chat screen calls, and the runner behind them. A turn is a row in the tenant's database: it takes the person's message through the model seam, streams the reply as server-sent events a client can resume, can be stopped, waits its turn in a chat's queue, and is finished after a restart. The test tenant always gets the fake provider. | [turns.md](turns.md) |
| Context builder | `internal/contextbuild` | Builds what one model call carries: the standing instructions, the current turn and as many earlier turns as fit the model's window, newest first and each one whole, with named slots for the parts later items bring. Returns a record of every part, kept and dropped, with its tokens, which the turn stores for the inspector. | [context.md](context.md) |

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
| 0060 | [The built-in knowledge store keeps its pages in the tenant's database](decisions/0060-built-in-store-in-the-tenant-database.md) |
| 0061 | [A built-in search matches any word, with stemming, and adds a cosine to the word score](decisions/0061-built-in-search-any-word-and-cosine.md) |
| 0062 | [The built-in store takes a vector of any model and size, and compares them in Go](decisions/0062-built-in-vectors-any-model.md) |
| 0050 | [A model is a row in the tenant's database, holding the entry as checked JSON](decisions/0050-a-model-is-a-row-of-checked-json.md) |
| 0051 | [The registry is a guard for rule 3, and `local` is checked against the address](decisions/0051-the-registry-guards-rule-three.md) |
| 0052 | [An adapter's builder is passed in, and its key comes from the vault by name](decisions/0052-builders-are-passed-in.md) |
| 0100 | [The Anthropic adapter places the cache marks itself, until the seam has a marker](decisions/0100-the-anthropic-adapter-places-cache-marks.md) |
| 0101 | [The Anthropic adapter asks for adaptive thinking, and for a token budget only where a setting names the model](decisions/0101-thinking-form-by-model-name.md) |
| 0102 | [The Anthropic adapter retries a call only before it has passed anything on, and never follows a redirect](decisions/0102-an-adapter-retries-only-before-the-first-event.md) |
| 0070 | [The design tokens become CSS variables when the interface is built](decisions/0070-design-tokens-become-css-at-build-time.md) |
| 0071 | [Screens in the interface have real paths, and the server falls back to the page](decisions/0071-client-side-routes-are-real-paths.md) |
| 0072 | [The interface names the design's fonts and does not bundle them yet](decisions/0072-fonts-are-named-not-bundled.md) |
| 0080 | [The test tenant is marked in its own database, and its password is published](decisions/0080-the-test-tenant-is-marked-in-its-own-database.md) |
| 0040 | [Every route sits inside one audit wrapper, and the doors are a short list](decisions/0040-one-wrap-and-a-short-list-of-doors.md) |
| 0041 | [The session travels in one cookie, for the browser and the command line alike](decisions/0041-the-session-travels-in-one-cookie.md) |
| 0042 | [The OpenAPI document is built from the routes, and a copy is committed](decisions/0042-the-openapi-document-is-built-from-the-routes.md) |
| 0130 | [A provider counts tokens through an optional interface, declared by the budget](decisions/0130-a-provider-counts-through-an-optional-interface.md) |
| 0131 | [The estimate counts characters, and leans high](decisions/0131-the-estimate-counts-characters.md) |
| 0110 | [The Responses adapter stores nothing at the provider, and reasoning travels in the signature](decisions/0110-responses-stores-nothing-at-the-provider.md) |
| 0111 | [For the Responses adapter a thinking budget becomes an effort, by two settings](decisions/0111-a-thinking-budget-becomes-an-effort.md) |
| 0112 | [The Responses adapter retries a call only before any of its reply has been passed on](decisions/0112-a-call-is-retried-only-before-its-reply-begins.md) |
| 0090 | [The Waymarks adapter holds three tokens, and its capabilities follow them](decisions/0090-waymarks-three-tokens.md) |
| 0091 | [The Waymarks catalog marker is a hash of the catalog and the labels](decisions/0091-waymarks-marker-is-a-hash-of-the-catalog.md) |
| 0092 | [A Waymarks label comes from the answer's own mark, and from the list of spaces read afterwards](decisions/0092-waymarks-labels-from-the-answer.md) |
| 0093 | [What each Waymarks refusal becomes](decisions/0093-waymarks-refusals.md) |
| 0120 | [An entry may trust one certificate authority of its own, and nothing turns the check off](decisions/0120-an-entry-trusts-one-authority.md) |
| 0121 | [The Chat Completions adapter holds tool calls until the reply ends](decisions/0121-tool-calls-are-held-until-the-reply-ends.md) |
| 0122 | [An adapter retries only until the first event, calls a local model directly, and follows no redirect](decisions/0122-retries-stop-at-the-first-event.md) |
| 0140 | [A turn is a row, its events are rows, and a stream reads the table](decisions/0140-a-turn-and-its-events-are-rows.md) |
| 0141 | [The turn runner opens a store of its own, for the tenant of the call](decisions/0141-the-runner-opens-its-own-store.md) |
| 0142 | [After a restart a queued turn runs, and a turn that was running is closed as failed](decisions/0142-an-interrupted-turn-fails-and-a-queued-one-runs.md) |
| 0143 | [Until the tool loop, a tool call is answered with an error and the model is called again](decisions/0143-a-tool-call-is-answered-with-an-error-for-now.md) |
| 0144 | [The test tenant's model is in no registry, and the fake provider's titles come from the message](decisions/0144-the-test-tenants-model-is-in-no-registry.md) |
| 0170 | [A turn is kept whole or left out, and nothing older is sent past a gap](decisions/0170-a-turn-is-kept-whole-or-left-out.md) |
| 0171 | [Recall and the summary hold a share of the room ahead of earlier turns](decisions/0171-recall-and-the-summary-hold-a-share.md) |
| 0172 | [The builder packs by the estimate and asks the provider to count the result](decisions/0172-the-builder-packs-by-estimate-and-asks-the-provider-once.md) |
| 0173 | [What a call was built from is a row beside the turn, not an event in its stream](decisions/0173-the-context-record-is-a-row-beside-the-turn.md) |
| 0150 | [The Markdown reader is written in the interface, not taken from a package](decisions/0150-the-markdown-reader-is-written-here.md) |
| 0151 | [Replies that are arriving live outside the screens, and are read with fetch](decisions/0151-arriving-replies-live-outside-the-screens.md) |

`verify` fails if a record in `decisions/` is not linked here.
