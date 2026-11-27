# P11-04. Settings at a glance

Status: provisional
Lane: interface
Depends on: P11-01
Owner gate: none
Scope: Asked for by the owner on November 26, 2026. The settings screens show each setting with a short plain line, and keep the long explanation one step away. Several approaches are tried and the best for reading and for looks is chosen.
Proof: Every setting shows a short line of its own and its full explanation on request, at desktop and phone width, with nothing lost from the registry.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

The registry has 326 settings today, 141 of them a person's own, and General draws each with its key, its whole description, its control and the word "Default". On a phone that is a page nobody reads.

1. **The words.** Two new fields in the registry beside `description`, written for every setting in every `settings/*.json`: `title`, the setting's readable name, and `summary`, one short plain line of about ten words. Each is a line of its own directly under the entry's `key` line. No key, type, default, scope or description changes. The registry still reads an entry without them, so a lane that adds a setting meanwhile breaks nothing that runs; a new function, `settings.CheckWords`, lists every entry whose title or summary is missing or does not fit, and `verify`'s `settings registry` check calls it.
2. **The route.** `GET /api/v1/settings` gains `title` and `summary` on each setting. `docs/api/openapi.json` is written again from the routes.
3. **Three sketches** of General with the real registry, behind `?sketch=` while the item is built, looked at in a browser at phone and desktop width, light and dark: (a) the short line with the long text opened in place, (b) the short line with an info button that opens a sheet on a phone and a popover on a desktop, (c) a compact list that opens a view of its own for each setting. One is chosen in a decision record, and the other two are removed.
4. **The screen.** What it works out (the groups and their plain headings from key prefixes, the search over titles, short lines and keys, the changed mark, the fallbacks for an entry with no title or summary) goes in a file with no imports, `web/app/src/settingsView.ts`, so Node's own test runner proves it against the real `settings/*.json` (decision 0501). `Settings.tsx` draws from it.
5. **The proof.** `settings.CheckWords` on the real registry, in `go test ./settings` and in `verify`; the route test extended to the two fields; `web/app/test/settingsView.test.ts`: every setting of the registry is in exactly one group of exactly one list, is found by its title, its short line and its key, and carries its short line and its full description; a source check that the screen's text boxes are 16px on a phone and nothing takes the focus by itself; and a look in a browser at phone and desktop width, in both themes.
6. **The write-up.** `settings-screens.md`, `settings.md`, decisions from 0560, the journal entry interface 010, this item's Result.

Left when this plan was written: everything.

### Result

Provisional on the owner's look at the screen on his phone: there is no mockup for it, and he asked for the best of several approaches to be picked. Everything in the plan is built and its proof passes.

- **The words.** Every one of the registry's settings (346 when the screen was built, 386 after the last merge) has a `title` and a `summary` beside its `description`, two added lines under its `key` line, in every `settings/*.json`. `settings.CheckWords` holds them to a shape (a title of at most 40 characters, a summary of 3 to 14 words and at most 80 characters, no key in either), and `verify`'s `settings registry` check runs it. Reading the registry does not ask for them, so a setting added without them still runs and still has a row (decision 0561).
- **The route.** `GET /api/v1/settings` carries both.
- **The sketches.** Three, on the real registry, in commit a12b53d: "More" in place, an info button with a sheet or a popover, and a compact list with a view for each setting. Decision 0560 has what each showed at phone and desktop width, in both themes, and why it lost.
- **The screen.** The compact list's rows, opening in place: a name and a value on one line, the short line under them, a switch flipped where it is, and the whole row opening for the full explanation, the box to change the value in, the default with "Reset to default", and the key with Copy. A "Changed" mark and filter in place of "Default" under every control, a search over names, short lines and keys, and 34 plain headings worked out from key prefixes (decision 0562). No overlay, nothing takes the focus by itself, and every box of the settings screens is 16px at phone width.
- **The proof.** `go test ./settings` (every setting has a title and a summary); `TestEveryOverridableSettingHasAControl`, extended (the route gives each its title, summary and description); `web/app/test/settingsView.test.ts`, ten tests against the real `settings/*.json` (each row shows its short line and not its description, gives the description once opened, is in exactly one group, is found by name, short line and key); the new `settings at a glance` check, which runs the last two and holds the screen's source to them; and a look in a browser at 375 and 1280 pixels, where the page's 346 rows were compared with the route's answer. `verify` is 42 of 42.
- **The merges.** `reboot` was merged in three times while the item was built, last at 5f6d798. The settings each merge brought got a title and a summary at it: eleven `agents.research_*` and the nine of `settings/visuals.json` at e3e5404; the 36 of `settings/loadtest.json` and `chats.table_page_rows`, `chats.table_select_max`, `chats.delete_batch_max` and `chats.search_rank_max` at aed8214; none were new at 5f6d798. All 386 settings have both, and `verify` is 44 of 44. A look in the browser after the last merge (386 rows for the route's 386, each opened in turn) found thirteen settings whose summary is their whole description, which an opened row said twice; it now says it once, with a test for it.
- **Not verified:** a real phone and its keyboard, a screen reader by ear, and browsers other than the app's own pane. The journal, interface 010, lists it all.

Docs: `docs/architecture/settings-screens.md` ("How General is drawn", "The proof of P11-04"), `docs/architecture/settings.md` ("Titles and summaries"), decisions 0560 to 0562, and the journal entry `docs/journal/2026-11-26-interface-010.md`.
