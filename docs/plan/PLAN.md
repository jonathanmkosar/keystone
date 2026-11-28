# Keystone: Architecture and Build Plan

> This is the intent for the build. It is written from `docs/briefs/keystone-reboot.md` and its review in `docs/reviews/keystone-reboot-brief-review.md`. When code and this file disagree, this file wins until a decision record changes it. Update it in the same commit as any decision that changes it.

Nov 24, 2026 · @Jon

## What Keystone is

Keystone is Jonathan's own agent workspace: a chat interface modeled on Claude.ai, run on his own machines, that can use any model, local or hosted. You talk to it, and you watch the work it does: its tool steps, its sub-agents and its background tasks. It remembers what matters in a knowledge store you choose. Waymarks is the first one.

One install serves several tenants. A tenant is one person, with their own chats, files, memory, keys, settings and knowledge store. Tenants never see each other's data.

### Done looks like

- Jonathan uses it daily instead of claude.ai for personal work, from the laptop and the phone.
- Every feature in the brief's inventory works end to end in one release (scripts are deferred; see below).
- It looks and behaves like Claude: layout, interactions, motion, type and spacing, with Keystone's own name, mark and themes.
- Any configured model can be picked per chat: local (Ollama and the Mac mini gateway), Anthropic, OpenAI, OpenRouter, LiteLLM.
- While an agent works he can watch its steps, sub-agents and background tasks.
- No call ever exceeds the chosen model's context window, and every turn shows what was sent, its tokens and its cost.
- It works with Waymarks, with the built-in store, or with no knowledge store at all.
- Two tenants on one install cannot see each other's chats, files, memory or keys.
- One script rebuilds a running install with the test tenant from an empty machine.

## How it is built

An install is one Go binary and one data folder. Every client goes through the same HTTP API, and the API checks every call the same way.

```
web interface ─┐                         ┌─> model seam ─> adapters ─> models (local or hosted)
phone ─────────┼─> API (session, tenant, ┼─> tools and connectors (approval policy)
command line ──┘    audit) ─> harness ───┼─> knowledge store adapter (Waymarks, built-in, none)
                                         └─> tenant folder: keystone.db, files/, artifacts/
```

### One turn

```
You ─> build context ─> model seam ─> model ─┬─> answer streams to you ─> after the turn
                ^                            │      (chat record, memory pass, usage)
                └── run tool <─ approval? <──┘ tool call
```

Every turn starts from your message. Keystone rebuilds the context, the model seam picks a model and fits the budget, and the model either answers or asks for a tool. A tool call that would change something outside Keystone goes through the approval policy, runs, and its result goes back into the context. This repeats until the model answers. After the turn Keystone saves the chat record, runs the memory pass, and records usage.

### The data folder

```
data/
  keystone.json            install settings: tenants list, ports, master key reference
  tenants/<tenant-id>/
    keystone.db            the tenant's source of truth: chats, messages, branches, summaries,
                           projects, settings, model registry, jobs, schedules, usage, audit log
    secrets.bin            provider keys and tokens, encrypted with the tenant's data key
    files/                 uploads and attachments, named by hash
    artifacts/             artifact versions
    knowledge/             the built-in knowledge store, when that adapter is chosen
```

A backup of one tenant is one file, sealed under a passphrase the person gives, holding its folder and its secrets with the data key wrapped by the passphrase's key, so it can be restored on another machine (decisions 0260 to 0262). The install master key that unwraps each tenant's data key is kept by the operating system (DPAPI on Windows) and never in the data folder.

### The door

- The **API** is HTTP with JSON, with server-sent events for streaming. It is the only thing that touches the data folder.
- A **session** belongs to one tenant. Every route resolves the tenant from the session and opens only that tenant's folder.
- The **web interface** and the **command line** are clients of the same API, with no private path around it.

### Four rules no setting can switch off

1. A tenant never reads another tenant's data.
2. A side effect outside Keystone happens only under the approval policy, and content the agent read never grants approval.
3. Content from a sensitive space reaches only models the registry marks local or trusted.
4. Every model call and every tool call is recorded: what was sent, what came back, tokens and cost.

## What is inside

### Tenants, sign-in and secrets

A tenant is one person. Each has a folder and one SQLite database, with numbered migrations that move forward only. People sign in with a password or a passkey. Provider keys and connector tokens are encrypted with a per-tenant data key; the browser never receives a key, and keys never appear in logs, the audit log or API responses.

