# 0670. The router asks routing.Plan for each step on its own, and reads nothing itself

November 27, 2026 · decided in P14-03

## The question

The spec says the router only ever narrows or orders what `routing.Plan` allows, and that a held chat on Auto never reaches a hosted model, whatever the verdict says. The item says the router reads nothing itself: the turn passes it the settings and the facts. How is "only narrows" made true in code rather than by care, and how does a package that needs nine settings read none?

## The decision

- **Each step of the ladder is asked of `routing.Plan` alone**, as a chat call to that model, with the held mark as `Sensitive` and the call's needs as `Needs`, and no rules or fallback. A step is a candidate only when the plan's route starts with it. Rules 1 and 2 (held chat, needs), and the skip of a name not in the registry, are therefore the plan's own filters, and their reasons are the plan's (`sensitive`, `capability`, `unknown`). The router checks rule 1 once more itself, and checks the step it returns a last time.
- **`internal/router` is a guard of fixed rule 3**, listed in `fixedRuleGuards`. It imports neither `settings` nor `internal/tenantsettings`, itself or through another package. It imports `internal/decision`, already a guard, and chooses a held chat's decision model with `decision.Choose`, so that guard decides who may read a held chat's verdict.
- **The turn reads the settings.** `router.Load` takes a function that returns a key's value, and parses and checks every key in `router.Keys()`. The facts, the clock (`Facts.Now`) and the decision call (`Input.Verdict`) are arguments too.

## Why

- Asking the plan for every step makes "the router never adds a model the plan would withhold" a property of the code: any step the plan withholds for any reason, today's or a later one, is never a candidate. A test holds every candidate to the plan's answer for the same ask.
- A guard that cannot import a setting cannot have one that widens a held chat's ladder; `verify` already proves this for `routing` and `decision`.
- Taking a reader keeps the setting keys and their parsing next to the code that uses them, without the import.

## What it costs

- One `Plan` per step per message: a few map lookups on a ladder of a handful of names.
- The window check cannot use the budget, which reads settings (decision [0674](0674-the-routers-window-check-mirrors-the-budgets.md)).
