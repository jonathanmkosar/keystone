# State

Phase: 0, foundations, and 1, design. The build has started.
Next: wave 5: P2-05 (core), P3-02 (models), P6-02 and P6-03 (knowledge). P4-03, P4-01 and P3-09 open when P2-05 is done.
Blocked: nothing
Branch: `reboot` on github.com/jonathanmkosar/keystone, a public repo. Where the plan says `main`, read `reboot`. `AGENTS.md` is where any agent starts.

## Lanes

| Lane | In progress | Next |
| --- | --- | --- |
| core | nothing | P2-05, then P3-09, P13-04 |
| models | nothing | P3-02 |
| chats | nothing | P4-03, after P2-05 and P3-09 |
| knowledge | nothing | P6-02, P6-03 |
| agents | nothing | P7-01, after P4-03 |
| interface | nothing | P4-01, after P2-05 |
| design | P1-02 design system, P1-03 mark, P1-04 chat mockups | The other screens in copper and ivory, then P1-05 |

## Waiting on the owner

| Question or input | Recommended | Assumed meanwhile | Provisional because of it |
| --- | --- | --- | --- |
| Corrections on the first round of screens | | The drafts stand | P1-02, P1-04 |
| The mark on light pages: on a dark tile, or free-standing with a dark keystone | On a dark tile | On a dark tile | P1-03 |
| The wordmark: Fraunces or Art Deco capitals | Fraunces | Fraunces | P1-02 |
| Claude.ai captures, or go-ahead to capture | Go-ahead to capture | Layout from knowledge of Claude.ai | P1-01 |
| How the sign-in screen asks who is signing in. There are no user names (core 005) | The screen lists the people on the install and the person picks their own. Look again when phone access from outside is decided (P10-05) | The sign-in functions take the tenant's store, so either answer fits | Nothing built. It shapes the sign-in route in P2-05 and the screen in P4-01 |
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
- The mark's form, chosen by the owner on November 24, 2026: three nested arches and one tall, sharp keystone ("Portal", round seven on the Keystone UI canvas). Colours, chosen the same day: three coppers for the arches and an ivory keystone, on a dark ground. Copper is Keystone's accent. See `design/README.md`. Approved by the owner the same day, with the dark and light chat screens.

- The brief (`docs/briefs/keystone-reboot.md`), its review (`docs/reviews/keystone-reboot-brief-review.md`), the plan, and the parallel plan (`docs/plan/PARALLEL.md`).

## Known gaps

- The owner chose the public `keystone` repo over a private one on November 24, 2026, so everything here is public: invented data only.
- P0-03 was checked on Windows only. P0-01 and P0-02 were checked on Linux and then on Windows. P3-01 and all of wave 4 were checked on Windows only.
- Four of the six wave 4 sessions stopped after committing their code and before their write-up (P2-03, P2-04, P3-03, P6-01). The integrator wrote their journal entries, and for P2-03 and P6-01 the missing docs and decision records, from the code and the plans. Those entries say so.
- Two lanes were each green alone and red together: P2-03 had the tenant package read a setting, which P2-04's fixed rules check refuses. It was fixed in the code at the merge (core 006). Expect this at every wide merge, and run `verify` after each branch.
- `verify` does not check that links inside the docs lead somewhere. Two dead links got through and were fixed by hand (knowledge 001).
- No API route exists yet, so "every API call is recorded" and "every route resolves its tenant" are proven for the wrappers and not for routes. P2-05 adds both walks.
- Scripts and computer use are out of this build (see PLAN.md).

## Carry forward

- Borrow from New Keystone: durable jobs and queued turns, Tavily search, voice dictation, the Mac mini gateway client, Operations, approval cards, Waymarks client code.
