# P7-01. Tool loop

Status: doing (agents)
Lane: agents + interface
Depends on: P4-03
Owner gate: Approval of the agent mockups (P1-05), for how steps are shown only.
Built without the gate: The server side and its tests; the screen built from the draft mockup with design tokens, marked provisional until the mockup is approved.
Scope: Multi-step loop with a step limit; each step shown in the chat as it happens.
Proof: A scripted multi-step task completes and each step renders as in its mockup.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

**The contract.** No route changes. The stream keeps `tool_call` and `tool_result` exactly as they are. One value is added: a message's and a turn's `stop_reason` may be `step_limit`, on the note a turn writes when it reaches the step limit. The turn then ends `done`, and the error code `step_limit` is no longer sent.

**Server side.**

1. `internal/tools/approval`: the test for fixed rule 2. `Check(changes, approval)` lets a tool that only reads run, and refuses one that changes something unless it is given an approval. Nothing can make an approval until P7-02, so a changing tool cannot run. The package imports nothing of Keystone's, and is a guard in `fixedRuleGuards["rules.approval"]`.
2. `internal/tools`: the registry. A tool is a name, a description, a JSON schema, `ReadOnly`, and a function that runs for one tenant's store with a context. `New()` makes a registry, `Add` registers a tool by a plain call, `Definitions` is what a model is offered, and `Run` is the only way a tool runs: it writes the audit entry before and after, asks `approval.Check`, turns a failure, a panic or a timeout into an error result, and returns when the context is cancelled. It reads no setting, and is the second guard.
3. `internal/tools/builtin`: three tools that only read: `current_time`, `search_chats` and `read_chat`.
4. `internal/turn`: the registry comes in `Options.Tools`. The tools on offer go into the context builder's `Tools` slot. A tool call is run through `Registry.Run`, its result is streamed and stored, and the model is called again, up to `chats.turn_max_steps`. At the limit the calls are answered, a note is written as the turn's last message, and the turn ends `done`. Stop cancels the tool, and the calls that did not finish are stored as stopped.
5. `internal/app`: the registry is made and the built-in tools are added where the server is put together.
6. Settings in a new `settings/agents.json`: whether tools are offered, how long one tool may run, how much of a result is kept, the note at the step limit, and how many messages `read_chat` returns.

**Proof.**

- `go test ./internal/tools/...`: the gate, the registry, a failing tool, a panicking tool, a slow tool, a cancelled tool, the audit entries, and the three tools against two tenants.
- `go test ./internal/turn`: the test tenant's scripted task (`fixtures/test-tenant/turns/tool-steps.json`: search the chats, read the time, answer) completes with each step in the stream, the record and the audit log; the step limit ends a turn with its note; stop cancels a running tool; a changing tool is refused and never runs.
- `verify`: `calls recorded` runs the scripted task on the real server and reads the audit log for each tool call; a new check, `approval`, registers a changing tool on the real server, has the model call it, and fails if it ran.

**The screen.** The chat screen already draws tool steps from the same events. It gains a readable line for each step (what the tool is doing and to what, from its name and input), the running state, the result collapsed by default, and the step limit's note. Built from the tokens, from the approved `MainDark.html` step rows.

