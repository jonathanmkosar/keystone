# State

Phase: 4, the first usable chat. Keystone runs in a browser: sign in, open a chat, send a message, read a streamed reply, with the fake provider. Phases 0, 2 and 3 are built; phase 1, design, waits on the owner.
Next: P8-01 background tasks (agents), P5-04 the turn inspector (chats), P6-06 the memory pass (knowledge). The owner is trying his first web search and the model picker on his own domain.
Blocked: nothing
Branch: `reboot` on github.com/jonathanmkosar/keystone, a public repo. Where the plan says `main`, read `reboot`. `AGENTS.md` is where any agent starts.

## Lanes

| Lane | In progress | Next |
| --- | --- | --- |
| core | nothing | P13-04, held until the app runs |
| models | nothing | P6-08 with knowledge, after P6-05 |
| chats | nothing | P5-04 the turn inspector, P5-05, P9-05 |
| knowledge | nothing | P6-06, the memory pass |
| agents | nothing | P8-01 background tasks, P7-04 connectors |
| interface | nothing | the model route reporting a held chat and missing keys (decisions 0240, 0241), P11-02 |
| design | P1-02 design system, P1-03 mark, P1-04 chat mockups | The other screens in copper and ivory, then P1-05 |

## In flight

Work claimed and not yet merged into `reboot`. `go run ./tools/doctor` shows the live state of each branch and worktree. Each lane branch is pushed to `origin` after every commit, so another machine can pick it up. To carry one on after a stop: "Resuming after a stop" in `docs/plan/LOOP.md`.

| Item | Lane | Branch |
| --- | --- | --- |
| P1-02, P1-03, P1-04, design | design | none: they wait on the owner's approval |

## Waiting on the owner

