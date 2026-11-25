# The tool loop

Folders: `internal/tools`, `internal/tools/approval`, `internal/tools/builtin`, and the turn's side in `internal/turn/tools.go`

A turn offers the model tools. When the model asks for one, the tool runs for the person whose turn it is, its result is appended to the chat, and the model is called again, until it answers or the turn reaches its step limit. Each step is streamed as it happens, kept in the chat record, and recorded in the audit log. A tool that changes something runs only under the approval policy (P7-02, [approvals.md](approvals.md)).

Decisions [0200](decisions/0200-a-tool-is-data-and-a-function-in-a-registry.md) to [0204](decisions/0204-the-screen-writes-the-steps-line.md) say why it is built this way. The chat screen's side is under "On the screen" below, and in [web.md](web.md).

## A tool

`tools.Tool` is plain data and one function:

| Field | Meaning |
| --- | --- |
| `Name` | What the model calls it by: lower-case letters, digits and underscores, starting with a letter, at most 64. |
| `Description` | What it does and when to use it, for the model. |
| `InputSchema` | The JSON Schema of its arguments, an object. |
| `ReadOnly` | True for a tool that only reads. Anything else changes something, inside Keystone or outside it, and runs only with a person's approval. |
| `Run` | `func(ctx, tools.Input) (string, error)`. It is given the tenant's store, the arguments and the chat's id, and returns the text the model reads. An error becomes an error result with the error's text, so it is written for the model. |

`ctx` is cancelled when the person stops the turn or the tool has run for `agents.tool_timeout`. `Run` should return soon after, and the loop does not wait for it if it does not.

## The registry

    reg := tools.New()                              // empty, and nothing else can fill it
    err := reg.Add(tool)                            // a plain call; a bad tool is refused with the reason
    defs := reg.Definitions()                       // what a model is offered, by name
    res := reg.Run(ctx, tools.Call{...})            // the only way a tool runs
    res := reg.Decline(call, "why")                 // answers a call without running anything

Nothing registers itself: there is no global registry and no `init`. A test makes its own registry and leaves nothing behind. The server's registry is made in `internal/app/tools.go`, by `app.Tools(c)`, which adds the built-in tools with `builtin.Add`. An item that brings tools adds them there, by one call. `app.Config.Tools` is a registry a caller made instead, which the `approval` check in `verify` uses. Nil means `app.Tools`.

**`Run` never fails.** Whatever happens comes back as a `tool_result` block a provider takes, with a `Status`:

| Status | When | The model reads |
| --- | --- | --- |
| `ok` | The tool ran and gave its result. | The result, cut at `agents.tool_result_max_chars` with a line that says so. An empty result is said to be empty. |
| `error` | The tool is not in the registry, its input is not a JSON object, it returned an error, it panicked, or it ran past its timeout. | Why, as a sentence. A panic's value goes to the server's log, never to the model. |
| `refused` | The tool changes something and the call has no approval, or the call was declined (tools are off, or the turn is at its step limit). | Why. |
| `stopped` | The turn was stopped while the tool ran, or before it started. | That it has no result. |

**Each call is recorded in the tenant's audit log** (fixed rule 4), as two entries of the kind `tool`, with the turn's request id, its actor, and the tool's name as the action ([audit.md](audit.md)):

| Entry | Outcome | Holds |
| --- | --- | --- |
| Before the tool runs | `started` | `sent`: the arguments. |
| Its end | `ok` (ran, or stopped), `refused` or `error` | `received`: `{"status": …, "result": …}`, and the time taken. |

A call whose first entry cannot be written is not run. A result whose entry cannot be written is not used: the model is told so instead. A declined call gets the same two entries.

**The tool runs in a goroutine of its own**, so a tool that ignores its context cannot hold a stopped turn open. `Run` returns when the tool does, when the turn is stopped, or at the timeout.

`internal/tools` reads no setting. What a caller may vary, the timeout and the most characters of a result, comes in the `Call`. It is a guard of fixed rule 2 in `verify`'s `fixedRuleGuards`.

## The approval rule

`internal/tools/approval` is fixed rule 2's test, one function and one type:

    err := approval.Check(readOnly, a)   // nil lets the tool run

A tool that only reads runs. A tool that changes something runs only with an `approval.Approval` that says a person agreed. The type has one field, which is not exported, and no function makes one yet, so code outside the package can hold only the empty value, which says nobody agreed. Nothing a tool returned or a model wrote can become one. Until the approval policy (P7-02) adds the one function that makes an approval, every tool that changes something is refused, and the model reads:

> This tool changes something, and it was not run: a tool that changes something needs a person's approval, and approvals are not built yet.

The package imports nothing of Keystone's. It is the first guard of the rule in `fixedRuleGuards`, so `verify` fails if it could ever read a setting (decision [0201](decisions/0201-a-changing-tool-is-refused-until-approvals.md)).

