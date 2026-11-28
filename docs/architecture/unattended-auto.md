# Auto for unattended work

Files: `internal/turn/unattendedauto.go`, `internal/jobs/auto.go`, `internal/subagents/auto.go`, and one start line each in `internal/jobs/runner.go` (`Start`), `internal/jobs/inchat.go` (`StartIn`), `internal/subagents/tool.go`, `internal/subagents/direct.go` (`StartParts`) and `internal/research/runner.go` (`Start`)

Sub-agents, background tasks and scheduled runs start on **Auto**, each of their turns routed afresh by the router, unless the call that started them names a model (P14-06). The design is `docs/plan/SPEC-router-and-admin.md`, Part 1, "Unattended work". Decisions [0730](decisions/0730-unattended-work-starts-on-auto-as-a-new-chat-does.md) to [0733](decisions/0733-held-parents-and-the-cap-on-auto-are-the-route-they-always-were.md) say why it is built this way.

## The shape

**Nothing new routes anything.** Unattended work already works through ordinary turns of a chat of its own: a background task's work chat ([jobs.md](jobs.md)), a scheduled run's own chat ([schedules.md](schedules.md)), a sub-agent's work chat ([sub-agents.md](sub-agents.md)), a research task's chat and its parts' ([research-mode.md](research-mode.md)). And since P14-04 a chat on Auto (`chats.auto`) has every turn that names no model routed by `router.Route` before its first call, the choice and its effort held for that turn's tool steps ([router.md](router.md), "For the turn"). So unattended work on Auto is two things at its start:

1. The chat it made for its turns is put on Auto.
2. Its turns name no model.

Then every round of a task, every run of a schedule and every sub-agent's turn is routed as a person's message on Auto is: from that turn's own message and facts, with a row of `router_verdicts` under the work's own request id, its decision call recorded under that request id and actor (fixed rule 4), a `route` event in its stream and a route on its replies. A task's carry-on round after its step limit is routed again, so a hard first round on a hosted step can be followed by an easy one on a local step.

## Who starts on Auto

`turn.Runner.StartOnAuto(ctx, store, chatID, named)` decides, on the chat just made:

| The call that started the work | `router.auto_default` | The person's ladder | The chat | Its turns name |
| --- | --- | --- | --- | --- |
| Names a model | any | any | off Auto | that model |
| Names none | on | has a step | **on Auto** | no model: each is routed |
| Names none | off | any | off Auto | the model they named before Auto |
| Names none | on | empty | off Auto | the model they named before Auto |

"Before Auto" is, for a background task, a research task and a sub-agent, the model the chat it was started from would use next; for a scheduled run, its chat's own model.

What each kind of work can name (decision [0731](decisions/0731-only-the-call-that-started-the-work-names-its-model.md)):

| Work | Names a model with | So on Auto when |
| --- | --- | --- |
| A background task | nothing: neither `POST /chats/{id}/tasks` nor `start_background_task` takes a model | always, when Auto is offered |
| A scheduled run | the schedule's `model` | the schedule has none |
| A sub-agent | `agents[].model` of `run_subagents` | it gives none |
| A research task, and its parts | nothing | always, when Auto is offered |

The pick of the chat the work was started from is not a named model: a task started from a chat on a model picked by hand runs on Auto.

The decision is made once, when the work starts, and kept on its chat, as a person's chat keeps it (decision [0732](decisions/0732-auto-is-decided-when-the-work-starts-and-kept-on-its-chat.md)). A ladder emptied afterwards leaves the chat on Auto with nothing to route by, so its turns use the chat's own model, as `openTurn` does for any chat on Auto with an empty ladder. A ladder set afterwards does not move work already started.

## Held parents and the cap

Nothing new: they hold on Auto because the router's step is the ask of the same route (decision [0733](decisions/0733-held-parents-and-the-cap-on-auto-are-the-route-they-always-were.md)).

- **A held parent's sub-agent** is held before its turn runs: `carry.Down` marks its chat when the turn is written (decision [0483](decisions/0483-a-held-sub-agents-result-reaches-only-a-held-parent.md)). The router reads the chat as held (`Facts.Held`), so its rule 1 leaves out every step that may not read sensitive content, the verdict is asked only of a decision model that may read it and offered no hosted step, and a verdict that names a hosted step anyway is not followed. With no step it may use, the turn fails with `sensitive`, or `local_unavailable` while the local machine is down, as a held chat's does.
- **At the cap or the run's budget** (decision [0392](decisions/0392-unattended-work-at-the-cap-falls-back-with-a-notice-or-stops.md)), the chosen step is opened through `routedChat`, whose chain asks the monthly cap and then the run's budget (`unattended.Gate`) before a call to a model that is not local. A refused step moves to the next model of the person's chain through `unattended.Moved`: with fallback on, the local model writes the reply and the run's chat is told; with it off, the run stops with `spend_cap`. The verdict row is kept either way, naming the step the router chose.

