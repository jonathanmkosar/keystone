# 0702. A disabled person is a mark in their own database, which sign-in, the pause switch and share links read

November 28, 2026 · decided in P15-02

## The question

An admin can disable a person and enable them again (the owner's ask of November 27, 2026). A disabled person cannot sign in, has every session ended at once, has their schedules held and their background tasks and sub-agents paused through the switches that exist, and has their share links answer as revoked; enabling restores it, and runs missed meanwhile are not caught up. Where is "disabled" kept, and how does each part learn it?

## The decision

- **The mark** is a row of the person's own `tenant_meta`, `disabled_at`, holding when. `tenant.Store.Disabled` reads it. `signin.Disable` writes it and deletes every session in one transaction; `signin.Enable` deletes it.
- **Sign-in.** A session is opened by one statement that inserts it only while the mark is absent, so a sign-in racing a disable cannot leave a session behind. A disabled person's right password answers 403 `disabled`, "ask the admin"; a wrong one answers as before, so the door does not tell a stranger who is disabled. A setup code for a disabled person is refused and not used.
- **Unattended work.** `unattended.Paused`, the one function the scheduler, the task runner, research and sub-agents ask, reads as on while the mark is there. So a disabled person's schedules are held and their tasks pause exactly as under their own switch (decisions 0343, 0390). Disabling then wakes the task runner (`jobs.Runner.PauseAll`), so a running round stops now. Enabling first moves every schedule's due time past now (`schedules.Scheduler.Rearm`), so nothing missed is made up, then clears the mark, then wakes the runner. The person's own switch is never touched: one who had paused their agents is still paused after enabling.
- **Share links.** `share.Usable` answers `ErrRevoked` while the mark is there, so a link answers 410 `revoked`; enabling restores every link that was live.
- The admin's view is handed the three switch functions by `internal/app` (`adminview.Options`), never the packages.

## Why

- In the person's own database, the mark and the end of their sessions are one transaction, and every place that decides already holds the person's store: no lookup in `keystone.json`, which a command-line process could rewrite with a stale copy.
- Reading it inside `unattended.Paused` reuses the pause switch's tested behaviour for every kind of unattended work, instead of a second hold in each runner.
- Leaving the person's own switch alone means enabling restores what they had chosen, not what the admin guesses.

## What it costs

- One more read of `tenant_meta` on every check of the pause switch and every share link: a primary-key lookup.
- A person who is disabled shows as paused in their own Operations, but they cannot sign in to see it.
- The share page says the owner turned the link off, which is near enough: the owner's account was turned off.
