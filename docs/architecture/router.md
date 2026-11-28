# The router

Folder: `internal/router`

The router (P14-03) chooses the model and the effort for one message of a chat on **Auto**. It narrows the admin's ladder with four rules, asks a decision model for a verdict when more than one step is left, and gives the turn one step and one effort. That step's model is then the ask the turn gives `routing.Plan`, where the privacy filter, the capability filter and the fallback chain work as before ([routing.md](routing.md)). The design is `docs/plan/SPEC-router-and-admin.md`, Part 1, "The ladder" to "The verdict".

The router only ever narrows or orders what `routing.Plan` allows. It never adds a model to a route the plan would withhold.

The package reads nothing itself: no store and no setting. The turn passes it the settings, the facts of the call (the time among them) and a function that makes the decision call; the clock is read only to time the verdict, and for `Facts.Now` when the turn leaves it zero. It holds no state between calls. The turn calls it since P14-04 ("For the turn", below).

## Every message afresh

Every message on Auto is routed afresh, before its call, from that message and its call's facts. Nothing from an earlier message holds a chat on a step: a hard message followed by an easy one goes back down. The turn holds the choice for that message's whole turn, every tool step included, since a thinking block goes back only to the model that wrote it. The step the previous message used is one of the facts, shown to the verdict and used for the reason line, and it never limits the choice.

## The ladder

`router.ladder` is a list of registry names, smallest first, separated by commas. A name may carry a size limit, `name<=100000`: that step is skipped when the call's estimated size is over the limit. An example, in docs only:

    local-chat, small-hosted<=100000, mid-hosted, large-hosted

`ParseLadder` reads it. It refuses a limit that is not a whole number of tokens above zero, a limit with no name, and a name written twice. A name that is not a usable model in the person's registry is skipped when the message is routed. An empty ladder means Auto is not offered: `Route` refuses with `no_ladder`, and the chat uses the model picked.

## The rules, in order

`Candidates(settings, facts, usable)` applies them to each step and returns what is left, the **candidates**, in the ladder's order, with every step left out and why (`Skip`).

| Rule | A step is skipped when | Why it says |
| --- | --- | --- |
| 1. Held chat | The chat is held for sensitive content (decision 0211) and the model may not read sensitive content (`models.Model.MayReadSensitive`). | `sensitive` |
| 2. Needs | The call holds an image and the model lacks `vision`, offers tools and it lacks `tools`, or holds a file and it lacks `files`. A model that takes no chat calls, an embedding or a decision model, is skipped the same way. | `capability` |
| 3. Size | The call's estimated tokens are over the step's size limit. | `size` |
| 3. Size | The estimate and the room kept for the reply do not fit the model's window: the budget's own check (`budget.Measure`), which a test holds the router to. | `window` |
| 4. Local machine down | The step is local, and a call to a local endpoint, a decision call included, failed with `unavailable` within `router.local_down_for`. The reply carries the notice `local_unavailable`. | `local_down` |

A name not in the registry is skipped as `unknown`.

Rules 1 and 2 are not code of the router's own: each step is asked of `routing.Plan` alone, as a chat call to that model with the held mark as `Sensitive` and the needs as `Needs`, and is kept only when the plan routes the call to it. The router then checks rule 1 once more itself.

With no candidate left, `Route` refuses (`*Refusal`, whose message is safe to show):

| Code | When |
| --- | --- |
| `local_unavailable` | A step was skipped because the local machine is down. A held chat whose only steps are local waits and says why: it never moves to a hosted model. |
| `sensitive` | The chat is held and every step it could use is hosted. `errors.Is(err, routing.ErrSensitive)` is true, so the turn sorts it as it always has. |
| `no_model` | No step can take the call. |
| `no_ladder` | The ladder is empty. |

## The verdict

With one candidate there is no verdict. With two or more:

1. The decision model is the registry entry `router.decision_model` names, chosen through `decision.Choose` with the held mark as sensitive, so a held chat's verdict goes only to a decision model that may read sensitive content ([decision-calls.md](decision-calls.md)).
2. It is shown the newest message's text, cut to `router.verdict_max_chars` characters, and the call's facts: estimated tokens, whether it holds images or files, how many tools are offered, the step the previous message used, and how many turns the chat has. Never the rest of the chat.
3. It is asked two questions. `step`: a choice among the candidates, each described by its entry in `router.step_notes`, or "<label>: step n of m, smallest first". `effort`: `low`, `medium` or `high`.
4. The call is the turn's `Verdict` function, under a context that ends at `router.verdict_timeout`.