The router does not yet know about the cap: at the cap it may still choose a hosted step, which the gate then refuses. The fallback is the person's chain (`routing.fallback_models`), not the ladder's local steps. See "Not here yet".

## In code

    on, err := turns.StartOnAuto(ctx, store, chatID, named)  // on the chat just made; named is the model the call named, or ""
    turns.UnattendedAuto(ctx, store)                         // router.auto_default on and a ladder with a step

`internal/jobs` and `internal/subagents` each have an `onAuto` that calls it when a turn runner is bound. The callers:

- `jobs.Runner.Start`: after the work chat is made, named "". On Auto, the task's `model` is kept empty, so its rounds' `SendBody` names none.
- `jobs.Runner.StartIn`: named is the `model` it is given, the schedule's. Named none and on Auto, the run names none.
- `subagents.Runner.start` (the tool): named is `agents[].model`. Named none and off Auto, the sub-agent has the parent chat's model as before.
- `subagents.Runner.StartParts`: named is the caller's `model`.
- `research.Runner.Start`: named "". On Auto the task's `model` is empty, so its turns and its parts name none, and each part's chat is put on Auto by `StartParts`.

No migration, no setting, no route and no field of the API changed: `chats.auto` is the record, and the turn's own route, verdict and event follow. A sub-agent on Auto shows `model` empty, as before it showed empty for "the parent chat's"; its turn's route names the step.

## Settings

None of its own. It reads, through the turn runner, the router's settings in force for the person ([router.md](router.md), "Settings"): `router.auto_default` (tenant-admin) and `router.ladder` (tenant-admin). `agents.unattended_fallback` and the budgets of each kind ([unattended-spend.md](unattended-spend.md)) hold on Auto as off it.

## Checked by

With the fake provider, and the test tenant's recorded ladder (`recorded-small` local, `recorded-middle` trusted, `recorded-large` hosted, with `recorded-decisions`) and verdicts written in each test:

- `go test ./internal/schedules` (`auto_test.go`): **a scheduled run on Auto uses the router**: a daily schedule with no model, due while the server runs, has its run's chat on Auto, its reply from the step the verdict named at its effort, with a route; one verdict, under the run's task's request id, with the decision model, the step answered and its probability; the decision call and the chat call in the audit log as the schedule's, and the decision call's usage record.
- `go test ./internal/jobs` (`auto_test.go`): **a background task on Auto is routed each round**: two rounds (a step limit of one), two verdicts under the task's request id, the hard first round on the hosted step at high effort and the carry-on round back down on the local step at low effort, each decision call recorded with its usage record, and each reply with its route. A run whose caller names a model, and a task with no ladder, are off Auto with no verdict. **At the cap on Auto**: an invented person whose ladder is one hosted step and whose chain ends in a local model has the hosted call refused, the local model write the reply with the notice and the verdict kept; with fallback off the task stops with `spend_cap` and the local model is not called.
- `go test ./internal/subagents` (`auto_test.go`): **a held parent's sub-agent on Auto reaches no hosted model**: with a verdict that names the hosted step for every message, a sub-agent of a chat that is not held reaches the hosted step (so the check would see a router that refused everything); a sub-agent of a held chat is held, on Auto, makes no call to the hosted step, its one verdict was offered no hosted step, and the verdict kept is not on it. A sub-agent whose call names a model is off Auto with no verdict.
- `go test ./internal/research` (`auto_test.go`): a research task's own chat and both its parts' chats are on Auto, each routed, the task's plan and report included.
- `go test ./internal/turn` (`unattendedauto_test.go`): `StartOnAuto` with no ladder, with a ladder, with a model named, with `router.auto_default` off, after the ladder is emptied, and on a chat that is not the person's.

Not in `verify`: the held parent's sub-agent on Auto is proven by `go test ./internal/subagents` and the router's own cases in the `sensitive content` check, not by a case of its own through the real server.

## Not here yet

- **The router does not know about the cap.** At the monthly cap or a run's budget, Auto may still choose a hosted step for unattended work, which the gate refuses and the person's chain replaces. A ladder whose local step is not in the chain stops the run instead of moving down. An owner question (agents 016): skip hosted steps at the cap, as a down local machine's steps are skipped.
- **A background task cannot name a model.** `StartBody` and `start_background_task` take none, so a task is on Auto whenever Auto is offered.
- **The admin's policy** on what a named model may be (`router.allowed_models`, `router.may_switch_off`) is P14-05's: a schedule or a sub-agent that names a model is not yet checked against it.
- **Unattended work on Auto in Usage** is P14-07's: its verdicts are rows of `router_verdicts` like any other, under the work's request id.
- **A case in `verify`** through the real server for a held parent's sub-agent on Auto.
