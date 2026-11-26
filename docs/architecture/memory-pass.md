# Memory pass

Folder: `internal/knowledge/memory`, joined to a turn by `internal/turn/memory.go`

After each turn, a model reads the exchange, the person's message and the reply, and the durable facts it finds are added to the person's knowledge store as one source that points back to the chat. It runs beside the turn and never delays a reply. Its spend comes from its own reserve, a part of the monthly cap, so it stops before it eats the cap, and a local model costs nothing. An incognito chat runs none.

The package decides: the request, how the answer is read, the source the facts become, where they may land, the queue of passes, the incognito flag and the reserve. It imports the knowledge interface, the seam, routing's kind names, usage and the tenant handle, and nothing else of Keystone's: it reads no setting and does not import the chat record, which does. The turn reads the settings and the chat, routes and makes the model call, and passes plain values in.

Decisions: [0280](decisions/0280-the-memory-reserve-is-a-part-of-the-cap.md) for the reserve, [0281](decisions/0281-a-held-chats-facts-land-in-a-confirmed-sensitive-space-or-nowhere.md) for the facts of a chat that was sent sensitive content, [0282](decisions/0282-the-memory-pass-is-a-queue-in-the-tenants-database.md) for the queue and what a stop does to it, [0283](decisions/0283-the-facts-are-asked-for-as-one-json-object.md) for asking for the facts as structured output, and [0284](decisions/0284-incognito-is-a-chat-flag-that-stops-the-memory-pass.md) for the incognito flag.

## One turn's pass

1. **The turn ends.** `runTurn` closes the turn, and then calls `memoryLater` (one line). A turn that did not end as `done` queues nothing. Nor does one whose person has the pass off (`knowledge.memory`), has no store (`knowledge.adapter` is `none`), or whose chat is incognito: nothing is written at all.
2. **It is queued.** One row in `memory_passes` for the turn, `queued`. The chat's memory worker starts, unless one is at work: a goroutine of its own, with a store of its own, one for a chat at a time in a process.
3. **The worker takes the chat's passes**, oldest first, each `running` until it ends. When none waits it lets go, looks once more, and takes the chat back if a pass came in as it let go.
4. **Each pass**, inside `knowledge.memory_timeout`:
   - reads the settings again: a pass that waited while the person switched the pass off, or made the chat incognito, is `skipped` with that reason;
   - opens the person's store; a store without the `AddSource` capability is `skipped` before anything else;
   - asks where the facts may land (`Target`, below), before any model is asked; for a chat that was sent sensitive content and no confirmed sensitive space, the pass is `skipped` and no model reads the exchange;
   - reads the chat's title, the person's message and the reply the turn ended with, as text;
   - routes the `memory` task: `routing.memory_model`, then the chat's own model, then the fallback chain, each behind the budget's guard, with the reserve and then the monthly cap asked before each ([routing.md](routing.md)). The test tenant has no route and gets its recorded turns;
   - makes the call through `Runner.call` with its own step, `stepMemory`, under the turn's request id: two audit entries and a usage record of the kind `memory`;
   - reads the answer (`Facts`), asks again whether the chat was sent sensitive content while the model read, and adds the facts as one source (`Add`);
   - ends the row: `done` with the model, the space, the source, its label and the facts; `done` with no facts when the exchange held none; `skipped` or `failed` with the reason.

Whatever breaks is one line in the server's log and the pass's reason, and nothing else: the chat is never touched.

## The package

| Function | Does |
| --- | --- |
| `Request(options, exchange)` | The call that asks for the facts: the prompt as the system text, with `{max}` made the most facts, and one message holding the chat's title and the day, then the person's message and the reply, each cut at a word's end to `MessageMaxChars`. |
| `Facts(response, options)` | Reads the answer. See below. |
| `Target(ctx, store, sensitive, options)` | The space the facts may go to, or `ErrNowhere`. See below. |
| `Add(ctx, store, options, exchange, facts, space, sensitive, mayRead)` | Adds the facts as one source. Refuses sensitive facts from a model that may not read them with `ErrSensitive`. |
| `Origin(chat, turn)`, `ParseOrigin(origin)` | The source's origin, `keystone://chats/<chat>/turns/<turn>`, the shape of the API's path to the turn, and back. |
| `SourceName(options, exchange)` | The source's name, from `knowledge.memory_source_name`: `{title}` becomes the chat's title, or "untitled", cut to 120 characters, and `{date}` the day of the turn. |
| `CheckOptions(options)` | Refuses settings no pass can run with, with `ErrOptions` and the setting's name. |
| `Queue`, `Claim`, `Interrupt`, `Finish`, `Get`, `Passes`, `Waiting` | The queue, below. |
| `Incognito`, `SetIncognito` | A chat's incognito flag. |
| `NewReserve(usd)`, `Allow(ctx, store, reserve, at, next)` | The reserve, below. |

