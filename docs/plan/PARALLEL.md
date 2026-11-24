# Working in parallel

The aim: a question or a missing approval never stops the build. While it waits, everything that does not rest on that answer gets made, in full or in part.

Three things make that work.

1. **Depends on means code only.** An item's `Depends on` line lists the items whose code it needs. Approvals, keys, sign-ins and decisions are not dependencies. They are **owner gates**.
2. **Every gate has a way around it.** An item with an owner gate says what is built without it. Usually that is the server side in full, and the screen from the draft mockup, marked provisional.
3. **Lanes.** Each lane owns its own folders, so several agents can work at once without editing the same files.

## Lanes

| Lane | Owns | Items |
| --- | --- | --- |
| core | `cmd/`, `internal/tenant`, `internal/api`, `internal/secrets`, `settings/`, `tools/`, operations, backup | P0, P2, P3-09, P11-02 and P11-03 (server side), P13 except P13-01 |
| models | `internal/seam`, `internal/adapters` | P3-01 to P3-08, embeddings with knowledge |
| chats | `internal/chat`, `internal/context`, projects, uploads, styles, sharing, import | P4-02, P4-03, P5, P9-01, P9-02, P9-05, P10-04, P12-03 |
| knowledge | `internal/knowledge`, the store adapters, recall, the memory pass | P6, P12-01 |
| agents | `internal/agent`, `internal/tools`, connectors, jobs, schedules, browser | P7, P8, P9-03, P10-03, P12-02, P13-01 |
| interface | `web/` | P4-01, P4-04, P9-04, P10-01, P10-02, P10-05, P11-01, and the screen half of every `+ interface` item |
| design | `design/`, the Keystone UI canvas | P1 |

An item marked `chats + interface` has two halves. The first lane builds the server side. The interface lane builds the screen. They meet at a contract (below), so neither waits for the other.

Lanes are areas of the code, not a head count. Run as many at once as usage limits allow. Three at a time is a good start: one on core then models, one on design then interface, one that follows the widest wave.

## Rules for parallel work

- **Contract first.** The first commit of any item with a server side and a screen is the contract: the routes, the stream events and their shapes in the OpenAPI document, plus recorded responses for the fake provider. Both halves build against it. A contract changes only in a commit that updates both halves or tells the other lane in `STATE.md`.
- **The fake provider and the test tenant are everyone's back end.** The interface lane never needs a real model, a real key or a finished server feature to build a screen.
- **One worktree per lane.** Each agent works in its own git worktree on a short-lived branch and rebases on `main` before merging. One session, the integrator, merges to `main`, runs `verify`, and updates `STATE.md`.
- **Claim before building.** An agent sets an item's status to `doing (lane)` in its first commit. Nobody else picks it.
- **Shared files that would collide:**
  - Migrations are named by date and time, not by a running number.
  - Settings live in one file per lane under `settings/`, merged into the registry at build time.
  - Journal entries are named `date-lane-number.md`.
  - `STATE.md` is edited by the integrator only. Lanes report in their journal entry.
- **Provisional is a status.** Work built on a draft or an assumption is marked `provisional` in the item file with the reason. When the answer comes, only the provisional parts are revisited. An item closes when its gate clears and its proof passes.

## When a question or a blocker comes up

1. Write it in `STATE.md` under "Waiting on the owner": the question, a recommended answer, and what it holds up.
2. If a wrong guess is cheap to undo, take the recommended answer, build behind a setting, and mark the work provisional.
3. If a wrong guess is expensive to undo (data layout, a security rule, anything in the four fixed rules), park only the part that rests on it and build the rest of the item.
4. Take the next item in the lane that does not rest on the answer. If the lane is empty, help the lane with the widest wave.
5. When the answer comes, the integrator lists what was provisional because of it, and those parts are finished first.

An agent stops and waits only when every remaining item in every lane rests on the same unanswered question. With the gates below, that happens once: before the final rebuild drill.

## Owner gates