### The model seam

Every model call goes through one interface. A call states what it needs: tools, vision, thinking, context size, privacy (whether it carries sensitive content), and a cost ceiling.

- **Internal format.** Keystone's own request and stream shape, built from content blocks: text, image, file, tool call, tool result, thinking. Nothing outside the adapters sees a provider's format.
- **Model registry.** One entry per configured model: endpoint, adapter, context window, output limit, capabilities, price, and local, hosted or trusted. Adding a model is a settings change.
- **Three adapters.** Anthropic Messages (Anthropic, and Ollama or LiteLLM where it helps), OpenAI Responses (OpenAI), and Chat Completions (OpenRouter, LiteLLM, Ollama, the Mac mini gateway with its own certificate, and anything else).
- **Routing.** Rules from settings: chat uses the model picked; titles and summaries use a cheap model; the memory pass uses a local model; sensitive content is limited to local or trusted models; on error the call falls back along a chain. On Auto, the model router chooses each request's model and effort from the admin's ladder, with a local decision model's verdict, and passes it to these rules (`docs/plan/SPEC-router-and-admin.md`, Part 1). It is a package in Keystone, not a separate app (decided by the owner, November 26, 2026).
- **Budget.** Every call fits the model's window: token counts from the provider where it offers them, an estimate with a safety margin where it does not.
- **Usage.** Every call records tokens, cost, latency and outcome. Hosted spend defaults to a $100 monthly cap per tenant, with a warning near it and a stop at it.
- **Fake provider.** A scripted model for tests and the test tenant, so `verify` needs no keys and no network.

### Chats and context

A chat is one conversation with a clear start. The **chat record** keeps every message and branch (edit and retry), a summary, topics, decisions, and when and why. It never moves into the knowledge store.

Context is rebuilt every turn, packed in a fixed priority order: rules and tools, the current message, attachments and attached chats, recent turns, recalled knowledge, older history as a summary. Past a threshold, older turns are compacted into a summary; pinned items are never dropped. The start of every call is kept identical (rules, tool names, profile, person map, project) so providers can cache it. Most tool definitions are sent as names only until one is needed. Recalled items below a relevance threshold stay out.

- **Attach a chat.** Another chat comes in as its summary, and the agent can open the rest.
- **Handoff.** A button opens a fresh chat carrying a summary of this one, linked back.
- **Past-chat search.** A tool the agent uses by itself.
- **Turn inspector.** Shows what went into each turn, by section, with tokens and cost. You can pin or remove items for the next turn.

### Knowledge

The knowledge store is an interface: read the catalog, search, read a page or source, add a source with facts. Each adapter states which optional capabilities it has.

- **Adapters.** Waymarks (one instance per tenant), the built-in store (a markdown folder plus a SQLite index), and none.
- **Waymarks tokens.** One per Keystone role, mapped to the agent kit grants: a reader for chat and sub-agents, an intake grant for the memory pass.
- **Profile.** A few always-loaded facts and preferences.
- **Person map.** A short directory of what kinds of things the store holds, per space, generated from its catalog and rebuilt when the catalog changes.
- **Recall.** An automatic search on every message attaches the top hits; a standing rule says to search before answering anything about the person's world; lookup tools let the agent go deeper. Sensitive hits are dropped for models not allowed to read them, and the inspector shows what was withheld.
- **Memory pass.** After each turn, a local model reads the exchange and adds durable facts as a source that links back to the chat. It has its own spend reserve.
- **Embeddings.** Waymarks runs no embedding pipeline, so Keystone makes the vectors: embedding is a model seam capability, local first. Page vectors are pushed to stores that take them, and recall gets a query vector so it searches by meaning as well as by keyword. A sensitive page is embedded only by a local or trusted model.
- **Wiki upkeep jobs.** The memory pass only adds sources. The Waymarks agent-kit jobs (compile, lint, upkeep, fix issues) run as Keystone scheduled agents, each with its own token, and turn those sources into cited pages.
- **Memory screens.** View, correct and forget; incognito chats that keep no record and run no memory pass.

### Agents and tools