### Reading the answer

The prompt asks for one JSON object and nothing else: `{"facts": ["...", "..."]}`. The model seam has no response format yet, so the shape is asked for in words and checked on the way in:

- The text from the first `{` to the last `}` is read, so a fenced block or a sentence around the object does no harm.
- It must be an object with a `facts` list. Each fact is a string, or an object with a `text`. Anything else is `ErrAnswer`, and the pass fails.
- An empty list is an answer: the exchange held nothing durable.
- Each fact's spaces are made single. A blank fact, one longer than `knowledge.memory_fact_max_chars`, one said twice (letter case aside), and those past `knowledge.memory_max_facts` are left out and counted.

The store turns the facts into a Markdown list, one fact to a line, as the source's text.

## Fixed rule 3

Content from a sensitive space reaches only models the registry marks local or trusted. A fact drawn from such content is such content. So:

1. **Where facts may land, decided first.** `Target` with `sensitive` false gives `knowledge.memory_space`. With `sensitive` true, the exchange may hold sensitive content, because the chat was sent some ([knowledge-recall.md](knowledge-recall.md)): the facts may go only to `knowledge.memory_sensitive_space`, and only when the store's catalog lists pages in that space and labels every one sensitive. An empty setting, a space with no page, or one that is not labelled sensitive is `ErrNowhere`, and the turn asks no model at all. A store adds a space it does not hold as a normal one, so a space that is merely named is never enough.
2. **Who may read the exchange.** The memory route of such a chat is planned as sensitive, as every route of the chat is, so a model that may not read sensitive content is left out of it, first or as a fallback. `Add` refuses such facts from a model that may not read them, with `ErrSensitive`, whatever route they came by.
3. **Asked again after the call.** If the chat was sent sensitive content while the model read, its facts are taken to be sensitive: they go to the confirmed sensitive space or nowhere, and `Add` refuses them if the model that read was not one that may.
4. **The label it landed with.** `Add` reads the label the store gives the new source. A sensitive exchange whose source is not labelled sensitive is `ErrLanded`, a line in the log and a reason on the pass. It means the space was made ordinary between the check and the write.

The package is a guard of `rules.sensitive_content` in `tools/verify/fixedrules.go`, through an `init` in `tools/verify/memory.go`: `verify` fails if it ever imports `settings` or `internal/tenantsettings`, itself or through another package.

## The reserve

`knowledge.memory_reserve_usd` is a part of the monthly cap, not an addition to it (decision [0280](decisions/0280-the-memory-reserve-is-a-part-of-the-cap.md)). What the memory pass spends on models that are not local counts against both.

- **The gate.** The memory route's gate is the reserve and then the cap (`Runner.gate`, one line in `routed`). Before each model of the route, a model that is not local is refused when this month's memory spend, `usage.SpentOn(month, "memory")`, and the most the call could cost, worked out as the cap works it out, are together more than the reserve. A local model is never asked about.
- **A refused call** fails with the kind `spend_cap`, so the route moves on to the next model: a local model behind it reads the exchange at no cost. With none, the pass is `skipped`, and its reason gives the month's memory spend, the reserve and what the call could have cost. The audit log has the call's started entry and a `refused` end, and the usage record is `refused` and cost nothing, as for the cap.
- **The cap** still applies after the reserve allows a call, so a memory pass never takes the month past the cap either.
- **A reserve that cannot be read** stops the call.
- **The month** is the cap's: a calendar month in UTC.

The chat is never stopped by the reserve: only its memory pass is.

## The queue

`memory_passes`, from the migration `20261125T140000_memory_pass.sql`. One row for each turn whose pass was queued:

| Status | Means |
| --- | --- |
| `queued` | It waits for the chat's worker. |
| `running` | The worker has taken it. |
| `done` | The exchange was read. Its facts, when there were any, are in the store, in the source the row names. |
| `skipped` | It was not made, or its facts were kept nowhere, and the reason says why: switched off, incognito, no store that takes sources, no confirmed sensitive space, no model, the reserve or the cap. |
| `failed` | The model call failed or ran out of time, the answer did not read, or the store refused the source. |
| `interrupted` | It was running when Keystone stopped. |

A chat's worker first marks the chat's `running` rows `interrupted`, with a line in the log: in one process a chat has one worker, and it ends every pass it takes, so a pass still running was left by a stop. It is not run again, because it may already have added its source. A pass left `queued` by a stop runs when the chat's next turn starts a worker (decision [0282](decisions/0282-the-memory-pass-is-a-queue-in-the-tenants-database.md)).

The row keeps the facts of a pass that landed, as a JSON list, with the source's id and its space. Facts kept nowhere are not kept here either. The rows go when the chat is deleted, and a pass whose chat was deleted while it ran adds nothing.

