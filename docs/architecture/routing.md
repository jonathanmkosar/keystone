# Routing

Folder: `internal/routing`

Routing decides which models one call may be sent to, and in what order. It has three parts: a rule for each kind of task, a privacy filter for sensitive content, and a fallback chain for an endpoint that fails.

The package holds no state and reads no store and no setting. Everything it needs is an argument. The turn runner reads the settings and joins it to a turn ([turns.md](turns.md)); the code for that is `internal/turn/route.go`.

## The kinds of task

| Kind | What it is | Its rule |
| --- | --- | --- |
| `chat` | A reply in a chat. | The model the person picked. No setting overrides it. |
| `title` | A chat's title. | `chats.title_model` |
| `summary` | A summary of a chat. | `routing.summary_model` |
| `memory` | The memory pass after a turn. | `routing.memory_model` |
| `embedding` | Text into a vector. | `routing.embedding_model` |

Only `chat` and `title` are called today. The other three have a rule waiting for the items that make those calls (P5-05, P6-06, P6-08).

## A route

`routing.Plan(rules, ask, models)` returns a `Route`: the models to try, in order.

1. The model the kind's rule names. Chat has none.
2. The model the ask names: the one the chat runs on.
3. The fallback chain, `routing.fallback_models`, in the order written.

Each model is in a route once. A route holds its first model and at most `routing.max_fallbacks` more.

A named model is left out, and listed in `Route.Withheld` with the reason, when:

- `unknown`: the tenant has no usable model of that name. A rule that names nothing usable therefore falls through to the chat's model.
- `sensitive`: the call is sensitive and the model may not read sensitive content.
- `capability`: the model lacks something the call needs. An embedding model takes embedding calls and nothing else.
- `limit`: the route is full.

With nothing left, `Plan` returns `ErrNoModel`. When a model was withheld as sensitive, the error also wraps `ErrSensitive`.

## Privacy routing

This is fixed rule 3: content from a sensitive space reaches only models the registry marks local or trusted.

- The answer for one model is `models.Model.MayReadSensitive()` ([models.md](models.md)), and nothing else is asked.
- An ask with `Sensitive` set keeps only the models for which that is true. No rule and no fallback can put another model back, because the filter runs on every name.
- `NewChain(sensitive, links, hooks)` checks again, and refuses to build a chain that holds a model which may not read sensitive content.
- The package imports neither `settings` nor `internal/tenantsettings`, itself or through another package. It is listed in `fixedRuleGuards` in `tools/verify/fixedrules.go`, and the `fixed rules` check fails if that import ever appears ([tools.md](tools.md)).

Nothing marks a turn as sensitive yet. The turn passes `false` until recall (P6-05) brings content from a sensitive space into a turn. See "Not here yet".

## The fallback chain

`routing.Chain` is a `seam.Provider` over a route's opened models. The turn gets one chain and calls it as it would call one model.

- The first model is called. If it answers, that is the reply.
- If it fails **before any block of its reply has been passed on**, and the failure is one another model may not share, the next model is called with the same request.
- Once a block has been passed on, a failure is the call's failure. Half a reply from one model is never followed by another model's.
- A call the person stopped is not tried again.

The start of a stream and its error event are held back until the first block arrives, so a client never sees a failed attempt's events.

| The failure | Falls back |
| --- | --- |
| `unavailable`, `overloaded`, `rate_limited` | Yes |
| `auth` | Yes: another model has another key |
| `too_long` | Yes: another model may have a larger window |
| `spend_cap` | Yes: a local model costs nothing ([usage.md](usage.md)) |
| `invalid`, `other` | No: the trouble is with the request |

Each model is called once. An adapter already tries its own endpoint again (twice by default), and the chain does not add to that: with two fallbacks, the most attempts one call makes is three models times each adapter's own.