| What Jonathan gives | Items it touches | What is built without it |
| --- | --- | --- |
| Go-ahead to create the private repo | P0-04 | Everything, in the local git repo |
| Claude.ai captures, or go-ahead to capture them | P1-01 | The index and written notes; drafts of every screen |
| Approval of the design system | P1-02, P4-01 | Screens built on token names; approval changes values only |
| Choice of mark | P1-03 | The wedge stands in as one swappable file |
| Approval of mockups, by group | P1-04 to P1-07, and the screen half of P4-04, P5-04, P6-07, P7-01, P7-02, P8-02, P9-01 to P9-04, P10-05, P11-01 to P11-03 | Every server side in full; every screen from its draft, provisional |
| Provider keys and the Mac mini gateway address | P3-04, P3-05, P3-06 (live checks only) | All adapter code, proven on recorded exchanges |
| A week of daily use | P4-05 | Nothing waits on it |
| Voice provider, if a paid one is recommended | P10-02 | Dictation, and voice mode on a local or browser provider |
| Decision on phone access from outside the home | P10-05 | Phone layouts and home-network access |
| Tokens for his real Waymarks, and go-ahead to switch off New Keystone's wiki writes | P12-01 | Adapter and wiki jobs proven on a Waymarks test tenant |
| Google sign-in for the connectors | P12-02 | Connectors proven on a stand-in mail and calendar server |
| A copy of New Keystone's database, and go-ahead to import | P12-03 | The importer, proven on a fixture |

31 of the 76 items have an owner gate. Each of the 31 has something to build before the gate clears. Only one item, the final rebuild drill (P13-05), sits behind work that Jonathan alone can close.

## Waves

A wave is the set of items whose code dependencies are all in earlier waves. Everything in one wave can be built at the same time. Owner gates are ignored here, because every gated item has a part that needs no gate. Items with two lanes are listed under the first.

| Wave | core | models | chats | knowledge | agents | interface | design |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | P0-01 |   |   |   |   |   | P1-01 to P1-07 |
| 2 | P0-02 |   |   |   |   |   |   |
| 3 | P0-03, P0-04, P2-01 | P3-01 |   |   |   |   |   |
| 4 | P2-02, P2-03, P2-04 | P3-03 | P4-02 | P6-01 |   |   |   |
| 5 | P2-05, P3-09, P13-04 | P3-02 |   | P6-02, P6-03 |   |   |   |
| 6 |   | P3-04, P3-05, P3-06, P3-07 | P4-03 | P6-04 |   | P4-01 |   |
| 7 |   | P3-08 | P5-01, P9-02, P10-04 |   | P7-01 | P4-04, P11-01 |   |
| 8 | P11-02 |   | P5-02, P5-03, P5-04, P9-01, P9-05 | P6-05 | P7-02, P7-03, P8-01, P9-03 | P10-01, P10-05 |   |
| 9 | P11-03 |   | P5-05 | P6-06, P6-08 | P7-04, P8-02, P8-03, P8-05, P10-03 | P9-04, P10-02 |   |
| 10 | P13-02, P13-03 |   | P5-06, P12-03 | P6-07 | P7-05, P8-04, P8-06, P12-02, P13-01 |   |   |
| 11 | P13-05 |   |   | P12-01 |   |   |   |

P4-05, the week of daily use, belongs to Jonathan and can start once P4-04 and P3-08 are done.

The first two waves are narrow on purpose: the layout and the project scripts come first, because every lane needs `verify`. Design runs from the first minute. From wave 3 the work fans out, and by wave 8 there are fourteen items open at once.

## What changed from the first plan

- In the first plan, 37 of 73 items sat behind something only Jonathan could close: 25 of them behind the chat screen and its week of use. Now one does.
- The week of daily use is its own item, P4-05. The chat screen closes when it matches its mockup.
- The tool loop, uploads and sharing depend on streaming turns (P4-03), not on the finished chat screen.
- Mockup approvals moved from `Depends on` to owner gates.
- Two items were missing and are added: embeddings (P6-08) and the wiki upkeep jobs (P8-06). Without them, facts from chat would reach Waymarks as sources and never become pages, and recall would be by keyword only.
