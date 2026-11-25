# P6-06. Memory pass

Status: done
Lane: knowledge
Depends on: P6-05
Owner gate: none
Scope: After each turn a local model extracts durable facts and adds them as a source linked to the chat; its own spend reserve; skipped in incognito.
Proof: Test-tenant chats produce their expected facts; the reserve holds when the cap is reached.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Plan (knowledge lane, branch `knowledge/p6-06-memory-pass`):

- **A package of its own, `internal/knowledge/memory`**, a guard of fixed rule 3 that reads no setting and does not import the chat record. It builds the extraction request (the prompt is a setting; the exchange is the person's message and the reply as plain text), reads the answer as structured output (one JSON object, `{"facts": [...]}`, checked and trimmed), and makes the source: a name, the date, and an origin that points back to the chat and the turn. `Target` says where facts may land: the memory space, or, for a chat that was sent sensitive content, only a space the store itself says is sensitive, and otherwise nowhere, decided before any model is called. `Add` refuses sensitive facts from a model that may not read them.
- **A queue in the tenant's database**: one row for each turn (`memory_passes`), queued, running, then done, skipped, failed or interrupted, with the model, the space, the source and the facts of a pass that landed. A pass left running by a stop is marked interrupted with a log line, not run again, because it may already have added its source; a queued one is run by the chat's next worker.
- **The incognito flag**, the smallest one: a chat property in `memory_chats`, with `GET /chats/{id}/memory` (the flag and the chat's passes), and `PUT` and `DELETE /chats/{id}/incognito`. It turns the memory pass off for the chat and nothing else; there is no incognito mode on chats yet.
- **The reserve**: `knowledge.memory_reserve_usd`, a monthly amount inside the cap. A memory call to a model that is not local is refused when this month's memory spend and the most the call could cost pass it; the monthly cap still applies; a local model is never asked about. A new `usage.SpentOn` sums one kind of task.
- **In the turn**, a new file `internal/turn/memory.go`: a done turn queues its pass and starts the chat's worker beside it (one line in `runTurn`), one worker per chat, never delaying a reply. The pass routes the `memory` task (`routing.memory_model`, then the chat's model, then the fallback chain; the test tenant gets its recorded turns), calls through `Runner.call` with its own step, recorded with the kind `memory` (three lines in `used`), and the memory route's gate is the reserve and then the cap (one line in `routed`). It runs only when `knowledge.memory` is on, the chat is not incognito and the person's store has the `AddSource` capability.
- **Settings** in `settings/knowledge.json`; **fixtures**: extraction turns in `fixtures/test-tenant/turns/memory.json` and the expected facts in `fixtures/test-tenant/knowledge/memory.json`.
- **Proof**: package and turn tests, and a standing `verify` check, `memory pass`: test-tenant chats produce their expected facts in the built-in store, as sources linked to their chats; a message with nothing durable adds nothing; an incognito chat runs no pass; a held chat's facts land in the sensitive space or nowhere; the reserve refuses a paid memory call once reached while the chat goes on, and a local model behind it still runs at no cost.

Left out: a startup sweep for passes queued before a stop (they run with the chat's next turn), incognito chats that keep no record (P6-07, the memory screens), facts merged across turns (the wiki upkeep jobs, P8-06), and a response format in the model seam.

Result (November 25, 2026): done, as planned. `go run ./tools/verify` is 20 of 20 with the new `memory pass` check: the test tenant's chats produce their expected facts in the built-in store as sources linked to their chats, a message with nothing durable adds nothing, an incognito turn queues no pass, a held chat's facts land labelled sensitive in the confirmed sensitive space or nowhere, and on an invented tenant the reserve refuses a hosted memory call once reached while the chat is answered, and with the reserve and the cap reached a local model reads at no cost. Decisions 0280 to 0284; the component doc is `docs/architecture/memory-pass.md`; the journal is `docs/journal/2026-11-25-knowledge-006.md`, with three owner questions (the reserve inside the cap, naming a sensitive space, incognito's scope), none of which the proof rests on. Only the fake provider has extracted facts; no real model or real Waymarks has been called.