**Since P7-02** the package holds the approval policy's rule too, `approval.Decide`, and the two ways an approval is made: by the person's standing policy and by their answer to one call. An approval covers one tool with one input, and `Registry.Run` checks it with `approval.CheckCall`. A refusal now reads "this tool changes something, and it was not run: a tool that changes something runs only with a person's approval". A tool also says whether its result is Keystone's own (`Inside`) and may say what a call will do for the approval card (`Describe`). The whole of it is [approvals.md](approvals.md).

## The built-in tools

In `internal/tools/builtin`. All three only read, and each reaches the person's data through the store it is given and nothing else.

| Tool | Input | Gives |
| --- | --- | --- |
| `current_time` | none | The date and time where Keystone runs, with its offset, and in UTC. |
| `search_chats` | `query` | The person's other chats whose title or a message holds every word, as the chat record's search finds them: each with its title, id, date and a snippet. The chat the turn is in is left out. |
| `read_chat` | `chat_id` | One of the person's chats: its title and dates, and the text of the messages on its current branch, oldest first, at most `agents.read_chat_max_messages` of the last. A tool call is named, and tool results and thinking are left out. |

Knowledge lookups are recall's (P6-05), web search and fetch are P7-03's, and artifacts are P9-03's. Each adds its tools to the registry the same way.

`web_search` and `web_fetch` are in `internal/tools/web`, added in `app.Tools` by `web.Add`. Both only read, and both bring outside content: each result opens with `tools.OutsideMark`, and `tools.IsOutside` and `tools.ReadOutside`, in `internal/tools/outside.go`, tell a result or a turn that read outside content ([web-search.md](web-search.md)).

## In a turn

`internal/turn/tools.go`, called from `Runner.work` and `Runner.build`.

1. **What is offered.** Every tool in the registry, by name, goes into the context builder's `Tools` slot, which is never left out ([context.md](context.md)). Nothing is offered to a model without the `tools` capability, when the server has no registry, or when the person has switched `agents.tools_enabled` off.
2. **A call.** When a model call stops with `tool_call`, each call in the reply is answered in order: run through `Registry.Run`, or declined when tools are off. Each result is streamed as a `tool_result` event as it comes. The results are then appended to the chat as one message with the role `user`, which is streamed as a `message` event, and the model is called again.
3. **The step limit.** `chats.turn_max_steps` is the most model calls one turn makes. When the last of them still asks for tools, its calls are declined, so the record stays one a provider takes, and the turn writes `agents.step_limit_note` as an assistant message with the stop reason `step_limit`. The turn ends as `done`, with the stop reason `step_limit` (decision [0202](decisions/0202-the-step-limit-ends-a-turn-with-a-note.md)).
4. **Stop.** The stop route cancels the turn's context, which is the tool's. The running tool's call is answered as stopped at once, each call after it is answered as stopped without running, the results are stored, and the turn ends as `stopped` (decision [0203](decisions/0203-a-tool-runs-beside-the-turn-and-stop-does-not-wait.md)).
5. **A tool that breaks** gives an error result. The model reads it and goes on. The turn is not failed.
6. **A restart** while a tool runs leaves the call with no result. The turn is closed as `interrupted`, like any turn that was running, and the call is left out of later requests.

### What the person sees in the stream

No event changed shape. A turn that uses tools streams, for each step:

    start → text … → tool_call → message (the reply, with its calls) → usage → tool_result … → message (the results)

and then the next step's `start`. At the step limit, after the declined calls' results, one more `message` comes: the note, with the stop reason `step_limit`, before `title` and `end`.

### The test tenant's task of several steps

`fixtures/test-tenant/turns/tool-steps.json` holds two recorded turns:

- "What have I asked you about the lighthouse, and what is the date today?" The fake model says it will look, calls `search_chats` with "lighthouse", reads the result, calls `current_time`, reads that, and answers. The tools run for real against the test tenant's chats. This is the item's proof.
- "Send a postcard to the lighthouse keeper." The fake model calls `send_postcard`, a tool no build registers. It is answered as a tool that does not exist, or, in the `approval` check, which registers it as a tool that changes something, it is refused. The model then says nothing was sent.

## On the screen

The chat screen already drew tool calls as steps in the reply's card. Since this item each step reads as a sentence, from the approved step rows of `MainDark.html`, built from the design tokens (decision [0204](decisions/0204-the-screen-writes-the-steps-line.md)):

| State | The row |
| --- | --- |
| Running | What it is doing, in the present, pulsing, with "running" on the right: "Searching your chats for “lighthouse”". |
| Answered | What it did, in the past, with a few words on the right where the result gives them: "Searched your chats for “lighthouse”" and "3 matches"; "Read “Planning the lighthouse walk”"; "Checked the date and time". |
| No result | What the model asked for, with a tag: "Check the date and time" and "not run", or "stopped", in the plain tag; "failed" or "no such tool" in the error colour. |

A tool the screen has no words for reads "Used send_postcard: to “The lighthouse keeper”, message “Thank you for the light.”": its name and the first plain values of its input, cut short. Each row is closed until the person opens it, and then shows the tool's name, what it was sent, and what came back. The text a model wrote between steps stays between them, so a reply reads in the order it happened.