| Question or input | Recommended | Assumed meanwhile | Provisional because of it |
| --- | --- | --- | --- |
| Corrections on the first round of screens | | The drafts stand | P1-02, P1-04 |
| The mark on light pages: on a dark tile, or free-standing with a dark keystone | On a dark tile | On a dark tile | P1-03 |
| The wordmark: Fraunces or Art Deco capitals | Fraunces | Fraunces | P1-02 |
| Claude.ai captures, or go-ahead to capture | Go-ahead to capture | Layout from knowledge of Claude.ai | P1-01 |
| How the sign-in screen asks who is signing in. There are no user names (core 005) | The screen lists the people on the install and the person picks their own. Look again when phone access from outside is decided (P10-05) | The sign-in functions take the tenant's store, so either answer fits | Nothing built. It shapes the sign-in route in P2-05 and the screen in P4-01 |
| Should the built-in knowledge store's pages be files a person can open, or is the database enough (knowledge 002, decision 0060) | The database is the truth, and Keystone exports to and imports from a Markdown folder on request | Rows only, no export | P6-02 |
| Who may add, change and remove models in a person's registry: the admin only, or the person too (models 003) | The admin only. Marking a model trusted is a security choice. Each person still picks among their models and keeps their own keys | The registry functions take no role, so either answer fits | Nothing built. It shapes the registry routes and the models screen in P11-01 |
| The test tenant's password is published in the repo, so anyone who can reach sign-in can be the test tenant (core 009) | Keep it on the home network, and switch `test_tenant.load` off before phone access from outside (P10-05) | Loaded by default | Nothing |
| An OpenAI key, two model names (a cheap one and a cheap reasoning one), and a go-ahead to spend a few cents, for the live check of the Responses adapter (models 005) | Store the key with `tools/tenant secret set <tenant-id> provider.openai` | Tested against hand-written exchanges only | P3-05 |
| Should page vectors go to Waymarks under a third token, an embedder with read and page-write rights (knowledge 003, decision 0090) | Yes, so the reader cannot write and the intake token cannot read | The adapter takes an optional embedder token | P6-03, and the plan's line on Waymarks tokens |
| An empty Waymarks test instance and three tokens, for the live check of the Waymarks adapter (knowledge 003) | The owner starts one and stores the tokens with `tools/tenant secret set` | Tested against a fake Waymarks only | P6-03 |
| An Anthropic key, for the live check of the Anthropic adapter (models 004) | Store it with `tools/tenant secret set <tenant-id> provider.anthropic` | Tested against hand-written exchanges only | P3-04 |
| The Mac mini gateway's address, key and certificate authority file, and an OpenRouter key and model name, for the live checks of the Chat Completions adapter (models 006) | Keys go in a tenant's vault; the certificate is a path in the registry entry | Tested against hand-written exchanges only | P3-06 |
| Should a model's registry entry say how it thinks (adaptive or a token budget), and should the thinking control be a depth and not a token count (models 004) | Yes to both, decided with the live check | Adaptive by default, a budget for the models a setting names | Nothing built on it yet |
| Tool calls from Chat Completions endpoints are held until the reply ends, then sent whole (models 006, decision 0121) | Leave as is | Held | Nothing |
| A turn that was mid-reply when the server stopped: run it again, or close it as failed with a retry (chats 002, decision 0142) | Close it as failed and keep the words that arrived. Running it again pays for the call twice unasked | Closed as failed | Nothing |
| A message sent while a reply is streaming in the same chat: queue it or refuse it (chats 002) | Queue, up to five | Queued | Nothing |
| How a model call the person stopped is recorded in the audit log, which has no "stopped" outcome (chats 002) | As ok, with a stopped mark and the partial reply | As recommended | Nothing |
| Enter sends a message, and the greeting uses the person's whole name (interface 002) | Keep Enter to send. Use the whole name until the profile has a short one | As recommended | Nothing |
| Should a model that may not read sensitive content be told how many sensitive spaces were left out of the map (knowledge 004) | Yes, a number and no names | The line is there | Nothing |
| The profile has no label and goes to every model, hosted ones included (knowledge 004) | Yes, with a sentence on the editing screen saying so | One profile for every model | Nothing built. It shapes the profile screen |
| What the standing instructions say, and whether each person or only the admin may change them (chats 003) | Keep the two plain sentences, and let each person change their own | As recommended | Nothing |
| At the monthly cap, should a chat fall back to a local model in the person's chain, or always stop (models 008, decision 0160) | Fall back, and name the model that wrote the reply | Falls back; the chain ships empty | Nothing |
| Should a home-network model that forwards to a paid provider count against the cap (models 008, decision 0162) | Mark such a model trusted, which puts it under the cap | Local models are outside the cap | Nothing |
| Is the cap's month a calendar month in UTC (models 008) | Yes | Yes | Nothing |
| May a person see the admin's settings, read-only (interface 003) | Once there is an admin role, show a person only the ones that explain something they can see | Everyone sees all, read-only | P11-01 |
| A short name for the greeting (interface 003) | A "what to call you" setting, which General would draw with no other work | The whole name | Nothing |
| Once recall brings sensitive content into chats, should the chat-reading tools leave those chats out for a model that may not read sensitive content (agents 001) | Yes; recall marks such chats | Nothing is marked yet | P6-05 |
| At the step limit, should the model get one more call to sum up, instead of Keystone's note (agents 001) | No; the note asks the person to send another message | The note | Nothing |
| May each person switch tools off for themselves (agents 001) | Yes | Yes | Nothing |
| Should the person map alone hold a chat to local or trusted models, as recalled content does (knowledge 005) | No: the map is already kept out of calls a hosted model could get | Not held | Nothing |
| Should a chat that was sent sensitive content stay held for good, with a new chat as the only way back to a hosted model (knowledge 005) | Yes | Held for good | Nothing |
| For the approval rule, is knowledge store content "outside content" (knowledge 005) | Sources count, pages do not | Not decided | P7-02 |
| Should a person see and correct a chat's summary (chats 004) | Yes, read-only first, in the turn inspector (P5-04) | No way to see it | P5-04 |
| Where pinning is offered (chats 004) | A small action beside Copy on every message | Routes only | the chat screen |
| After outside content, should side effects ask for the rest of the chat or only the rest of that turn, as the plan says (agents 002, decision 0221) | The rest of the chat: what was read is sent again with every later call | The rest of the chat | Nothing |
| How long a call waits for an answer (agents 002) | 24 hours, each person can change it | 24 hours | Nothing |
| Should each web search ask before it is sent, as New Keystone did (agents 003) | No; a person can set web_search to ask on Settings → Approvals | Runs without asking | Nothing |
| Should web tools run in a chat held for sensitive content (agents 003, decision 0252) | Ask there instead of refusing, now that approvals exist | Off in a held chat | Nothing |
| Where the build agents run | Decided: the owner hands the build to Codex | | nothing |

