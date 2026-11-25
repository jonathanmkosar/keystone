# For any agent working on Keystone

This file is where you start, whatever tool you are: Codex, Claude Code, Cursor or another. It points to the real instructions and does not repeat them.

## Start here, in this order

1. `docs/plan/STATE.md`: where the work stands, what each lane does next, and what is waiting on the owner.
2. `docs/plan/LOOP.md`: the loop you repeat. One pass finishes one work item.
3. `docs/plan/PARALLEL.md`: the lanes, the owner gates, and what to do when a question comes up. The short version: never wait. Write the question in `STATE.md` with a recommended answer, build what does not rest on it, and move on.
4. `docs/plan/PLAN.md`: the intent. When code and the plan disagree, the plan wins until a decision record changes it.

Then run:

    go run ./tools/doctor

It prints the state, whether the build is healthy, the next item each lane can pick up, and what is in flight: work a stopped session left on a lane branch or in a worktree. If anything is in flight, finish it first: "Resuming after a stop" in `docs/plan/LOOP.md`, with the brief in `docs/plan/LANE-BRIEF.md`. The item files are in `docs/plan/items/`.

## The branch

This is the `reboot` branch of `jonathanmkosar/keystone`. It shares no history with `main`, which holds the earlier Python Keystone. Where the plan and the loop say `main`, read `reboot`, until the owner says otherwise. Do not merge into `main` and do not open a pull request unless he asks.

## Rules that are easy to break

- Put no agent or AI attribution in a commit message or author. Commits are authored as the owner.
- The repo is public. Invented data only: no real names, addresses, accounts, emails or documents in code, fixtures, tests or the journal. No keys or tokens anywhere. `verify` scans for both.
- A value someone might change is a setting in `settings/<lane>.json`, not a constant in code.
- Nothing is committed with a red `go run ./tools/verify`. Never weaken a check to get to green.
- The four fixed rules in `PLAN.md` cannot be switched off by any setting, and no check on them may be loosened.
- Prefer plain functions and data over class hierarchies.

## Each pass

    You work the <lane> lane. Read docs/plan/STATE.md, your lane's latest journal entries,
    docs/plan/PLAN.md and docs/plan/PARALLEL.md. Run the doctor tool. Do the next work item
    in your lane as far as it can be built today, then finish with verify, docs, journal
    and status.

Finish every item the same way: tests, `go run ./tools/verify`, the part's doc under `docs/architecture/`, a decision record for any choice that was not obvious, one journal entry in `docs/journal/` named by date, lane and number, the item's status, and `STATE.md`.

## Where things stand on handoff, November 24, 2026

- Done: P0-01 to P0-03 (layout, settings registry, scripts, stack decisions), P2-01 to P2-04 (tenants, sign-in, secrets, audit log and tenant settings), P3-01 and P3-03 (the model seam and the fake provider), P4-02 (the chat record) and P6-01 (the knowledge store interface). `verify` is green, 19 of 19, on Windows 11 with Go 1.26.
- Ready now: P2-05 (the API shell), P3-02 (model registry), P6-02 (built-in knowledge store), P6-03 (Waymarks adapter). P2-05 unblocks the most: P4-01, P4-03 and P3-09 wait on it.
- The store is `modernc.org/sqlite`, the same driver and version Waymarks uses (decision 0003). Follow Waymarks' choices unless a decision record says why not.
- The look is decided. `design/README.md` has the mark and every colour. `design/screens/` has the mockups as reference markup. Build the interface from the tokens, and treat screens marked as drafts as provisional.
- The earlier Python Keystone on `main`, and the owner's later "New Keystone", are sources to borrow from: durable jobs and queued turns, web search with approvals, voice dictation, the local model gateway client, approval cards.

## What only the owner can do

Listed in `STATE.md` under "Waiting on the owner". Do not guess a key, a sign-in or an approval. Build what the item says can be built without it, and mark the rest provisional.
