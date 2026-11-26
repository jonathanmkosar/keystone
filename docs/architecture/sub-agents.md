# Sub-agents

Folders: `internal/subagents`, the guard `internal/subagents/carry`, one option in `internal/turn/subagents.go`, and the lanes in `web/app/src/Subagents.tsx`

Helpers a model starts from a turn with the tool `run_subagents`, each doing one part of the work at the same time as the others, whose results come back to that turn as the tool's result. The plan's words: sub-agents run in parallel with their own context, each shown as a lane in the chat; each sub-agent has its own budget; research mode (P8-04) is built by sub-agents.

Built in P8-02. Decisions [0480](decisions/0480-a-sub-agent-works-through-one-turn-of-a-chat-of-its-own.md) to [0485](decisions/0485-a-call-waits-up-to-its-own-timeout-and-a-restart-ends-its-sub-agents.md) say why it is built this way.

## The shape

**A sub-agent is not a second loop beside the turns.** It works through one ordinary turn ([turns.md](turns.md)) of a chat of its own, its **work chat**, made when the call starts and titled "Sub-agent: …", as a background task does ([jobs.md](jobs.md), decision [0480](decisions/0480-a-sub-agent-works-through-one-turn-of-a-chat-of-its-own.md)). So every model call and tool call it makes streams, is kept in the chat record, is recorded in the audit log under its own request id, asks under the approval policy, is held by the sensitive rules, and is stopped the way a turn is. Nothing in the tool loop was copied.

**Its own context** is that chat alone: one message, made of `agents.subagent_brief`, its task, and what the model passed it as `context`. None of the parent chat's history goes with it. The context builder adds what it adds to any chat's call (standing instructions, the profile, the person map, recall on its message), so a sub-agent knows the person as any chat does, and not the conversation it came from.

**A call of `run_subagents`** writes its sub-agents, hands them to a **supervisor** that runs under the turn runner, and waits. The supervisor sends each sub-agent its turn while fewer than `agents.subagents_at_once` are working (the rest are `queued`, waiting for a lane), follows their turns, and, when every one has ended, hands the tool its result. The tool's own goroutine is not waited for by the turn runner when a turn stops; the supervisor is, so a call's sub-agents are always seen to their end.

## The tool

`run_subagents`, in the server's registry, added in `app.Tools` by `subagents.Runner.AddTool`.

| Input | Meaning |
| --- | --- |
| `agents` | One or more sub-agents, each an object: |
| `agents[].task` | What it is to do, in full. Required. |
| `agents[].title` | Its name in a few words, for its lane. Absent means the first words of the task. |
| `agents[].context` | What it needs from the conversation. |
| `agents[].model` | One of the person's models by its registry name. Absent means the model the parent chat would use next. An unknown name refuses the call. |
| `agents[].budget_usd` | The most it may spend on models that are not local, held to the person's `agents.subagent_budget_usd` (decision [0482](decisions/0482-a-model-may-ask-a-sub-agent-for-less-budget-never-more.md)). Absent means that limit. |

A call is refused, with words written for the model and nothing started, when: it has no sub-agent or one has no task; it is made in a sub-agent's own chat (a sub-agent cannot start sub-agents); the person's switch that pauses every agent is on; the turn would start more than `agents.subagents_per_turn` in all; a budget is not a number of dollars; or a model is not the person's.

It says it only reads and that its result is Keystone's own, as the artifact tools do: it changes nothing outside Keystone and loses nothing, so it runs under the default policy `always` and does not ask because a chat read outside content. What a sub-agent does that changes something asks the person, in its turn, and its spend is held to its budget. A person can set it to ask or never on Settings → Approvals (decision [0481](decisions/0481-run-subagents-runs-without-asking.md)).

It may run for `agents.subagent_timeout`, not `agents.tool_timeout`: a tool may now carry a longer limit of its own, `tools.Tool.Timeout` (decision [0485](decisions/0485-a-call-waits-up-to-its-own-timeout-and-a-restart-ends-its-sub-agents.md)).