## Done

- P0-01, repo layout and settings registry, and P0-02, project scripts and docs skeleton. `verify` is green, 8 of 8.
- P0-03, the five stack decision records (0002 to 0006) and the Windows check. `rebuild` and `verify` are green on Windows 11 with Go 1.26, and `keystone serve` serves the page compiled into the binary.
- P2-01, tenant folders and databases. A tenant is a folder under `data/tenants/` with its own SQLite database, and a store handle for one tenant cannot read another's rows or files. `tools/tenant` adds and lists tenants, and `rebuild` now opens the data folder.
- P3-01, the model seam's internal format: content blocks, stream events, usage, validation, a JSON codec, and a collector that folds a stream into a whole reply. Standard library only. Built by the models lane in its own worktree and merged here.
- Wave 4, six items built at once, each in its own worktree, and merged on November 24, 2026. `verify` is green, 12 of 12.
  - P2-02, sign-in: passwords, sessions and passkeys in each tenant's own database, and the `tenant isolation` check. Passkeys are off until P4-01 proves them in a browser.
  - P2-03, secrets: `secrets.bin` for each tenant under a master key the operating system holds, and the `no keys in output` check. The macOS and Linux keepers have not been run on those systems.
  - P2-04, the append-only audit log, tenant settings over the registry's defaults, and the `fixed rules` check.
  - P3-03, the fake provider, the seam's `Provider` interface, and the `offline` check.
  - P4-02, the chat record: chats, messages as a tree of branches, search, and deletion with a chat's files.
  - P6-01, the knowledge store interface, the `none` adapter and the contract suite.