Then:

- The step chosen, unless its probability is under `router.min_confidence`: then the request moves one candidate up. On the top candidate it stays.
- The effort chosen, never above `router.max_effort`.

The rules alone decide, with the lowest candidate at `router.default_effort` (capped the same way), when:

| `Record.RulesOnlyWhy` | When |
| --- | --- |
| `one_candidate` | One step is left. |
| `no_decision_model` | No decision model is named, or the turn passed no `Verdict`. |
| `decision_model_unusable` | The name is not a usable decision model this build can open. |
| `held` | The chat is held and the decision model may not read it. |
| `local_unavailable` | The decision model is local and the local machine is down, known from the facts or found by this verdict failing with `unavailable`. Local steps are then skipped for this message, with the notice. |
| `timeout` | No answer within `router.verdict_timeout`. A late answer is not used. |
| `error` | Any other failure, or an answer that does not answer both questions, or names a step that is not a candidate. |

A verdict that times out is not taken as the local machine being down: a decision model can wait behind the chat model on the same machine. If the chosen local step then fails with `unavailable`, the turn calls `Route` again with `LocalFailedAt` set (below).

A turn the person stopped while the verdict was asked returns the context's error.

A held chat's result is checked a last time before it is returned: a model that may not read sensitive content is refused as `sensitive`, whatever the verdict said. `Candidates` never leaves one, so this never happens; it is there so that nothing a verdict says can put one back.

## The reason line

One line, written by Keystone and never by a model:

| Case | Reason |
| --- | --- |
| A verdict | `Local model, effort low, decision 0.91` |
| Moved up | `Moved up: decision unsure (0.48)` |
| Rules only | `Rules only: local machine not answering`, `Rules only: no decision within 2s`, `Rules only: one step can take this message`, `Rules only: no decision model`, `Rules only: the decision model cannot be used`, `Rules only: the decision model may not read this chat`, `Rules only: the decision failed` |
| Try a stronger model | `Stronger model asked for` |

When the step is not the one the previous message used, the line ends `; switched from <label>` and `Result.Switched` is true: the call loses that provider's prompt cache, which Usage shows.

## For the turn (P14-04)

    s, err := router.Load(func(key string) (string, bool) { /* the person's value of key */ })
    res, err := router.Route(ctx, router.Input{
        Settings:  s,
        Facts:     router.Facts{Text: text, Tokens: estimate, ReplyTokens: maxOutput, Images: images, Files: files,
                               Tools: len(tools), PreviousStep: last, Turns: n, Held: held,
                               LocalFailedAt: lastLocalUnavailable, Now: time.Now()},
        Usable:    usable,                                                // models.Usable
        CanDecide: func(m models.Model) bool { return set[m.Adapter] != nil && adapters.Decides(m.Adapter) },
        Verdict: func(ctx context.Context, m models.Model, sensitive bool, req seam.DecisionRequest) (seam.DecisionResult, error) {
            m, d, err := adapters.OpenDecider(ctx, store, set, m.Name)
            if err != nil { return seam.DecisionResult{}, err }
            return decision.Run(ctx, store, decision.Opened{Model: m, Decider: d, Secrets: keys},
                decision.Call{RequestID: id, Actor: actor, Sensitive: sensitive, Request: req},
                decision.Options{Gate: capGate, Estimate: estimate})
        },
    })
    // res.Model.Name is the ask's model for routing.Plan, res.Effort goes on seam.Request.Effort,
    // res.Reason, res.Notice and res.Record go on the turn's record, router_verdicts and the route event.

