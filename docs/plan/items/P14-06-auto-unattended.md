# P14-06. Auto for unattended work

Status: doing (agents)
Lane: agents
Depends on: P14-04, P8-01, P8-03
Owner gate: None.
Built without the gate: Everything.
Scope: Sub-agents, background tasks and scheduled runs start on Auto, each turn routed afresh, unless their caller names a model. Held parents and the cap behave as decisions 0483 and 0392 already say.
Proof: A scheduled run and a background task on Auto use the router and record verdicts. A held parent's sub-agent reaches no hosted model.

See `docs/plan/SPEC-router-and-admin.md`, Part 1, "Unattended work", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan

Branch `agents/p14-06-auto-unattended`, cut from `reboot` at `165ada6` (P14-04 merged).

The shape: unattended work already works through ordinary turns of a chat of its own (decisions 0340, 0480), and P14-04 keeps Auto on the chat (`chats.auto`, decision 0690): a turn of a chat on Auto that names no model is routed afresh by `router.Route` before its first call, every round and every tool step included. So nothing new routes anything. What this item adds is the start: the chat a piece of unattended work makes is put on Auto, and its turns name no model, when the call that started it named none.

1. **One function in the turn runner** (`internal/turn/unattendedauto.go`, new): `Runner.StartOnAuto(ctx, store, chatID, named)` puts a chat just made for unattended work on Auto when `named` is empty, `router.auto_default` is on and the person's ladder has a step, and says whether it did; `Runner.UnattendedAuto(ctx, store)` is the test without the chat. No migration and no setting: `chats.auto` is the record, and the turn's own route, verdict row and route event follow.
2. **Background tasks** (`internal/jobs`, new `auto.go` and a line in `Start`): the work chat starts on Auto, and the task's rounds name no model. A task cannot name a model today, so a task always starts on Auto when Auto is offered; the model of the chat it was started from is no longer carried (the call named none). Off Auto (no ladder, or `router.auto_default` off) it is as before: the parent chat's model.
3. **Scheduled runs** (`jobs.StartIn`): a schedule with no model has each run's chat on Auto; one with a model names it, off Auto, as before.
4. **Sub-agents** (`internal/subagents`): `agents[].model` absent puts the sub-agent's chat on Auto; named, it is used as before. Off Auto, absent still means the parent chat's model. Research parts (`StartParts`) and the research task's own chat follow the same rule, since a research task is a background task and its parts are sub-agents.
5. **Held parents** (decision 0483): nothing new. `carry.Down` holds a held parent's sub-agent's chat before its turn runs, and the router's rule 1 then leaves out every step that may not read sensitive content, whatever the verdict says. **The cap** (decision 0392): nothing new either. The step the router chose is the ask of the same gated route (`routedChat`), so the monthly cap and the run's budget refuse a step that is not local, and `unattended.Moved` falls back to the next model with a notice, or stops.
6. **Proof**, with the fake provider and the test tenant's recorded ladder (`recorded-small` local, `recorded-middle` trusted, `recorded-large` hosted, `recorded-decisions`):
   - `go test ./internal/schedules`: a scheduled run with no model, on the recorded ladder, is routed by the router: its turn has a route, a verdict row under the run's request id, a decision call recorded with its usage record; a schedule naming a model is not on Auto and records no verdict.
   - `go test ./internal/jobs`: a background task on Auto is routed for each round, with a verdict row for each under the task's request id; with no ladder it uses the model it used before. At the cap on Auto, the chosen hosted step is refused and the local fallback writes it with the notice; with fallback off it stops.
   - `go test ./internal/subagents`: a held parent's sub-agent on Auto, given a verdict that names the hosted step, reaches no hosted model: no audit entry for it, the verdict offered no hosted step, the verdict row not on it. A sub-agent of a chat that is not held, given the same verdict, reaches the hosted step, so the check would notice one that refused everything. A sub-agent that names a model is not on Auto.
   - `go test ./internal/turn`: `StartOnAuto` with and without a ladder, with `router.auto_default` off, and with a model named.
7. **Write-up**: `docs/architecture/unattended-auto.md` with its `componentDocs` line and overview row, decisions from 0730, journal `docs/journal/2026-11-28-agents-016.md`, and lines added to `router.md`, `sub-agents.md`, `jobs.md` and `schedules.md` where they said Auto for unattended work was not here yet.

Left to others: the admin's refusals of a named model (P14-05), Auto in usage (P14-07), the live check (P14-08).
