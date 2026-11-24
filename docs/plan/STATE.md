# State

Phase: 0, foundations, and 1, design. The build has started.
Next: wave 3. P0-03 (core), P2-01 (core) and P3-01 (models) can all start.
Blocked: nothing

## Lanes

| Lane | In progress | Next |
| --- | --- | --- |
| core | nothing | P0-03, P2-01 |
| models | nothing | P3-01 |
| chats | nothing | P4-02, after P2-01 |
| knowledge | nothing | P6-01, after P2-01 |
| agents | nothing | P7-01, after P4-03 |
| interface | nothing | P4-01, after P2-05 |
| design | P1-02 design system, P1-03 mark, P1-04 chat mockups | The other screens in copper and ivory, then P1-05 |

## Waiting on the owner

| Question or input | Recommended | Assumed meanwhile | Provisional because of it |
| --- | --- | --- | --- |
| Go-ahead to create the private repo `keystone` on GitHub | Yes | Work in the local git repo | P0-04 |
| A run of `go run ./tools/rebuild` on the Windows machine | Run it and say what it prints | Built and checked on Linux only | P0-03 |
| Corrections on the first round of screens | | The drafts stand | P1-02, P1-04 |
| The mark on light pages: on a dark tile, or free-standing with a dark keystone | On a dark tile | On a dark tile | P1-03 |
| The wordmark: Fraunces or Art Deco capitals | Fraunces | Fraunces | P1-02 |
| Claude.ai captures, or go-ahead to capture | Go-ahead to capture | Layout from knowledge of Claude.ai | P1-01 |
| Where the build agents run, and how many at once | Claude Code on the Windows machine, three at once | Loops run one at a time from this workspace | nothing |

## Done

- P0-01, repo layout and settings registry, and P0-02, project scripts and docs skeleton. `verify` is green, 8 of 8.
- The mark's form, chosen by the owner on November 24, 2026: three nested arches and one tall, sharp keystone ("Portal", round seven on the Keystone UI canvas). Colours, chosen the same day: three coppers for the arches and an ivory keystone, on a dark ground. Copper is Keystone's accent. See `design/README.md`. Approved by the owner the same day, with the dark and light chat screens.

- The brief (`docs/briefs/keystone-reboot.md`), its review (`docs/reviews/keystone-reboot-brief-review.md`), the plan, and the parallel plan (`docs/plan/PARALLEL.md`).

## Known gaps

- Nothing has been run on Windows yet. The binary cross-compiles for it.
- The workspace these first loops ran in cannot download Go modules. P2-01 onward needs a machine that can.
- Scripts and computer use are out of this build (see PLAN.md).

## Carry forward

- Borrow from New Keystone: durable jobs and queued turns, Tavily search, voice dictation, the Mac mini gateway client, Operations, approval cards, Waymarks client code.
