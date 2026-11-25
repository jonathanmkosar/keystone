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

## Surviving a stop

A session can stop at any moment: a usage limit, a closed window, a restart. Work is kept so that the next session, on this machine or another, loses nothing:

- **Claim with a branch name.** When an item is claimed, `STATE.md` records it under "In flight" with its lane branch, named `<lane>/<item id in lower case>-<words>`, such as `agents/p7-02-approvals`. `doctor` finds branches by that shape.
- **Plan first.** The item's Plan section says what will be built and what is left, in a few lines, before any code. It is the brief a second session finishes from.
- **Commit small, push the branch.** Commit each time `verify` is green, not only at the end, and push the lane branch to `origin` after every commit (`git push -u origin <branch>`). Only the integrator pushes `reboot`. If the session refuses a lane agent's push, the agent does not retry it or work around it and says so in its report: the commits stay in its worktree, which `doctor` shows, and reach `origin` when the branch is merged.
- **Write up at once.** Docs, journal and status follow the build commit straight away, so a stop never leaves built code with no record.

## Resuming after a stop

1. Run `go run ./tools/doctor`. Its "In flight" section lists every item marked doing and every lane branch not yet merged, with unmerged and unpushed commits and uncommitted files in its worktree.
2. For each one, in this order of value: the branches nearest done first (code committed, write-up left), then the rest.
   - **Its worktree is on this machine** (doctor names it): work in that worktree. Uncommitted files there are the stopped session's work in progress; read them with `git diff` and continue the design, do not start over.
   - **Only the branch exists** (another machine, or the worktree was removed): `git fetch origin`, then `git worktree add .claude/worktrees/<branch with / as -> <branch>` and work there.
   - **No branch yet**: start the item fresh in a new worktree on a branch of that shape.
3. Start the work with the lane brief in `docs/plan/LANE-BRIEF.md`, filled in for the item, and add: "A previous session started this and stopped. Read the item's Plan and the branch's commits and uncommitted files first, then finish."
4. When a lane finishes, the integrator merges its branch into `reboot`, runs `verify`, updates `STATE.md` (moving it out of "In flight") and pushes `reboot`.

A stopped agent in the same session can instead be sent a message to carry on: it keeps its own context.

## Prompt for each pass

> You work the <lane> lane. Read docs/plan/STATE.md, your lane's latest journal entries, docs/plan/PLAN.md and docs/plan/PARALLEL.md. Run the doctor tool. Do the next work item in your lane as far as it can be built today, then finish with verify, docs, journal and status, and hand the branch to the integrator.

For the integrator, who runs the lanes and merges them, including in a new session after a stop:

> /loop Work the Keystone build loop from AGENTS.md and docs/plan/LOOP.md: run doctor, finish anything in flight first ("Resuming after a stop"), then take the next ready item in each lane, each in its own worktree with the brief in docs/plan/LANE-BRIEF.md; merge each lane as it reports, with verify, docs, journal, status and STATE.md, commit as the owner with no agent attribution, and push to reboot. Goal: every item in docs/plan/items is done or provisional on its owner gate.
