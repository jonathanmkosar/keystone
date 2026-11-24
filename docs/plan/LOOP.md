# The loop

Each pass through the loop finishes one work item, or as much of it as can be built today. Several agents run the loop at once, one per lane. `PARALLEL.md` has the lanes, the owner gates and the waves. Anyone can pick up at any point by reading `STATE.md`.

1. **Orient.** Read `docs/plan/STATE.md`, the last three journal entries for your lane, `docs/plan/PLAN.md` and `docs/plan/PARALLEL.md`. Run `go run ./tools/doctor`. If it reports a broken build, fixing that is this loop's item.
2. **Pick.** In your lane, take the first item in `docs/plan/items/` whose status is `todo` or `provisional` and whose `Depends on` items are done. An owner gate does not stop you from picking it. If your lane has nothing, help the lane with the most open items.
3. **Claim.** Set the item's status to `doing (lane)` and commit that first.
4. **Plan.** Write a short plan in the item file: what will change and how it will be proven. If the item has a server side and a screen, write the contract first. If the item is too big for one loop, split it and record the split.
5. **Build.** Smallest change that delivers the item, with its tests. New behavior goes behind a setting until its checks pass. If the item has an owner gate that is not cleared, build what its `Built without the gate` line says.
6. **Verify.** `go run ./tools/verify` must be green, offline, with no keys. Add a standing check when the item creates a rule worth keeping.
7. **Document.** Update the component doc and your lane's settings file. Record any decision in `docs/architecture/decisions/`.
8. **Journal.** Add one entry in `docs/journal/`, named by date, lane and number: what was done, what was learned, what was verified, what is provisional and why, what is next. Never edit an old entry.
9. **Status and merge.** Mark the item `done`, or `provisional` with the gate it waits on. Rebase on `main` and hand the branch to the integrator, who merges, runs `verify` and `snapshot`, and updates `STATE.md`.

## Item statuses

- `todo`: not started.
- `doing (lane)`: claimed.
- `provisional`: built as far as it can be; waits on the owner gate named in the item. Its proof passes for everything except the gated part.
- `done`: gate cleared and proof passes.

## Rules

- Invented data only. No secrets in the repo.
- The integrator commits to `main`. Put no agent or AI attribution in a commit message or author. Open a pull request only when the owner asks.
- `docs/plan/PLAN.md` is the intent. When code and the plan disagree, the plan wins until a decision record changes it, and the plan is updated in the same commit.
- Never wait. When a question comes up, follow "When a question or a blocker comes up" in `PARALLEL.md`: write it down with a recommended answer, build what does not rest on it, and move on.
- Stop and ask the owner before building only for anything that would weaken one of the four fixed rules, or three failed attempts at one item.
- No merge with a red `verify`.
- Never weaken a standing check to get to green.

## Prompt for each pass

> You work the <lane> lane. Read docs/plan/STATE.md, your lane's latest journal entries, docs/plan/PLAN.md and docs/plan/PARALLEL.md. Run the doctor tool. Do the next work item in your lane as far as it can be built today, then finish with verify, docs, journal and status, and hand the branch to the integrator.
