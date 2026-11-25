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
| Profile and map | `internal/knowledge/personmap` | The two short texts about a person that start every call: a profile in their own words, and a map of what their knowledge store holds, made from its catalog, held to a token limit and built again when the catalog changes. The map for a model that may not read sensitive content leaves every sensitive space out. Nothing calls it yet. | [knowledge-profile-and-map.md](knowledge-profile-and-map.md) |
| Context builder | `internal/contextbuild` | Builds what one model call carries: the standing instructions, the current turn and as many earlier turns as fit the model's window, newest first and each one whole, with named slots for the parts later items bring. Returns a record of every part, kept and dropped, with its tokens, which the turn stores for the inspector. | [context.md](context.md) |
| Routing | `internal/routing` | Decides which models one call may be sent to: a rule for each kind of task, sensitive content kept to local and trusted models, and a fallback chain that moves to the next model while nothing of a reply has been passed on. A guard for fixed rule 3. | [routing.md](routing.md) |
| Usage and the spend cap | `internal/usage` | One append-only record for each model call, with its tokens and its cost, summed by month for a tenant. The monthly cap stops a call to a model that is not local when it could take the month past the cap, and warns near it. | [usage.md](usage.md) |
| Settings routes and screens | `internal/settingsapi`, `web/app/src` | The routes behind the settings screens, and the screens: a person's own settings, the names of their secrets, their model registry and their profile. A secret's value goes in and is never returned, and a fixed rule is listed and never changed. | [settings-screens.md](settings-screens.md) |
| Tool loop | `internal/tools`, `internal/tools/approval`, `internal/tools/builtin` | The registry of the tools a turn offers, filled by plain calls, and the one place a tool runs: recorded before and after, under a timeout, with a tool that breaks turned into an error result. The approval rule's test refuses every tool that changes something until approvals exist. Three built-in tools that only read: the time, search of the person's chats, and reading one. The turn calls the model again after each step, up to its step limit. | [tool-loop.md](tool-loop.md) |
| Web search and fetch | `internal/tools/web`, `internal/tools/outside.go` | `web_search`, which asks Tavily with the person's own key from their vault and returns numbered results, and `web_fetch`, which reads one public page and refuses every address on this machine or a private network, as it connects and on every redirect. Both only read. Every result is marked as outside content, so the approval policy can tell a turn that read some, and a reply's [n] links to its source on the chat screen. | [web-search.md](web-search.md) |
| Recall | `internal/knowledge/recall` | Searches the person's knowledge store with each message, keeps the hits over a threshold set for each adapter, and gives each call the pages and sources found, each saying where it came from, with a record of what was kept and dropped. Three lookups for the model. A guard for fixed rule 3: a call that is given sensitive content is routed only to models that may read it, and a chat that was sent some stays held to them. The lookups wait for the tool loop to offer them. | [knowledge-recall.md](knowledge-recall.md) |
| Compaction | `internal/compact` | Keeps a long chat inside its model's window: past a share of the room for earlier turns, the oldest are summarised beside the turn by the model routing gives the summary task, extending the summary before, and later calls carry the summary in their place. Keeps the summaries with the chat, and the messages a person pinned are never left out. A guard for fixed rule 3: the summary of a held chat is made and sent only where the chat may go. | [compaction.md](compaction.md) |
| Approvals | `internal/tools/approval`, `internal/turn` (`approvals.go`, `policies.go`, `approvalroutes.go`), `web/app/src` | The approval policy, fixed rule 2: each person's policy for each tool (always, ask or never), a card in the chat for a call that asks, with the turn waiting until the person answers, also across a restart, and the rule that once a chat has read outside content every tool that changes something asks, whatever its policy. An approval covers one call and is used once. | [approvals.md](approvals.md) |
| Backup and restore | `internal/backup`, `internal/tenant` (`backup.go`), `internal/secrets` (`rewrap.go`), `tools/tenant` | One tenant in one file, sealed under a passphrase with PBKDF2-SHA-256 and AES-256-GCM in chunks a cut or changed file cannot pass, and restored into a data folder on this machine or another with its data key wrapped again under that install's keeper. A restore checks the whole file before writing anything, refuses a tenant that is there unless told to replace it, and puts the tenant in place with one rename. Made by hand with `tenant backup` and `tenant restore`; no schedule yet. | [backup.md](backup.md) |

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
| 0180 | [The profile is one text, and it goes to every model](decisions/0180-the-profile-is-one-text.md) |
| 0181 | [The map has two versions, and the open one names nothing from a sensitive space](decisions/0181-the-map-has-two-versions.md) |
| 0182 | [The map is kept in the tenant's database under a key, and is not used when the store is down](decisions/0182-the-map-is-kept-and-keyed.md) |
| 0183 | [The map is held to a token limit by giving up detail in a fixed order](decisions/0183-the-map-gives-up-detail-in-a-fixed-order.md) |
| 0160 | [A call falls back on the endpoint's failures, on its window and on the cap, and never on the request's own](decisions/0160-a-capped-call-falls-back-to-a-local-model.md) |
| 0161 | [The cap stops a call that could pass it, counted at the most the call could cost](decisions/0161-the-cap-stops-a-call-that-could-pass-it.md) |
| 0162 | [The cap counts a calendar month in UTC, and every model that is not local](decisions/0162-the-cap-counts-a-utc-calendar-month.md) |
| 0163 | [A route is one provider, and the turn's model stays the one that was asked for](decisions/0163-a-route-is-one-provider.md) |
| 0190 | [The settings routes act as the person, in a package of their own](decisions/0190-settings-routes-act-as-the-person.md) |
| 0191 | [The General screen is drawn from the settings route, and the proof is a test of the route](decisions/0191-the-general-screen-is-drawn-from-the-route.md) |
| 0192 | [The theme is a setting once a person has chosen one](decisions/0192-the-theme-is-a-setting-once-chosen.md) |
| 0200 | [A tool is data and a function in a registry made by plain calls, and the registry is the one place a tool runs](decisions/0200-a-tool-is-data-and-a-function-in-a-registry.md) |
| 0201 | [Until approvals exist, a tool that changes something is refused, and an approval is a value only its own package can make](decisions/0201-a-changing-tool-is-refused-until-approvals.md) |
| 0202 | [At the step limit a turn ends as done with a note, and the last calls are answered without running](decisions/0202-the-step-limit-ends-a-turn-with-a-note.md) |
| 0203 | [A tool runs beside the turn, stop does not wait for it, and every call still gets a stored result](decisions/0203-a-tool-runs-beside-the-turn-and-stop-does-not-wait.md) |
| 0204 | [The chat screen writes each step's readable line from the tool's name, input and result](decisions/0204-the-screen-writes-the-steps-line.md) |
| 0210 | [Recall searches once, before the route, and a call is built for the least of its route](decisions/0210-recall-searches-once-before-the-route.md) |
| 0211 | [A chat that was sent sensitive content is held to models that may read it](decisions/0211-a-chat-sent-sensitive-content-is-held.md) |
| 0212 | [Recall searches with the message's words, keeps by a threshold for each adapter, and fills each kind of model's room](decisions/0212-what-recall-searches-with-and-keeps.md) |
| 0213 | [The lookups are plain values the tool loop wraps, and take the model's answer like recall](decisions/0213-the-lookups-are-plain-values.md) |
| 0214 | [The search-first rule is a paragraph of the standing instructions, sent when a store is open](decisions/0214-the-search-first-rule-is-a-standing-instruction.md) |
| 0240 | [A picked model goes with the next message, and the server keeps it for the chat](decisions/0240-a-pick-goes-with-the-next-message.md) |
| 0241 | [The picker learns that a chat is held from a refused turn, and keeps that in the browser](decisions/0241-a-held-chat-is-learned-from-a-refused-turn.md) |
| 0230 | [A summary is made beside the turn, and the call goes with what is stored](decisions/0230-a-summary-is-made-beside-the-turn.md) |
| 0231 | [A summary extends the one before it, and stands for the start of one branch](decisions/0231-a-summary-extends-the-one-before.md) |
| 0232 | [A chat is summarised past a trigger share, down to a keep share, and whenever a turn is left out](decisions/0232-when-a-chat-is-summarised.md) |
| 0233 | [A pinned message is held its room, and sent on its own only when its turn is not](decisions/0233-a-pinned-message-is-sent-on-its-own-only-when-its-turn-is-not.md) |
| 0234 | [The summary of a held chat is sensitive, and goes only where the chat may go](decisions/0234-a-held-chats-summary-is-sensitive.md) |
| 0220 | [The rule decides each call from values it is given, and an approval covers one call and is used once](decisions/0220-the-rule-decides-each-call-and-an-approval-covers-one-call.md) |
| 0221 | [Outside content marks the chat, a tool brings it unless it says otherwise, and a knowledge source counts while a page does not](decisions/0221-outside-content-marks-the-chat.md) |
| 0222 | [A step is decided whole before any call runs, and the turn waits in place, with the wait kept in the database](decisions/0222-a-step-is-decided-whole-and-the-wait-survives-a-restart.md) |
| 0223 | [The person's answer is in the route's path, and each tool's policy is a table, not a setting](decisions/0223-the-answer-is-in-the-path-and-the-policy-is-a-table.md) |
| 0224 | [The approval card is drawn from approval events kept outside the screens, and forgotten when the turn ends](decisions/0224-the-card-is-drawn-from-approval-events-kept-outside-the-screens.md) |
| 0250 | [Citations are numbered through a turn, in the result's own text, and the screen reads them back](decisions/0250-citations-are-numbered-through-a-turn-in-the-result-text.md) |
| 0251 | [web_fetch checks the address it connects to, after resolving the name and on every redirect](decisions/0251-web-fetch-checks-the-address-it-connects-to.md) |
| 0252 | [The web tools are off in a chat held for sensitive content](decisions/0252-web-tools-are-off-in-a-chat-held-for-sensitive-content.md) |
| 0253 | [Outside content is marked in the tool's result, where the record keeps it](decisions/0253-outside-content-is-marked-in-the-result.md) |
| 0260 | [A backup is one file sealed under a passphrase, with PBKDF2 from the standard library](decisions/0260-a-backup-is-one-file-sealed-under-a-passphrase.md) |
| 0261 | [A backup carries the tenant's data key re-wrapped, and a restore wraps it again under the new install's keeper](decisions/0261-a-backup-carries-the-data-key-re-wrapped.md) |
| 0262 | [A restore checks the whole file first, builds the tenant beside the others, and ends its old sessions](decisions/0262-a-restore-is-all-or-nothing.md) |
| 0263 | [Scheduled backups wait for a key no one has to type, and for background jobs](decisions/0263-scheduled-backups-wait.md) |

`verify` fails if a record in `decisions/` is not linked here.
