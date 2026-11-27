# P11-04. Settings at a glance

Status: doing (interface)
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