- `Load` reads every key in `router.Keys()` through the function given and refuses a value that does not parse or that `Settings.Check` refuses. The package does not import `settings`; the turn reads the values.
- `Facts.Tokens` is the budget's estimate of the call's input and `ReplyTokens` the reply's room the call asks for (zero for the model's output limit), so the window check is the budget's.
- `Facts.LocalFailedAt` is the turn's to keep: the last time a call to a local endpoint, chat or decision, failed with `unavailable`. `Result.Record.LocalUnavailable` says this message's decision call found the machine down. When the chosen local step fails with `unavailable` before any block of its reply was passed on, the turn may route the message again with `LocalFailedAt` set: the local steps are skipped, the notice is set, and a local decision model is not asked again.
- The verdict is a recorded call: `decision.Run` writes its two audit entries and its usage record of the kind `decision` (fixed rule 4).
- `Result.Record` is a `router_verdicts` row less its ids and time: the decision model, the facts shown (`Shown`, without the text), the step answered and its probability, the effort answered, the step and effort chosen, `moved_up`, `timed_out`, `rules_only` and why, and the latency in milliseconds. `stronger_asked` is set by the `stronger` route on the verdict it rewrites.
- `Stronger(input, answered, effort)` is "Try a stronger model": the first candidate above the step that answered, at that effort, capped. It is refused with `no_stronger` on the top candidate, or when the step that answered is no longer on the ladder.
- A `*Refusal`'s code is the turn's code; `sensitive` is the one the turn has always used.

### How the turn wires it (P14-04)

`internal/turn/auto.go` is the wiring above, as built ([turns.md](turns.md), "Auto"):

- `openTurn` takes the place of the turn's `open`. A chat on Auto (`chats.auto`) with a ladder is routed by `Route` before its first call; a chat off Auto, or with an empty ladder, opens the model picked as before. A message sent with `auto` puts the chat on Auto, one that names a model takes it off (decision [0690](decisions/0690-a-chats-auto-is-kept-on-the-chat-and-changed-by-the-message-that-picks.md)).
- `Facts` are read before the call is built, from the branch and the turn's own message (decision [0691](decisions/0691-the-routers-facts-are-read-before-the-call-is-built.md)); a need no step of the ladder can meet is left out (decision [0692](decisions/0692-a-need-no-step-can-meet-is-not-a-need.md)). `Usable` is the person's usable models whose adapter the build has, decision models included; the test tenant's are its recorded ones (decision [0695](decisions/0695-the-test-tenant-has-a-recorded-ladder-that-ships-switched-off.md)).
- `Verdict` is `adapters.OpenDecider` and `decision.Run`, with the monthly cap as its gate for a decision model that is not local; the test tenant's verdicts are its recorded ones, through `decision.Run` all the same.
- The chosen step's model is opened as the ask's model (`routedChat`, so `routing.Plan`, the privacy filter and the fallback chain), and `Result.Effort` goes on every request of the turn (`seam.Request.Effort`), tool steps and a turn resumed after an approval included: that one reads its route back from its row and is not routed again.
- `LocalFailedAt` is kept by the runner for each tenant, in memory. When the first call of a turn on Auto fails with `unavailable` on a local model before anything is passed on, the message is routed again once with it set (decision [0693](decisions/0693-a-local-step-that-does-not-answer-is-routed-again-once-before-anything-is-passed-on.md)).
- `Result.Record` is written as a row of `router_verdicts` with the reason and the notice, and the turn's row keeps `auto`, the step, the effort, the reason, the notice and the verdict's id. `Stronger` serves `POST /api/v1/chats/{id}/turns/{turn}/stronger` (decision [0694](decisions/0694-a-stronger-model-writes-the-whole-turn-again-from-the-persons-message.md)).
- A `*Refusal` ends the turn with its code and message: `local_unavailable` (retryable), `sensitive`, `no_model`.

## Settings

In `settings/models.json`. Names, types, defaults and scopes are the spec's.

| Key | Type | Default | Scope | Meaning |
| --- | --- | --- | --- | --- |
| `router.ladder` | string | empty | tenant-admin | The ladder, as above. |
| `router.decision_model` | string | empty | tenant-admin | The registry name of the decision model. Empty: the rules alone decide. |
| `router.step_notes` | string | empty | tenant-admin | Descriptions of steps for the verdict, as `name=words`, separated by semicolons. |
| `router.min_confidence` | float | 0.6 | install | The probability under which a verdict moves one candidate up. |
| `router.verdict_timeout` | duration | 2s | install | The longest the verdict may take. |
| `router.verdict_max_chars` | int | 8000 | install | How much of the newest message the verdict sees. |
| `router.default_effort` | string | medium | install | The effort when the rules alone decide: low, medium or high. |
| `router.max_effort` | string | high | install | The most effort the router asks for: low to max. |
| `router.local_down_for` | duration | 2m | install | How long local steps are skipped after a local endpoint failed with `unavailable`. |

