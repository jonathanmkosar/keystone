# Plan review: Keystone reboot (brief design, before PLAN.md)

Verdict: not ready (the plan does not exist yet; three design holes in the brief should be closed before it is written)
Reviewed: 2026-11-24

## The plan in three sentences
Build a Go and React agent workspace modeled on Claude.ai, with a model seam over three provider adapters, separate chats whose context is rebuilt every turn, and a pluggable knowledge store with Waymarks first. Build order is a design phase first, then the rest in phases the Waymarks way; there is no PLAN.md yet, so the order beyond "design first" is not written. It is proven by a standing verify, a test tenant, recall, context-fit and tenant-isolation checks, and comparison against mockups.

## Holes
| # | Hole | Why it matters | Suggested fix | Must close before building? |
|---|---|---|---|---|
| 1 | Untrusted content plus unsandboxed scripts plus no per-run approval. Web pages, fetched files, emails and connector results can carry instructions; the agent can then write and run a script on the host with no one asked. | One poisoned web page could read the key files and the knowledge store and send them out. This is the most expensive kind of mistake and cannot be undone once data leaves. | Taint rule: once a turn has read untrusted content, script runs and side-effect tools in that turn need approval; clean turns keep running without approval. New Keystone already does a version of this for attachments. | Yes (decision) |
| 2 | The "low-privilege account" safeguard is not real yet. New Keystone runs as the signed-in Windows user and keeps keys in current-user DPAPI, so a script started by the server can decrypt every provider key and OAuth token, and can read every tenant's database. | The safeguard written in the brief would not stop anything, and it would be trusted. | Make it a real control with a proving check: scripts run as a separate local account (or inside WSL as a separate Linux user with no Windows drives mounted) and a verify check proves a script cannot read the tenant database or the key store. Move the sandbox research to the first agent-tools item. | Yes, before the script tool is built |
| 3 | The recall design sends Waymarks hits to whatever model the chat uses, while "which models may read sensitive spaces" is still open and marked non-blocking. The automatic search on every message would attach health or finance pages to calls to hosted models. | Sensitive data sent to a hosted provider cannot be recalled. | Default rule in the model seam: content from a sensitive space only reaches models marked local or trusted in the registry; recall filters hits by the chosen model; the inspector shows what was withheld. Mark the open question as blocking for recall. | Yes (decision) |
| 4 | Tenant is defined, but users inside a tenant are not. Can two people share a tenant? Are chats private to a person? Whose knowledge store does the tenant point to? Waymarks is one owner per instance. | The tenant/user model shapes every table, every key and every grant; changing it after the code exists is a rewrite. | A tenant has users. Every record carries tenant and owning user; chats, memory and keys are private to the user; projects can be shared within a tenant. The knowledge store is set per user (one Waymarks instance per user). | Yes (decision) |
| 5 | No first useful piece. The inventory is ~25 features in one release, and only "design first" is ordered. | Months of building before anything is used means UI and context problems surface late, which is how New Keystone ended up. | The plan's first build phase is a thin end-to-end slice you use daily: chat, model seam, one adapter, chat record, streaming, the design system. Everything else lands on it. This is build order, not a smaller release. | In the plan |
| 6 | One Waymarks token for all of Keystone. The chat agent, the memory pass and sub-agents would share one grant. | Waymarks' audit cannot tell them apart, and a tricked chat agent holds the memory pass's write access. | One Waymarks token per Keystone role, mapped to the agent kit's grants: reader for chat and sub-agents, intake/compile for the memory pass. | In the plan |
| 7 | Spending with no one present. Background tasks, scheduled tasks, sub-agents, research mode and the memory pass on every turn all spend against the $100 cap; the brief does not say what happens at the cap. | A hard stop mid-month could silently break the memory pass or the morning brief; a runaway agent could spend the month in an hour. | Per-run and per-job budgets, a reserve for the memory pass, fall back to local models at the cap, and a per-tenant "pause all agents and schedules" switch. | In the plan |
| 8 | "Model everything on Claude" has no written reference, and the screen designer is deferred while the design phase blocks all interface work. | The comparison against mockups has nothing to compare to, and the build can stall on a decision nobody has scheduled. | First design item: a reference library of Claude.ai screens and interactions captured as screenshots and short recordings. Backend phases run while design is in review. Decide the designer before the design items start. | In the plan |
| 9 | Cutover from New Keystone is unstated. Both would write to the same Waymarks, and its chats, routines and Google connections stay behind. | Duplicate and conflicting facts in the wiki; unclear when the old one goes off. | The new Keystone gets its own Waymarks tokens; New Keystone's save_to_wiki is switched off when the new memory pass is live; importing old chats and routines is one plan item. | In the plan |

## Proverbs
The project has no proverbs of its own; these are the starter questions. Jonathan's stated design preferences (settings over constants, feature flags, user-driven behavior) cover most of them.

| Proverb | Status | Note |
|---|---|---|
| What varies? | Followed | Everything configurable, registry entries, settings |
| Who decides? | Followed | Per-tenant settings and defaults |
| Where is the off switch? | Not addressed | Feature flags exist in principle; no switch to pause all agents and schedules for a tenant (hole 7) |
| What happens on the next case? | Followed | New model = registry entry; new provider = adapter; new store = adapter; new tool = connector |
| Do the pieces stand alone? | Followed in design, unproven | Interfaces are named; per-piece proofs wait for the plan (hole 5) |
| What does idle cost? | Followed | Local first, spend cap; per-tenant SQLite holds at ten times the load for a household |

## Matters of taste
- The built-in knowledge store could start as read and write of plain markdown only, without citations, to keep the interface honest about what Waymarks adds.

## What this review did not check
- No PLAN.md exists, so build order, per-item proofs and the first useful piece could not be checked against a written plan.
- Whether Ollama and LiteLLM's Anthropic-compatible endpoints support everything the adapter needs (tools, streaming, thinking); only their existence was checked.
- The Waymarks MCP and API surface against what the knowledge store interface will need.

## Outcome
- 2026-11-24: Hole 1 and hole 2 closed by deferring scripts until the sandbox is researched. Hole 3 accepted as written. Hole 4 closed: tenant and user are the same, one knowledge store per tenant. Holes 5 to 9 carried into the plan.