- P6-02, the built-in knowledge store, provisional. Pages, sources and vectors are rows in the tenant's own database, and the store passes the whole contract suite with every capability. The Markdown folder in the item's scope is not built (decision 0060), and vectors are proven with test vectors only until P6-08.
- P3-02, the model registry: each tenant's models as checked entries in its own database, a class on each that decides whether it may read sensitive content (the guard for fixed rule 3), and providers opened with keys from the vault. Only the fake adapter can be opened until P3-04 to P3-06.
- P4-01, the web shell, provisional: a React and Vite app built from the draft tokens and compiled into the binary, with the frame, the theme switch, routing and a sign-in screen. Its four API paths are guesses until P2-05 is merged, and it has been seen only in the "server not ready" state.
- P3-09, the test tenant: `rebuild` loads an invented person, "Test Person One", who can sign in and has three chats, and `verify` has the `test tenant` check, 13 of 13. Files, images and knowledge for it wait for P9-02 and later. Nothing yet ties the test tenant to the fake provider on a route: P4-03 does.
- P2-05, the API shell: routes under `/api/v1/` inside one audit wrapper, the session in a cookie the page cannot read, server-sent events, the OpenAPI document, the command-line client, and route walks in `verify` (`calls recorded`). The web shell is wired to it: on November 25, 2026 the test tenant signed in through the browser, the frame loaded, and a reload on `/chats` kept the page.
- P3-07, the token budget: a request that cannot fit a model's window is refused before it is sent, with the `context fit` check. `verify` is 15 of 15. Every model is estimated until the adapters count for themselves.
- P3-05, the OpenAI Responses adapter, provisional: built and tested against hand-written exchanges, never against the real API.
- P6-03, the Waymarks adapter, provisional: it passes the whole contract suite against a fake Waymarks written from Waymarks' own source, and has never called a real one. Both knowledge adapters are compiled into the binary.
- P3-04, the Anthropic Messages adapter, and P3-06, the Chat Completions adapter, both provisional: built and tested against hand-written exchanges, never against a real endpoint. With P3-05 that is all three real adapters. None is in the server's set of builders yet: the chat turn (P4-03) is where a provider is opened.
- P4-03, streaming turns: twelve chat and turn routes, a turn that streams over server-sent events, stop, resume after a dropped connection, and turns kept in the database so a queued one finishes after a restart. On November 25, 2026 a message sent to the running server as the test tenant streamed a reply from the fake provider. The server starts with the three real adapters. A real model is registered with `go run ./tools/tenant model add`, as `docs/runbooks/adding-a-model.md` describes; none has been yet.
- P4-04, the chat screen, provisional: the sidebar's chats, a new chat, a reply that streams in, the title, rename, star and delete. On November 25, 2026, on the merged server, the test tenant typed a message in the browser and the fake provider's reply streamed in. Stop, resume after a dropped stream, a reload mid-reply and a failed turn have run only against a stand-in server, because the recorded replies finish too fast to interrupt. No model picker, edit, retry or attachments yet.
- P6-04, the profile and the map, provisional: a short text about the person, and a size-bounded outline of what their knowledge store holds, in two versions so a model that may not read sensitive content never sees a sensitive space. The turn puts both into every call (`internal/turn/person.go`). No route or screen edits the profile yet.
- P5-01, the context builder: every chat call is packed under its model's window, starting with standing instructions, with the oldest turns dropped whole when a chat is too long, and a record kept of what each call was built from. The person's profile and the map of their knowledge store are now part of each call. Recall, summaries, project text and styles have named slots that are empty until their items land.
- P3-08, routing and usage: a model chosen by the kind of task, a fallback chain that moves on only before any of a reply has been passed on, one usage record for each model call, and a monthly cap that stops a call to a model that is not local, with the `spend cap` check. `verify` is 16 of 16. `tools/tenant` gained `model`, `setting` and `usage` commands. No real endpoint has been called. Privacy routing is built and tested and has no caller until recall (P6-05) marks a call sensitive.
- P11-01, the settings screens, provisional: Profile, Appearance, Models and keys, and General, reached from the person's name in the sidebar, with routes under `/api/v1/` for settings, secret names, the model registry and the profile. General draws a control for every setting a person may change, generated from the registry, and a test proves every one is there. No route ever returns a key. Connectors, approvals and spend are placeholders. On November 25, 2026 the screens loaded on the running server with no console errors. There is no settings mockup, so the layout is provisional.
- P7-01, the tool loop, provisional on the agent mockups (P1-05) for how steps look: a turn runs tools and calls the model again, up to a step limit, with each call and result recorded and shown in the chat. Three read-only tools so far: the date and time, search your chats, read a chat. Fixed rule 2 holds by construction: no tool that changes anything can run until approvals (P7-02) exist, and the new `approval` check proves it. `verify` is 17 of 17. Finished by a second session after the first ran out of usage.
- P6-05, recall: each message searches the person's knowledge store and the best hits go into the call, labelled with where they came from; a sensitive hit never reaches a model that may not read it, and a chat that received one is held to local or trusted models from then on. The `recall` and `sensitive content` checks run on the test tenant with both stores. Finishing it found and closed a leak: a call was built for the first model of its route, so a hosted fallback could have received a local model's map. Each call is now built for the least of its route. A model that can use tools can also search the store and read its pages and sources itself, with the same filter and the same hold. `verify` is 19 of 19. Finished by a second session, and joined to the tool loop at the merge.
- First real model calls, November 25, 2026: the owner added a person of his own with three models (Claude Sonnet 5.5 and Claude Haiku 5.5 through the Anthropic adapter, and Qwen 3.8 27B on his home-network gateway through the Chat Completions adapter, with the gateway's own certificate authority). One short plain answer from each came back through Keystone's adapters and his vault. This is the first part of the live checks of P3-04 and P3-06; tools, thinking budgets, caching and the other endpoints are still to check. The Responses adapter (P3-05) has not been called.
- The chat's model picker (part of P4-04, still provisional on the mockups): the composer lists the person's models with a local, hosted or trusted mark; a model whose key is missing cannot be picked; a pick goes with the next message and the server keeps it as the chat's model. A chat held to local or trusted models is learned in the browser from a refused turn until the model route reports it (decision 0241).
- P5-02, compaction: when a chat's earlier turns no longer fit, the oldest are summarised beside the turn (never delaying it) by the summary model, and later calls send the summary in their place; summaries extend one another and are stored with the chat. Pinned messages are never left out (pin routes, no button yet). A held chat's summary is written and read only by models that may read sensitive content. The `context fit` check grows a chat to ten times each model's window. Only the fake provider has written a summary so far.
- P7-02, approvals, provisional on the agent mockups for the card's look: each tool is always, ask or never, per person (a list on Settings → Approvals); a tool that changes something asks by default, and always asks once the chat has read outside content, whatever its policy. A call that asks pauses the turn, shows an approval card, and runs once when approved; a waiting turn survives a restart. The `approval` check proves a side effect after outside content always asks.
- P7-03, web search and fetch, provisional until the owner's first search in a chat (a real Tavily search with his key through Keystone's searcher returned three results on November 25, 2026): `web_search` through Tavily with each person's own key (`provider.tavily`), numbered sources the answer cites as [1], [2] with links in the chat, and `web_fetch` for one page, which refuses any address that is not public, after resolving it and on every redirect. Every result is marked as outside content, so a tool that changes something always asks afterwards. Both tools are off in a chat held for sensitive content.
- The mark's form, chosen by the owner on November 24, 2026: three nested arches and one tall, sharp keystone ("Portal", round seven on the Keystone UI canvas). Colours, chosen the same day: three coppers for the arches and an ivory keystone, on a dark ground. Copper is Keystone's accent. See `design/README.md`. Approved by the owner the same day, with the dark and light chat screens.