`routing.For(model, request, other)` fits the request to the model it goes to: the model's name, no more reply than the model writes, no thinking for a model without it, and, for a model the request was not built for, no thinking blocks, since a thinking block goes back only to the model that wrote it.

### Hooks

A chain asks its caller two things. Both are arguments.

- `Gate(ctx, model, request)` before each model is called. A non-nil answer is why the call may not be made. The monthly cap is the gate ([usage.md](usage.md)).
- `Moved(ctx, failed, next, request)` when a model did not answer and the next is about to be tried. The turn records the failed call in the audit log and the usage records, and writes the next call's started entry. If that cannot be written the call ends: a call that cannot be recorded is not made.

`Chain.Last()` is the model the most recent call ended on. The turn uses it for the cost, the audit entry and the reply's own record, so a reply written by a fallback says so. The turn's `model` stays the one asked for.

A chain also counts tokens with its first model's provider when that provider offers a count, so it is a `budget.Counter` ([budget.md](budget.md)).

## In a turn

`internal/turn/route.go`:

1. The picked model is opened. If it cannot be, that is the turn's error, as before.
2. The rules are read from the tenant's settings and the route is planned over the tenant's usable models.
3. Each other model of the route is opened behind `budget.Guard`. One that cannot be opened is left out and logged.
4. The chain is made with the cap as its gate.

The test tenant has no route. It always gets its recorded turns (decision [0144](decisions/0144-the-test-tenants-model-is-in-no-registry.md)).

## Settings

In `settings/models.json`, with `chats.title_model` in `settings/chats.json`.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `routing.summary_model` | empty | tenant | The model that writes summaries. |
| `routing.memory_model` | empty | tenant | The model that runs the memory pass. |
| `routing.embedding_model` | empty | tenant | The model that makes vectors. |
| `routing.fallback_models` | empty | tenant | Registry names, separated by commas, tried in order after the first model. |
| `routing.max_fallbacks` | `2` | install | The most other models one call is sent to. |

An empty rule uses the chat's own model. An empty chain means nothing falls back, which is how Keystone ships.

## In code

```go
route, err := routing.Plan(rules, routing.Ask{Kind: routing.KindChat, Model: "example-large", Sensitive: false}, usable)
chain, err := routing.NewChain(route.Sensitive, links, routing.Hooks{Gate: gate, Moved: moved})
err = chain.Stream(ctx, request, emit)
answeredBy := chain.Last()
```

## Checked by

`go test ./internal/routing ./internal/turn`.

- Each kind follows its rule, chat ignores a rule, and a rule that names nothing usable falls to the chat's model.
- A sensitive ask is routed to local and trusted models only, for every kind, and with only hosted models there is no route.
- A model that fails before its reply begins is followed by the next one, each called once, and the client sees none of the failed attempt.
- A reply that breaks off part of the way is not followed by another model. Neither is an invalid request or a stopped call.
- A gate's refusal moves to a model the gate allows, and is the call's error when there is none.
- In a turn: a failing endpoint falls back, the audit log holds a started and an end entry for each of the two calls, and the reply is recorded under the model that wrote it.

In `verify`: the `fixed rules` check lists `internal/routing` as a guard for rule 3.

## Not here yet

- **The sensitive mark on a turn.** The filter is built and tested, and the turn passes `false`, because nothing yet brings sensitive content into a turn. Recall (P6-05) sets it, and adds the proof that a sensitive page never reaches a hosted model.
- **Calls of the kinds `summary`, `memory` and `embedding`.** Their rules are settings; the callers come with P5-05, P6-06 and P6-08.
- **A cost ceiling on one call**, which the plan lists among what a call states. The monthly cap is the only ceiling today.
- **Routing by capability from a turn.** `Ask.Needs` works, and the turn does not fill it yet: a message with an image does not yet ask for vision.
- **A chain for each kind.** There is one fallback chain for a tenant.
- **The router app.** It plugs in as the source of `Rules` or as one more endpoint.