- **Tool loop.** Multi-step, with each step visible in the chat and a step limit from settings.
- **Approval policy.** Per tool: always, never, or ask. Side effects ask by default. Once a turn has read outside content (web pages, fetched files, email, connector results), every side-effect tool in that turn asks.
- **Built-in tools.** Web search and fetch with citations, knowledge lookups, past-chat search, artifacts.
- **Connectors.** MCP servers, local and remote, with a tools menu to switch them per chat.
- **Skills.** Folders of instructions loaded when needed.
- **Sub-agents.** Run in parallel with their own context, each shown as a lane in the chat.
- **Background tasks.** Durable jobs that outlive the tab and resume after a restart.
- **Scheduled tasks.** A prompt on a schedule; each run is a chat turn. A missed run happens once when the machine is back. The morning brief is one of these.
- **Research mode.** A long multi-source report built by sub-agents.
- **Unattended spend.** Each background run, schedule and sub-agent has its own budget. At the monthly cap, unattended work falls back to local models or stops, and says so. A per-tenant switch pauses all agents and schedules at once.

### Workspace

- **Projects.** Instructions, knowledge files, and project-scoped chats and memory.
- **Uploads.** Files and images, routed only to models that can read them.
- **Artifacts.** Code, HTML, React, SVG, Mermaid and markdown documents in a side panel, rendered in a sandboxed frame, with versions, download and a local publish link.
- **Inline visuals.** Charts and diagrams in the reply.
- **Styles.** Saved tone and format presets.
- **Voice.** Dictation into the composer, and a voice mode.
- **Browser use.** The agent drives a separate headless browser profile that holds none of the person's sign-ins. Pages it reads are outside content.
- **Share.** A read-only link to one chat.

### Settings, operations and monitoring

Settings for profile, appearance (light, dark, themes, font), models and keys, connectors, approvals, spend and usage. Every value has a default chosen for the best experience. Operations and Monitor pages show jobs, schedules, model health, spend against the cap, and where the install is running out of room.

## The look

Keystone copies Claude's layout, interactions, motion and features, and uses its own name, mark and themes. The design phase comes before interface code:

1. A reference library of Claude.ai screens and interactions, captured as screenshots and short recordings, so every later screen has something concrete to match.
2. The Keystone design system: tokens for colour, type, spacing, radius and motion; light and dark plus themes; one family with Waymarks.
3. The mark, building on the existing Keystone logo studies.
4. Mockups of every screen, agreed by Jonathan before that screen is built.

Backend phases run while design is in review.

## What stays out of this build

- **Agent-written scripts and script workflows**, until the sandbox is researched. They come back with their own brief.
- **Computer use**, until Jonathan decides.
- **A hosted service** for other people.

## Stack

Each choice becomes a decision record in phase 0. Where Waymarks already decided the same question, Keystone follows it unless a reason is written down.

| Layer | Choice | Why |
| --- | --- | --- |
| Language | Go, built to one binary | One file to run; matches Waymarks |
| Store | SQLite through modernc.org/sqlite, one database per tenant, full-text search | Same driver as Waymarks (its decision 0004); isolation by construction |
| API | HTTP with JSON and server-sent events | One door for every client; streams to the browser |
| Web interface | TypeScript and React with Vite, built files committed and embedded in the binary | Same as Waymarks (its decision 0005); building needs Go only |
| Project scripts | Go, run with `go run` | Same commands on Windows, macOS and Linux |
| Secrets | Per-tenant data key, wrapped by an install master key held by the operating system | Keys stay out of the data folder and the repo |
| Look | Tokens from the Keystone design system | One source for colour, type and motion |

## Build phases

Phases group the work; they are not a queue. Several lanes build at once, an item starts as soon as the code it depends on is done, and an approval or a decision that is still open never stops the parts that do not rest on it. `docs/plan/PARALLEL.md` has the lanes, the owner gates and the waves. Each feature stays behind a setting until its checks pass. The release is done when every phase passes.