### Its result

A line for the call ("3 sub-agents ran: 2 done, 1 did not finish."), then a section for each sub-agent: its place and title and how it ended; its model calls and its spend against its budget; why it did not finish, when it did not; which model wrote its work when the cap or its budget refused the one it was given; its result, or what it had written, cut at `agents.subagent_result_max_chars`; and its chat's id, which `read_chat` reads for the whole transcript. `agents.tool_result_max_chars` still cuts the whole.

| A sub-agent's turn | It ends as | Its result |
| --- | --- | --- |
| `done`, the model answered | `done` | its last reply |
| `done` at its step limit | `failed`, `step_limit` | what it had written |
| `stopped` from its lane | `stopped` | what it had written |
| `stopped` with the parent turn | `stopped` | nothing is handed back: the turn no longer waits |
| `stopped` past `agents.subagent_timeout` | `failed`, `timeout` | nothing is handed back |
| `stopped` by the pause switch | `paused` | what it had written |
| `failed` with any code | `failed` with that code and message (`spend_cap`, `no_model`, `sensitive`, …) | what it had written |

## The fixed rules

| Rule | How it holds |
| --- | --- |
| 1, tenant isolation | Every row, chat, turn and route is the signed-in person's store's. The routes are walked by `tenant isolation`. |
| 2, approval | A sub-agent's side effect asks, in its own turn, under the same rule as any turn; nothing a sub-agent returns can approve anything. Its chat starts marked as having read outside content when the parent chat had (`carry.Down`), so every side effect in it asks whatever its policy. A sub-agent that read outside content marks the parent chat before its result is given to the turn, and the result opens with `tools.OutsideMark` (`carry.Up`). A mark that cannot be written gives no result at all. |
| 3, sensitive content | A sub-agent of a held chat is held from its first call (`carry.Down`), so only models that may read sensitive content serve it. The result of a sub-agent that was sent sensitive content reaches only a parent chat that is held too; any other parent is told it stays in the sub-agent's chat, since that parent may reach a model that may not read it (`carry.Up`, decision [0483](decisions/0483-a-held-sub-agents-result-reaches-only-a-held-parent.md)). |
| 4, call recording | Every model and tool call of a sub-agent is a turn's, recorded under its own request id; the call of `run_subagents` is recorded under the parent's. |

`internal/subagents/carry` is the rule, two functions on plain marks, and imports nothing of Keystone's. `verify`'s `fixed rules` check lists it as a guard of rules 2 and 3, so it can never read a setting.

## Spend

**Every sub-agent is an unattended run** ([unattended-spend.md](unattended-spend.md)), registered with `unattended.Register(…, Run{Kind: KindSubagent, …})` in the transaction that writes its row, with its own request id and budget, before its turn can be sent. So, with nothing more:

- **its budget** is the registry's gate (`turn.Options.Gate`): a call to a model that is not local that could take it past its budget is not sent, with the cap's own kind of refusal, `spend_cap`, whose message names the sub-agent;
- **at the cap or its budget** it goes on with the next model in the person's chain when `agents.unattended_fallback` is on, or stops (`turn.Options.Moved`, `unattended.Moved`). When it ends, its chat is told of each refused call (`unattended.Tell`), and what they came to (`unattended.Summary`) is kept as its `notice` and goes into its section of the result.

## The switch that pauses every agent

The supervisor reads `agents.paused` at every look, and a look comes at least every `chats.stream_poll`. Switched on, every sub-agent still working has its turn stopped through its model call's context, as the stop route stops a turn, and ends as `paused`; one still waiting for a lane ends at once. The parent turn then has every result, and goes on. A call made while the switch is on starts none and says why (decision [0484](decisions/0484-the-pause-switch-ends-sub-agents.md)). A switch that cannot be read counts as on.

Sub-agents are not counted in the switch's `working` and `held`, and the pause route does not wake them: they are part of a turn the person is watching, and stop at their next look.

## Stop, the parent's stop, and a restart