## Incognito

There is no incognito mode on chats yet, so this item adds the smallest flag that turns the memory pass off for one chat (decision [0284](decisions/0284-incognito-is-a-chat-flag-that-stops-the-memory-pass.md)). It is a property of the chat, not a setting: `memory_chats`, one row for a chat whose flag was ever set, going with the chat.

An incognito chat's turns queue no pass and leave no row. A pass that waited when the chat was made incognito is `skipped`. Making the chat ordinary again does not read the turns it had while incognito.

The routes, in the shell like every chat route ([api.md](api.md)), in `internal/turn/memory.go` and listed in `internal/app`:

| Route | Reply |
| --- | --- |
| `GET /chats/{id}/memory` | `MemoryReply`: `chat_id`, `incognito`, and `passes`, the chat's passes, oldest first, each with its turn, status, reason, model, space, source, label and facts. |
| `PUT /chats/{id}/incognito` | `MemoryReply`, after making the chat incognito. No body. |
| `DELETE /chats/{id}/incognito` | `MemoryReply`, after making it ordinary again. |

A chat that is not the person's is `not_found`.

## The test tenant

The test tenant has no route: its passes are answered by its recorded turns. `fixtures/test-tenant/turns/memory.json` answers a request whose system text holds "long-term memory of a personal assistant", the shipped prompt's words, and whose message holds one of the fixture's questions, with that question's facts, and any other such request with no facts. It also answers the questions themselves. `fixtures/test-tenant/knowledge/memory.json` holds the questions, the facts each must give, a message with nothing durable, and the held chat's question, space and facts. A model of another tenant that names the fake adapter gets the same answers.

The test tenant's store is `none` by default, so it runs no pass until it is given a store.

## Settings

In `settings/knowledge.json`. The package exports each key as a constant.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `knowledge.memory` | true | tenant | Whether the memory pass runs. It runs only for a store that takes sources, and never in an incognito chat. |
| `knowledge.memory_space` | `memory` | tenant | The space facts go to. A space the store does not hold yet is made, as a normal space. |
| `knowledge.memory_sensitive_space` | empty | tenant | The space the facts of a chat that was sent sensitive content go to. The store must hold it, with a page, and label it sensitive. Empty keeps such facts nowhere. |
| `knowledge.memory_reserve_usd` | 2 | tenant | The most the memory pass may spend in a calendar month on models that are not local. A part of the monthly cap. Zero keeps it to local models. |
| `knowledge.memory_prompt` | see the file | install | The instruction to the model. `{max}` becomes `knowledge.memory_max_facts`. |
| `knowledge.memory_source_name` | `Facts from the chat "{title}", {date}` | install | The source's name. |
| `knowledge.memory_max_facts` | 10 | tenant | The most facts one pass keeps, from 1 to 100. |
| `knowledge.memory_fact_max_chars` | 300 | install | The longest fact kept, from 20 to 2000. |
| `knowledge.memory_message_max_chars` | 6000 | install | The most of the person's message, and of the reply, the model is shown, from 200 to 100000. |
| `knowledge.memory_max_output_tokens` | 1000 | install | The longest answer, from 64 to 32000. |
| `knowledge.memory_timeout` | 2m | install | How long one pass may take, from 5s to 30m. |

The model is `routing.memory_model` ([routing.md](routing.md)). For the owner it names his local model, so his memory pass costs nothing and the reserve is there for when that model is down and the route falls back to a hosted one.

## In the turn

Every existing line of shared code this item changed:

- `internal/turn/runner.go`: the field `memory memoryWorkers` on `Runner`, in a block of its own; in `runTurn`, after the turn is closed, `r.memoryLater(s, t, status)`.
- `internal/turn/route.go`: in `routed`, `Gate: r.capGate(s),` became `Gate: r.gate(s, kind),`; in `used`, a call made with the step `stepMemory` is recorded with the kind `memory` (three added lines).
- `internal/app/app.go`: `turn.MemoryRoutes(runner)` added to the routes, after the approval routes.
- `internal/usage`: a new file, `kind.go`, with `SpentOn`, what one kind of task spent in a month on models that are not local.
- `tools/verify/sensitive.go`: the `sensitive content` check switches the memory pass off for its invented tenant, whose hosted model it counts between steps; the memory pass check has its own proof of rule 3.

New files: `internal/turn/memory.go`, `internal/turn/memory_test.go`, the package, the migration, the two fixtures and `tools/verify/memory.go`.

## Checked by

`go test ./internal/knowledge/memory`, against the built-in store:

- The settings are checked, each bad one by name. The request holds the prompt with its number, the title, the day and both sides of the exchange, cut where they are too long, and is one a provider takes.
- An answer is read plain, fenced, with objects, and empty; blank, repeated, long and extra facts are left out and counted; an answer with no object, no list, or a list of the wrong things is `ErrAnswer`.
- The origin points back to the chat and the turn, and no other origin reads as one.
- An ordinary exchange's facts go to the memory space, labelled normal, as a source whose text holds them. A sensitive exchange's go to the sensitive space only when the catalog labels it sensitive; no space, an ordinary space, a space with no page and the memory space itself are nowhere; a catalog that cannot be read is an error. Sensitive facts from a model that may not read them are refused and nothing is added; from one that may, they land labelled sensitive; a space made ordinary under the pass gives `ErrLanded`. A store with no `AddSource` is refused.
- Passes are queued once, taken oldest first, ended once and only with an ending status, marked interrupted when left running, read back, and go with their chat. The incognito flag is set, cleared, refused for a chat that is not there, and goes with its chat.
- The reserve sums the memory pass alone on models that are not local, this month and this tenant only, and stops the call that would pass it.

`go test ./internal/usage`: `SpentOn` sums one kind, on models that are not local, in one month, for one tenant.

`go test ./internal/turn`, through the routes:

- In the test tenant with the built-in store, the beehives question adds its two facts after the turn, as one source in the memory space that points back to the chat, with one usage record of the kind `memory`, and the memory route shows it; the bakery question adds nothing; with no store, or the pass off, nothing is queued.
- The reply ends while the memory call is held; the facts land when it answers.
- An incognito chat queues no pass; made ordinary again it does; a waiting pass in a chat made incognito is skipped; another tenant's chat, or none, is `not_found`.
- The reserve: with none, a hosted memory call is refused, recorded as refused at no cost, and the chat is answered; with one, the hosted model reads at its price; once that reaches the reserve the next is refused; with the reserve and the monthly cap reached, the local model behind the hosted one the memory rule names reads, at no cost.
- A held chat: with no sensitive space, no memory call is made; with an ordinary space named, nowhere; with the sensitive space, the local model reads, never the hosted one the memory rule names, and the facts land labelled sensitive.
- After a stop, a pass left running is interrupted and a queued one runs with the chat's next turn; an answer that is not the facts fails the pass and adds nothing.

Faults were put in by hand and each was caught: no reserve on the memory route, an incognito turn queued, and the hold ignored.

`verify`, `memory pass` ([tools.md](tools.md)), with the real server on a throwaway folder, the test tenant and the built-in store: every chat of `fixtures/test-tenant/knowledge/memory.json` produces its expected facts as one source linked to its chat, through one memory call that was sent the exchange; a message with nothing durable adds nothing; an incognito turn queues no pass; a held chat's facts are kept nowhere with no model reading them while no sensitive space is set, and land labelled sensitive in the sensitive space once it is; on an invented tenant with a hosted and a local model, the reserve refuses the hosted memory call while the chat is answered, allows one when there is room, refuses the next once that reaches the reserve, and with the reserve and the cap reached the local model reads at no cost; and a held chat's pass never reaches the hosted model.

## Not here yet

- **A sweep at start.** A pass left `queued` by a stop runs with the chat's next turn, not when the server starts. P8-01's background jobs are the place for it.
- **Incognito chats that keep no record** came with P6-07: made incognito from the start, no recap, left out of past-chat search, deleted when left or once idle ([memory-screens.md](memory-screens.md)). Recall still reads the store in an incognito chat.
- **Forgetting** came with P6-07: a fact the pass added is corrected or forgotten on the Memory screen, and its row follows the new source ([memory-screens.md](memory-screens.md)). The facts of a deleted chat still stay in the store until forgotten.
- **Facts merged across turns.** Each pass adds its own source, and the same fact told twice is added twice. Turning sources into pages is the wiki upkeep jobs' (P8-06).
  - Since P8-06 the wiki upkeep jobs turn the sources into cited pages ([wiki-jobs.md](wiki-jobs.md)). A wiki job's run chat runs no memory pass (`turn.Options.SkipMemory`), so a job's report never becomes a source.
- **What the store already holds.** The pass is not shown the turn's recall, so it does not know which facts the store has.
- **A response format in the model seam**, so a provider can be asked for JSON that fits a schema.
- **A real model.** Only the fake has extracted facts so far. How well the owner's local model follows the prompt is unknown.
- **A screen.** The routes are there; showing the passes and the flag is the interface lane's.

Since P9-01, a chat in a project with a memory space keeps its facts there in place of `knowledge.memory_space` (one added line in `memoryPass`). A chat that was sent sensitive content is unchanged: its facts go to `knowledge.memory_sensitive_space` or nowhere, whatever its project. See [projects.md](projects.md) and decision [0400](decisions/0400-a-projects-memory-is-a-space-of-the-knowledge-store.md).