The note at the step limit is drawn as Keystone's own: in a dashed box with the stop mark, under the steps, and not in the reply's reading type. All of it is the same after a reload, because it is drawn from the record. The code is `web/app/src/steps.ts`, `Conversation.tsx` and `layout.ts`.

## Settings

In `settings/agents.json`. `turn.ReadConfig` reads the install's, and the one a person may set is read from their stored value for each turn.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `agents.tools_enabled` | true | tenant | Whether a turn offers the model tools. Off, nothing is offered, and a call the model makes anyway is declined and recorded. |
| `agents.tool_timeout` | 60s | install | How long one tool may run. |
| `agents.tool_result_max_chars` | 20000 | install | The most characters of one result kept and sent back. Zero keeps every result whole. |
| `agents.step_limit_note` | see the file | install | The note a turn ends with at its step limit. The person reads it, and the model reads it in later turns. |
| `agents.read_chat_max_messages` | 40 | install | The most messages `read_chat` returns. |

`chats.turn_max_steps` (8) in `settings/chats.json` is the step limit ([turns.md](turns.md)).

## Checked by

- `go test ./internal/tools/...`
  - The registry refuses a tool with a bad name, no description, a schema that is not an object, no function, or a name already taken, and offers its tools by name.
  - A tool runs and is recorded before and after. A tool that changes something is refused and never runs. A tool that fails, panics, runs too long, is given input that is not an object, or is not in the registry gives an error result. Stopping the context stops a tool, even one that ignores it. A call whose first entry cannot be written is not run.
  - The approval rule lets only a tool that reads run with no approval, and an approval cannot be made outside its package.
  - The built-in tools only read, and each finds and reads the person's own chats and no other person's.
- `go test ./internal/turn`, with the real HTTP server and the fake provider:
  - **The test tenant's task of several steps completes**: the stream, the stored record, the context record and the audit log each hold both steps, under the turn's request id, and the model was offered the three built-in tools.
  - The step limit ends a turn as done with its note, and the chat can go on. A tool that changes something does not run, and the turn goes on. Tools that break, fail or do not exist do not break the turn. Stop cancels a running tool, and the call after it never runs. Tools switched off are not offered. A tool given another tenant's chat id cannot read it.
- `verify`:
  - `calls recorded` runs the task of several steps on the real server as the test tenant, and reads each tool call's two entries in the audit log, the model calls' entries, and the calls and results in the chat record. The code is in `tools/verify/toolloop.go`.
  - `approval`, a new standing check, registers a tool that changes something on the real server beside the built-in ones, has the test tenant's recorded turn call it, and fails if it ran, if the model was not told it needs an approval, or if the refusal is not in the audit log.
  - `fixed rules`: `internal/tools/approval` and `internal/tools` are the guards of the approval rule, and may not import the settings registry or the tenant overrides.

- In a browser, against the server built from this branch on port 7921 with a data folder of its own, signed in as the test tenant: the task of several steps, its rows opened, and the chat reloaded; the postcard turn; and, with a stand-in server made for the look and not kept, a slow search to see a running row, a turn at a step limit of two to see the note, and a postcard tool that changes something to see "not run". In dark and light, and at phone width.

## Provisional

- How the steps look waits on the owner's approval of the agent mockups (P1-05). The rows follow `MainDark.html`'s approved step rows. The running state, the "no result" rows, the open body and the step limit's note have no mockup, and were made from the tokens.
- The words of each row are a first draft, in `web/app/src/steps.ts`.

## Not here yet

- Approvals are built since P7-02: the policy for each tool, the card that asks, and the rule that a chat which read outside content asks for every side effect ([approvals.md](approvals.md)).
- Tools that change something, web search and fetch, connectors, skills and sub-agents (P7-03 to P7-07).
- Running two calls of one reply side by side. They run one after another.
- Sensitive chats. Nothing marks a chat as holding content from a sensitive space yet. When recall (P6-05) marks a turn sensitive, `search_chats` and `read_chat` must leave a marked chat out for a model that may not read sensitive content, or they would carry it into a turn with a hosted model (fixed rule 3). This is a question for the owner in the journal, agents 001.
- A tool's own settings screen, and a list of tools a person can switch one by one.

## Connectors' tools

Since P7-04 a person's connectors bring tools too, made like the knowledge lookups below: not in the server's registry, asked for by each call that offers tools (`withConnectors` in `internal/turn/connectors.go`), kept on the turn, and run through a registry of their own, so they are recorded, timed and checked as every tool is. Each is offered by name and one line until the model loads it with `load_tools`. A name the server's tools or a lookup has keeps it. See [connectors.md](connectors.md).

## The knowledge lookups

The three knowledge lookups of recall (P6-05) are tools too, but not in the server's registry, because what a lookup may return depends on the call. Each call that offers tools, for a person whose knowledge store opened, also offers the lookups made for the least of its route, and the calls the model makes in reply run those same ones through a registry of their own. The code is `internal/turn/lookups.go`; the lookups themselves are in [knowledge-recall.md](knowledge-recall.md).