- **Stop from a lane**: `POST …/subagents/{sub}/stop` writes the stop on its row, wakes its call's supervisor, which stops its turn, and waits up to `chats.stop_wait`. The call goes on with the others.
- **The parent turn stopped**: the tool call's context ends; the supervisor stops every sub-agent still working, ends each as `stopped`, and hands nothing back.
- **A restart** while sub-agents work: the turn runner closes the parent turn as failed (`interrupted`), and `subagents.Runner.Recover`, which runs next, stops any sub-agent turn the turn runner put back in its queue and ends every unfinished sub-agent as `failed` with `interrupted`. Nothing runs again unasked (decision [0485](decisions/0485-a-call-waits-up-to-its-own-timeout-and-a-restart-ends-its-sub-agents.md)).

## The routes

Every path starts with `/api/v1`. `{id}` is a chat's id: the one the sub-agents were started from, or a sub-agent's own. `{sub}` is a sub-agent's id.

| Route | Request | Reply |
| --- | --- | --- |
| `GET /chats/{id}/subagents` | Query `call` (a tool call's id) or `turn` (a turn's id) to narrow. | `SubagentsReply`: `subagents`, oldest first, by call and place. In a sub-agent's own chat, that sub-agent. |
| `GET /chats/{id}/subagents/{sub}` | | `Subagent`. |
| `POST /chats/{id}/subagents/{sub}/stop` | No body. | `Subagent`, as it stands after the stop. |

A sub-agent's steps are its chat's, read with `GET /chats/{work_chat_id}`; its approval cards are its turn's, read and answered with the approval routes ([approvals.md](approvals.md)).

### `Subagent`