| Phase | Scope | Done when |
| --- | --- | --- |
| 0. Foundations | Repo layout, settings registry, project scripts, docs skeleton, journal, decision records for the stack, Windows check. | `rebuild` runs from an empty Windows machine and `verify` is green. |
| 1. Design | Claude reference library, design system, mark, mockups of every screen. | Jonathan approves the design system, the mark and each group of mockups. |
| 2. Tenants and core | Tenant folders and databases, migrations, sign-in, secrets, audit log, per-tenant settings, the API shell. | A session for one tenant is refused on every route of another, and every call is in that tenant's audit log. |
| 3. Model seam and test tenant | Internal format, registry, fake provider, three adapters, budget, routing, fallbacks, usage, spend cap. The test tenant: an invented person's chats, files, knowledge and recorded turns. | Every registered model passes the context-fit check, a call over the cap is stopped, every call has a usage record, and `rebuild` creates the test tenant. |
| 4. First slice | Web shell from the design system, chat record, streaming turns, the chat screen. A week of Jonathan's daily use, which nothing else waits on. | The chat screens match their mockups, and the week's findings are each an item or closed. |
| 5. Context | Context builder, compaction, caching, turn inspector, chat summaries, past-chat search, attach and handoff. | A long chat never exceeds the window, cache hits show in usage, and a handoff chat answers a question about its parent. |
| 6. Knowledge | Store interface, built-in store, Waymarks adapter, profile, person map, recall, memory pass, memory screens, embeddings. | The recall checks pass on the test tenant with both stores, and a sensitive page never reaches a hosted model, for chat or for embedding. |
| 7. Agent loop and tools | Tool loop, approval policy, web search and fetch, several search providers with fallback (`docs/plan/SPEC-web-search.md`), connectors, skills. | An agent finishes a multi-step task with visible steps, and a side effect after outside content always asks. |
| 8. Agents | Background tasks, sub-agents, scheduled tasks, research mode, unattended spend, wiki upkeep jobs. | Sub-agents run in parallel lanes, a background task survives a restart, the pause switch stops everything, and a fact told in chat becomes a cited Waymarks page. |
| 9. Workspace | Projects, uploads, artifacts, inline visuals, styles. | Each works in the test tenant and matches its mockup. |
| 10. Voice, browser, sharing, phone | Dictation, voice mode, browser use, share links, phone layouts and access. | Jonathan uses Keystone from the phone, and browser use never touches his own sign-ins. |
| 11. Settings and operations | Settings screens, usage and cost, Operations and Monitor. | Every setting in the registry has a screen, and spend matches the usage records. |
| 12. Cutover | Own Waymarks tokens, New Keystone's wiki writes switched off, Gmail and Calendar as connectors, import of old chats and routines. | Only Keystone writes to Waymarks for Jonathan, and the morning brief runs here. |
| 13. Hardening | Tricked-agent drill, tenant isolation drill, load test, backup and restore, rebuild drill, final screen comparison. | Every drill passes and the results are in the journal. |
| 14. Model router | Effort on the seam, decision calls, the router, Auto in the turn, the admin's router policy, Auto for unattended work, Auto in usage, the live check. `docs/plan/SPEC-router-and-admin.md`, Part 1. | A held chat on Auto never reaches a hosted model, each step answers in the live check, and Usage shows Auto against the top step. |
| 15. Admin and views | The admin account, people and monitoring, disabling a person, admin settings from the browser, views and view sets, encryption at rest, the admin isolation drill. `docs/plan/SPEC-router-and-admin.md`, Part 2. | An admin manages people, settings and views from the browser, sees no one's content, and a view that is off is refused by the server. |

Jonathan has a chat he can use at the end of phase 4, with memory at the end of phase 6, and agents at the end of phase 8.

## Standing checks

`verify` runs these from the first phase they apply to, and the list grows as features land.

- **Tenant isolation.** Every route, tool and job is refused or empty for another tenant's data.
- **Every call recorded.** Each model call and tool call has an audit entry with what was sent, tokens and cost.
- **Context fit.** For every registered model, the largest test chat is packed under its window.
- **Sensitive content.** Recalled content from a sensitive space never appears in a call to a model not marked local or trusted.
- **Approval.** A side-effect tool in a turn that read outside content always asks, whatever the tool's setting.
- **Spend cap.** A hosted call that would pass the cap is stopped, and unattended work falls back or stops.
- **Recall.** Test-tenant questions whose answers sit in pages the person map does not name are answered from the store; the share is recorded each run and may not fall.
- **No keys in output.** No key or token appears in logs, the audit log, API responses or the journal.
- **Fixed rules.** No setting can switch off any of the four rules.
- **No real data and no secrets.** Nothing in the repo, the journal or the fixtures is real, and every fixture is marked as test data.
- **Offline.** `verify` passes with no network and no keys, using the fake provider.
- **Test tenant.** Every use case in the test tenant produces its expected result.
- **Docs.** Every component has a doc, every setting has a registry entry, and every decision is linked.
- **Rebuild.** `rebuild` works from an empty machine.