`router.auto_default`, `router.may_switch_off` and `router.allowed_models`, the rest of the admin's policy, are read by the turn and the chat routes, not by the router, and come with P14-04 and P14-05 (decision [0672](decisions/0672-the-router-adds-the-settings-it-reads-and-leaves-the-rest-of-the-policy.md)). P14-04 added the one it reads:

| Key | Type | Default | Scope | Meaning |
| --- | --- | --- | --- | --- |
| `router.auto_default` | bool | true | tenant-admin | A chat made from now on starts on Auto. No effect while the ladder is empty. |

## Decisions

- [0670](decisions/0670-the-router-asks-the-plan-for-each-step-and-reads-nothing-itself.md): each step is asked of `routing.Plan` on its own, and the router reads nothing itself.
- [0671](decisions/0671-a-late-verdict-is-rules-only-and-only-unavailable-means-the-local-machine-is-down.md): a late verdict is the rules alone; only `unavailable` means the local machine is down.
- [0672](decisions/0672-the-router-adds-the-settings-it-reads-and-leaves-the-rest-of-the-policy.md): the router adds the settings it reads.
- [0673](decisions/0673-the-verdict-chooses-among-candidates-and-moving-up-is-the-next-candidate.md): the verdict chooses among the candidates, and moving up is the next candidate.
- [0674](decisions/0674-the-routers-window-check-mirrors-the-budgets.md): the window check mirrors the budget's.

## Fixed rule 3

The router is a guard of fixed rule 3, listed in `fixedRuleGuards` in `tools/verify/fixedrules.go`: it imports neither `settings` nor `internal/tenantsettings`, itself or through another package, so no setting can widen what it gives a held chat. Its window check therefore mirrors the budget's rather than importing it.

## Checked by

`go test ./internal/router`:

- The ladder and the step notes parse, and each malformed one is refused; `Load` reads every key and refuses each bad value; the registry has each setting with the spec's type, default and scope.
- Each rule, in a table, on a ladder of local, trusted and hosted models: the held chat, each need, the size limit at and over it, the window with and without a smaller reply, the local machine within and after `router.local_down_for`, an unregistered name, and the rules' order.
- Every candidate is a model `routing.Plan` routes the same ask to.
- Each refusal and its code, with no verdict asked.
- The verdict: the step it names, the confidence step (under, at, over a skipped step, on the top step), the effort cap, and the reason lines with "switched from".
- The rules-only paths: timeout, an answer after the timeout, a failure, an unavailable hosted decision model, an answer naming a step that is not a candidate, an answer missing a question, no decision model, no verdict function, an unusable decision model, one candidate, and a held chat with a hosted decision model.
- A held chat never gets a hosted step, whatever the verdict says: every model as the verdict's answer, sure and unsure, on three ladders, with each decision model and with the local machine up and down; its verdict goes only to the local decision model, marked sensitive, and is offered no hosted step.
- The local machine not answering: the decision call finding it down, a held chat moving to a trusted step, a held chat with local steps only waiting, and a known-down machine asking no local decision model.
- What the verdict is shown, and the two questions; a stopped turn; bad settings; `Stronger`.
- The window check agrees with `budget.Measure`.

In `verify`: the `sensitive content` check routes a held chat with a verdict naming each hosted step, a held chat with a hosted decision model, a held chat with hosted steps only, and a held chat whose local machine is down, and proves none reaches a hosted step; and a chat that is not held follows the verdict (`tools/verify/router.go`). The `fixed rules` check lists `internal/router` as a guard of rule 3.

In `verify` since P14-04, the same check sends a message on Auto through the real server in a held chat of the test tenant, with a recorded verdict naming its hosted step, and proves no call of the turn went to it and the verdict was offered no hosted step; the same message in a chat that is not held reaches the hosted step (`tools/verify/autoturn.go`). `go test ./internal/turn` holds the rest of P14-04's proof (`auto_test.go`).

## Not here yet

- **The admin's policy** beyond the ladder, the decision model, the step notes and `router.auto_default` (P14-05).
- **Unattended work** on Auto (P14-06) and **Auto in usage** (P14-07).
  - Built in P14-06 ([unattended-auto.md](unattended-auto.md)): sub-agents, background tasks, scheduled runs and research tasks start on Auto unless the call that started them names a model, through `turn.Runner.StartOnAuto`; each of their turns is routed by the turn as above. Nothing in this package changed.
- **The live check** with the owner's decision model and chat model on his local machine, and his hosted models (P14-08).