| Field | Meaning |
| --- | --- |
| `id`, `chat_id`, `turn_id`, `call_id`, `position` | The sub-agent; the chat, turn and `run_subagents` call that started it; its place among the call's sub-agents, from 1. A lane is drawn under the call's step. |
| `work_chat_id`, `work_turn_id` | Its own chat, its whole transcript, and its turn there. |
| `title`, `task`, `model` | Its name, the words it was given, and the model its turn names (empty for the parent chat's). |
| `status` | `queued`, `running`, `done`, `stopped`, `failed` or `paused`. |
| `progress` | Where it is in a few words: "Waiting for a free lane", "Using search_chats", "Writing", "Waiting for your approval to run …", "Done". |
| `waiting` | A call of its turn waits for the person's answer. |
| `steps`, `max_steps` | The model calls it has finished, and `agents.subagent_max_steps` as it started. |
| `budget_micros`, `spent_micros` | Its budget and spend on models that are not local, in millionths of a US dollar. |
| `result`, `error` | Its last reply, or what it had written; on one that did not finish, `code` and `message`. |
| `notice` | Which model wrote its work when the cap or its budget refused its own. |
| `outside`, `sensitive`, `passed` | Its chat read outside content, or was held; and its result went back to the parent turn. |
| `created`, `started`, `ended` | |

While a sub-agent runs, `steps`, `spent_micros`, `waiting` and `progress` are read from its turn when the route is called; once it has ended, they are what was written then.

## On the screen

`web/app/src/Subagents.tsx`, `subagentCalls.ts` and `subagents.css`, with one line in `Conversation.tsx` (the step list draws a `run_subagents` call as its lanes, as it draws an artifact's card) and an entry in `steps.ts` for an approval card's title. Provisional on the agent mockups (P1-05): the row is the approved one of `design/screens/MainDark.html`, "Ran 2 sub-agents in parallel"; the lanes under it have no mockup and are built from the tokens.

- **The row**: "Running 3 sub-agents in parallel", pulsing, with "1 of 3 done" on the right; once they have ended, "Ran 3 sub-agents in parallel" and "all done", or how many were not. A call the tool refused reads "Run 3 sub-agents in parallel" with "none started", and opens on the reason.
- **A lane for each sub-agent**, in its place: a number, its title, and its state on the right (Waiting for a lane, Working, Waiting for you, Done, Stopped, Paused, Did not finish), with a left edge in the accent while it works, green when done and red when it did not finish; where it is ("Using search_chats · 1 step · $0.00 of $0.25"); its error, its notice, or why its result stayed in its chat; the first lines of its result. **Show its steps** opens its steps as rows, read from its own chat and read again as it moves on, and its whole result with Copy; **Open its chat** goes to its transcript; **Stop** stops it alone.
- **Its approval card**, when a call of its waits for the person: the same card as a turn's, drawn in its lane and answered there, through its own turn's approval routes.

The lanes are read from `GET /chats/{id}/subagents?call=…` once a second while the step runs or a sub-agent is under way, so they are the same after a reload.

Seen in a browser on November 26, 2026, on a server built from this branch on port 7952 with a data folder inside the worktree (removed afterwards), as the test tenant, with the recorded turn `fixtures/test-tenant/turns/subagents.json` ("Count the boats, the gulls and the buoys in the harbour, all at once."): three lanes done with their steps and results, in light and dark, at phone width, and after a reload; with `search_chats` set to ask, a lane waiting with its card, approved there, and its sub-agent finishing; a call left waiting past `agents.subagent_timeout` ending as `timeout`; and a call while every agent was paused reading "none started". The only console error was the sign-in probe's 401.

## Tables

In `internal/tenant/migrations/20261126T010700_subagents.sql`.

| Table | Holds |
| --- | --- |
| `subagents` | One row for each sub-agent: the chat, turn and call that started it and its place; its own chat and turn; its title, task and context; its own request id, actor, model, budget and step limit; its status; and, once it has ended, its steps, spend, result, error, notice, marks, whether its result passed, and who stopped it (`stop_why`: the person, the parent, the timeout or the switch). It goes with the chat it was started from; its own chat stays. |

## The turn's side

`internal/turn/subagents.go`, new, and one option: `turn.Options.MaxSteps`, asked for the step limit of a chat's turns. `subagents.Runner.MaxSteps` answers `agents.subagent_max_steps` for a sub-agent's chat and nothing for any other, which keeps `chats.turn_max_steps`. `internal/turn/tools.go` changed one line, so the step limit is asked of it. `turn.Options.SkipMemory` is wrapped so no memory pass reads a sub-agent's chat: its message is a model's, not the person's.

`internal/tools` gained two fields: `Tool.Timeout`, a tool's own limit when it is longer than the call's, and `Input.CallID`, the id of the model's call a tool answers, so the sub-agents a call starts are shown under it.

## In code

The surface other parts use, research mode (P8-04) first:

    r := subagents.NewRunner(subagents.Options{Install, Log, Config, Turns})
    r.AddTool(registry)                 // run_subagents, before the turn runner is made
    turns := turn.NewRunner(turn.Options{..., MaxSteps: r.MaxSteps, SkipMemory: r.SkipMemory(inner)})
    turns.Recover()
    r.Bind(turns)                       // the first turn runner given stays
    r.Recover()                         // once, when the server starts, after the turns'
    routes = append(routes, subagents.Routes(r)...)
    r.Stop(ctx, store, chatID, id)      // as the stop route does
    subagents.ReadConfig(get), subagents.DefaultConfig()
    carry.Down(parent), carry.Up(parent, agent)

`internal/app` does all of this (`newSubagents` in `internal/app/subagents.go`, and lines in `app.go` and `tools.go`). A caller that makes its own tool registry gets no `run_subagents`.

## Settings

In `settings/agents.json`. `subagents.ReadConfig` reads the install's; the budget is read from the person's stored value when a call starts.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `agents.subagents_at_once` | 3 | install | How many of one call's sub-agents work at the same time. The rest wait for a lane. |
| `agents.subagents_per_turn` | 6 | install | The most sub-agents one turn may start, across its calls. |
| `agents.subagent_max_steps` | 8 | install | The most model calls one sub-agent makes. |
| `agents.subagent_result_max_chars` | 4000 | install | The most characters of each sub-agent's result that go back to the turn. |
| `agents.subagent_timeout` | 30m | install | How long one call of `run_subagents` waits, a wait for the person's approval included. |
| `agents.subagent_brief` | see the file | install | The words each sub-agent's message opens with. |
| `agents.subagent_budget_usd` | 0.25 | tenant | A sub-agent's budget, and the most a model may give one (P8-05). |

`agents.unattended_fallback` and `agents.paused` ([unattended-spend.md](unattended-spend.md)) hold for sub-agents; `chats.stream_poll` and `chats.stop_wait` ([turns.md](turns.md)) are read here too.

## Checked by

- `go test ./internal/subagents/...`, with the real HTTP server, the turn runner and the fake provider:
  - **Three sub-agents run in parallel and their results reach the parent turn** (`TestThreeSubagentsRunInParallelAndTheirResultsReachTheParent`, the item's proof): each sub-agent's model call waits until all three are under way at once, so they overlap in time or the test fails; the parent's next call carries the three results; each worked in a chat of its own with one message and none of the parent's history, under a request id of its own registered as a sub-agent with its budget; every model call and the tool call are in the audit log; another person reaches none of it.
  - **Past its budget** (`TestASubagentPastItsBudgetStopsAndSaysSo`): a budget of five dollars asked for is held to the person's tenth of a cent; the call to the paid model is not sent, and the sub-agent and the parent say why; with a local model in the chain it goes on with it, its chat names the model, and its result carries the notice; with fallback off it stops.
  - **The pause switch** (`TestThePauseSwitchStopsSubagents`): three running sub-agents are stopped within five seconds through their calls' context, each ends as paused, each cancelled call has its two audit entries and a stopped usage record, the parent turn goes on, and while the switch is on the tool starts none.
  - **Approval and outside content** (`TestASideEffectAsksAndOutsideContentTravelsBothWays`): a sub-agent's side effect waits for the person, its lane says so, and it runs once when approved; a result that brings outside content back opens with the mark and marks the parent chat; a sub-agent of that chat asks for a tool set to always.
  - **Sensitive content** (`TestSensitiveContentStaysWhereItMayBeRead`): a held sub-agent's result does not reach a parent that is not held, nor any of its calls; a held chat's sub-agent is held, and its held parent gets the result.
  - **The limits** (`TestTheLimitsHoldAndASubagentStartsNone`): with one lane they run one at a time; more than the turn may start, no task, no agents, an unknown model and a negative budget are refused with nothing started; a sub-agent cannot start sub-agents. A lane's stop stops one and the others finish; at its step limit a sub-agent says so with what it had written; the parent's stop stops them; a restart ends them as interrupted.
  - `internal/subagents/carry`: both functions on every combination of marks.
- `go test ./internal/tools`: a tool's own longer timeout replaces the call's, a shorter one never does, and the tool is told its call's id.
- `verify`, the standing check `sub-agents` (`tools/verify/subagents.go`): on the real server, with the shipped settings, three sub-agents overlap and their results reach the parent's next call, each in its own chat and request id and registered as a sub-agent; one past its budget stops and says so without calling the paid model; the pause switch stops three within five seconds and the parent goes on. The routes are walked with every other route: `tenant isolation`, `calls recorded` and `no keys in output`. `fixed rules` lists `internal/subagents/carry` as a guard of rules 2 and 3.

## Not here yet

- **Sub-agent chats stay in the sidebar**, as task chats do. Three a call add up; an owner question.
- **A sub-agent is offered `start_background_task`**, which asks the person as it does everywhere.
- **A sub-agent's chat stays when the chat it was started from is deleted**, as a task's does.
- **A held sub-agent's result and a parent whose route is local only.** The result is kept from any parent chat that is not held, even when every model the parent's route may reach could read it; the turn's route is not known to a tool.
- **Two calls at once** can each pass a budget's check and together spend more than it by one call's cost, as at the cap.
- **Research mode** (P8-04) builds on this.