## How the agent builds it

Agents build Keystone by repeating one loop, as Waymarks was built, with one agent per lane working at the same time. Each pass picks one work item in its lane, builds it as far as it can be built today, checks it, documents it, journals it and hands it to the integrator to merge. Anyone can stop after any pass and pick up again from the repo alone. The loop is in `docs/plan/LOOP.md`, and the lanes and gates are in `docs/plan/PARALLEL.md`.

### Three rules

1. The repo is the only memory. Nothing lives in a chat or on one laptop.
2. Every loop ends with a working build and updated docs, never half done.
3. This document is the intent. When code and intent disagree, intent wins until a recorded decision changes it.

### Repo layout

```
cmd/keystone/          the binary: the server and the command line
internal/              tenants, API, harness, model seam, adapters, context, knowledge, tools, jobs
web/                   the web interface (built files committed)
tools/                 rebuild, verify, doctor, snapshot, tenant
settings/              every setting, its default and its description
fixtures/test-tenant/  invented chats, files, knowledge pages, use cases and expected results
design/                reference library, design system, mark, mockups
docs/
  briefs/              the brief this plan delivers
  reviews/             plan reviews
  architecture/        overview, one file per component, decisions/
  plan/                PLAN.md, LOOP.md, STATE.md, items/
  journal/             one entry per loop, append-only
  runbooks/            backup, restore, adding a tenant, adding a model
```

### Rebuild

`rebuild` takes an empty machine to a running install with the test tenant. It checks the required tools, builds the binary, creates the data folder, applies migrations, loads the test tenant (chats, files, a built-in knowledge store, the fake provider), replays recorded turns, and runs `verify`. Model output is not repeatable, so recorded turns stand in for live ones; a live run gives different wording and every check still passes.

### Rules for the agent

- Commit to main. Put no agent or AI attribution in a commit message or author. Open a pull request only when Jonathan asks.
- Use invented data only. Never put real personal data in the repo, the fixtures or the journal.
- Never write a key or a secret anywhere in the repo. Read it from the tenant's secret store or an environment variable by name.
- Never weaken one of the four rules to make a check pass.
- One item per loop. No loop ends red.
- Never wait. When a question comes up, write it in `STATE.md` with a recommended answer, build what does not rest on it, mark the rest provisional, and move on.

## What Jonathan provides

- The go-ahead to create the private repo, and its name.
- A Windows machine with Go, Git and Node, where the agent works.
- Screenshots and short recordings of Claude.ai for the reference library, or the go-ahead for the agent to capture them in a browser he signs in to.
- Approval of the design system, the mark and each group of mockups.
- For live checks only, not for `verify`: keys for the hosted providers he wants, and the Mac mini gateway address.

## Open items that do not block the start

| Item | What the plan assumes until Jonathan decides |
| --- | --- |
| Computer use | Out of this build |
| Sandbox research, which brings scripts back | Not started; scripts stay out |
| Phone access: tunnel, domain, sign-in | The home network only, with password and passkey, until phase 10 |
| Repo module path | `keystone`, under Jonathan's GitHub account, private |
| Voice mode providers | Browser speech for dictation; voice mode picks a provider in its item |

Decided since the first draft: the name stays Keystone (November 24, 2026). The agent drafts the screens and Jonathan corrects them; the first round is on the "Keystone UI" design canvas, built on the Waymarks design system.

## After the plan review

The brief was reviewed on November 24, 2026 (`docs/reviews/keystone-reboot-brief-review.md`). Holes 1 and 2 were closed by deferring scripts; hole 3 became rule 3; hole 4 was closed by making tenant and user the same. The plan carries the other five: a first useful piece (phase 4), one Waymarks token per role (knowledge), unattended spend and the pause switch (agents), the Claude reference library (design), and the cutover from New Keystone (phase 12).

The plan itself was checked the same day for what would stall parallel work. In the first draft, 37 of 73 items sat behind something only Jonathan could close, 25 of them behind the chat screen and its week of use. Dependencies now mean code only, approvals are owner gates with a stated way to build without them, and one item is left behind an owner-closed one. Two items the brief called for were missing and were added: embeddings (P6-08) and the wiki upkeep jobs (P8-06).
