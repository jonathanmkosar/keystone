# 0633. Decision calls are chosen and recorded by a guard package of their own, with no fallback to another decision model, and a local one costs nothing

November 27, 2026 · decided in P14-02

## The question

Every decision call needs a started and an end entry in the audit log and a usage record of the kind `decision`, and a sensitive one must never reach a hosted entry. The embedding calls do the same inside `internal/turn`, next to the settings they read. The router that will make decision calls (P14-03) is not built yet. Where does the recording live, how is the model chosen, and what does a call cost?

## The decision

- **A package of its own, `internal/decision`**, with two functions. `Choose(name, sensitive, usable, can)` asks `routing.Plan` with the kind `decision`, so the guard that holds every route holds this one. `Run(ctx, store, opened, call, options)` makes the call and writes the started entry, the end entry and the usage record, for a refused or failed call as much as an answered one.
- **It is a guard of fixed rule 3**, listed in `fixedRuleGuards`: it imports no setting and no adapter. The model's name, the sensitive mark, the gate (the cap) and the estimate (the budget) are arguments. `Run` refuses a sensitive call to a model that may not read sensitive content a second time, whatever chose the model, and records it as refused.
- **No fallback.** `Choose` returns the model named or none. With none, the router decides by rules, as the spec says it does when the decision model cannot be had.
- **A local decision model costs nothing**: its cost is written as 0 whatever its price says. Another is priced at its input and output tokens and gated by the cap the caller passes.
- **A request that is not valid is not a call**: it is refused before anything is recorded, as an embedding request is.

## Why

- The router, the chats lane's Auto turn and unattended work will all ask for verdicts; one place that records them means none can forget a record.
- Taking the gate and estimate as arguments keeps the package out of the settings, which is what lets `verify` hold it to the fixed rules.
- A second decision model behind the first would add a wait to a call whose whole worth is speed.
- The spec says a local model costs nothing; writing 0 means an entry priced by mistake cannot spend the person's cap on verdicts.

## What it costs

- The caller opens the model itself (`adapters.OpenDecider`), since this package may not import the adapters.
- The started entry of a refused sensitive call holds the state in the person's own audit log, as every refused call's request does there; it never leaves the machine.