- The brief (`docs/briefs/keystone-reboot.md`), its review (`docs/reviews/keystone-reboot-brief-review.md`), the plan, and the parallel plan (`docs/plan/PARALLEL.md`).

## Known gaps

- The owner chose the public `keystone` repo over a private one on November 24, 2026, so everything here is public: invented data only.
- P0-03 was checked on Windows only. P0-01 and P0-02 were checked on Linux and then on Windows. P3-01 and all of wave 4 were checked on Windows only.
- Four of the six wave 4 sessions stopped after committing their code and before their write-up (P2-03, P2-04, P3-03, P6-01). The integrator wrote their journal entries, and for P2-03 and P6-01 the missing docs and decision records, from the code and the plans. Those entries say so.
- Two lanes were each green alone and red together: P2-03 had the tenant package read a setting, which P2-04's fixed rules check refuses. It was fixed in the code at the merge (core 006). Expect this at every wide merge, and run `verify` after each branch.
- `verify` does not check that links inside the docs lead somewhere. Two dead links got through and were fixed by hand (knowledge 001).
- On November 25, 2026 the owner had the build put on his own domain. It runs on this machine on `127.0.0.1:9080`, reached through his Cloudflare tunnel, with Cloudflare Access (email code, two people) in front of every path. The hostname is kept out of this public repo. The server is started by hand (`bin/keystone.exe serve --address 127.0.0.1:9080`) and stops with the session that started it: nothing restarts it after a reboot yet. The session cookie is not marked Secure, because the tunnel speaks plain HTTP to Keystone; the browser still only sees HTTPS. Keystone does not yet check Cloudflare's token itself, so anyone who can reach port 9080 on this machine reaches Keystone.
- Scripts and computer use are out of this build (see PLAN.md).

## Carry forward

- Borrow from New Keystone: durable jobs and queued turns, Tavily search, voice dictation, the Mac mini gateway client, Operations, approval cards, Waymarks client code.
