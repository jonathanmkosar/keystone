# Approvals

Folders: `internal/tools/approval` (the rule), `internal/tools` (where a tool runs), the turn's side in `internal/turn/approvals.go`, `policies.go` and `approvalroutes.go`, and the screen in `web/app/src/Approval.tsx` and `ApprovalSettings.tsx`

Fixed rule 2: a side effect outside Keystone happens only under the approval policy, and content the agent read never grants approval. This part is that policy. Each person sets, for each tool, whether it runs without asking (`always`), asks first (`ask`) or never runs (`never`). A call that asks pauses its turn and puts a card in the chat; the person approves or declines that one call, and the turn goes on. Once a chat has read outside content, every tool that changes something asks, whatever its policy, and no setting or policy can change that.

Built in P7-02. Decisions [0220](decisions/0220-the-rule-decides-each-call-and-an-approval-covers-one-call.md) to [0224](decisions/0224-the-card-is-drawn-from-approval-events-kept-outside-the-screens.md) say why it is built this way. It is provisional on the owner's approval of the agent mockups (P1-05) for how the card looks, and on the open question about the knowledge store (see "Provisional").

## The rule

`internal/tools/approval` imports nothing of Keystone's, so it can read no setting, and the `fixed rules` check fails if that ever changes. It is given values and gives a verdict:

    d := approval.Decide(approval.Call{Tool: name, Input: input, ReadOnly: t.ReadOnly, Policy: p, Outside: outside})

| Policy | A tool that only reads | A tool that changes something, nothing read from outside | A tool that changes something, after outside content |
| --- | --- | --- | --- |
| `always` | runs | runs, with an approval for this call | asks (reason `outside`) |
| `ask` | asks | asks | asks (reason `outside`) |
| `never` | refused | refused | refused |
| anything else, or unreadable | asks | asks | asks (reason `outside`) |

A tool with no stored policy has its default: `always` for a tool that only reads, `ask` for one that changes something (`approval.Default`). That is the plan's "side effects ask by default".

**An approval is a value only this package makes**, two ways: `Decide` makes one for a changing tool set to `always` in a turn that read nothing from outside, and `Answered` makes one from a person's recorded answer. Its fields are not exported. **It covers one call**: the tool's name and a digest of its input, compared as JSON. `internal/tools` runs a tool that changes something only through `approval.CheckCall`, so an approval given for one call cannot run another tool, or the same tool with other input (decision [0220](decisions/0220-the-rule-decides-each-call-and-an-approval-covers-one-call.md)).

## Outside content

A turn has read outside content when, before the call being decided:

- **a tool whose result is not Keystone's own ran** in this chat. `tools.Tool.Inside` says a tool's result holds only what Keystone keeps for the person: their chats, the clock, a page of their knowledge store. A tool that does not say so is taken to bring outside content, so a forgotten flag makes side effects ask rather than run. The three built-in tools are inside. Web fetch, connectors and the like (P7-03, P7-04) are not;
- **a source of the knowledge store was read** with `knowledge_read_source`, or **a knowledge search named a source**;
- **recall gave a call of this chat a source** (`turn_recall` keeps what each turn's recall kept);
- **an earlier call in the same step** may bring outside content (it is not inside), since the calls of a step are decided together.

A page of the knowledge store is not outside content: the person and Keystone's own upkeep write it. That is the recommended answer to the owner's open question (knowledge 005), built here and marked provisional.

**The mark is on the chat, not only the turn.** What was read stays in the chat's history, and every later call is sent it again, so every later turn of the chat asks for side effects too. The plan says "every side-effect tool in that turn asks"; marking the chat is stricter and never weaker (decision [0221](decisions/0221-outside-content-marks-the-chat.md)). The mark is kept in `tool_outside`, and a mark that cannot be read counts as read. A new chat starts unmarked.

## In a turn

`internal/turn/approvals.go`, called from `toolStep` (`tools.go`):

1. **The step is decided whole.** When a model call stops for tools, every call of the reply is decided before any of them runs: the person's policies (`tool_policies`), whether the chat has read outside content, and the tool's own flags go to `approval.Decide`.
2. **A call that asks** gets a row in `tool_approvals` with a readable summary (the tool's `Describe`, or its name and the plain values of its input) and a sentence saying why it asks, and an `approval` event with the status `pending`.
3. **The turn waits** until every call of the step that asked is answered, the person stops the turn, or the person's `agents.approval_timeout` passes (24 hours as shipped). The turn holds its chat's queue meanwhile: a message sent now waits behind it. Nothing of the step has run while it waits.
4. **Then each call is answered in order.** An approved call runs once, with the approval its answer made. A declined call is answered with "The person declined to give their approval, so this tool was not run." An expired one, a stopped one and a `never` one each get their own sentence. Every call is recorded in the audit log as before: two entries of the kind `tool`, the second `refused` for a call that did not run.
5. **A call of a changing tool that runs under `always`** gets a row too, with the status `standing`, so it is used once.
6. **After each call**, if it brought outside content, the chat is marked.

**An approval is used once.** Its row is marked `used_at` just before the call runs. A row already marked is never run again: the model is told its approval was used.

**A restart.** A server that stops while a turn waits leaves the turn `running` and its rows `pending`. The next start (`Runner.Recover`) finds a running turn whose newest message is the reply that made the calls, puts it back in the queue (`requeuePaused`), and when it runs it goes on from that step (`resumeStep`): the person's message is not written again, the step's knowledge lookups are made again, the step is decided again (an answer already given stands), and it waits again. A call that was approved and had started when the server stopped is not run again (decision [0222](decisions/0222-a-step-is-decided-whole-and-the-wait-survives-a-restart.md)).

**Stop.** The stop route cancels the turn. What still waits is answered as `stopped`, each call is answered as not run, the results are stored, and the turn ends as `stopped`. A server that is closing writes nothing.

**A tool set to `never` is not offered**, nor a knowledge lookup set to `never`. A call the model makes to one anyway is refused.

## The routes

In `internal/turn/approvalroutes.go`, added to the one list by `internal/app/app.go`. Each acts for the signed-in person on their own tenant.

| Route | What it does |
| --- | --- |
| `GET /api/v1/chats/{id}/turns/{turn}/approvals` | `ApprovalsReply`: the approvals the turn asked for, oldest first. A pending one of a turn that has ended reads as `stopped`. |
| `POST /api/v1/chats/{id}/turns/{turn}/approvals/{approval}/approve` | Approve one call that waits. No body. Answers with the `Approval`. |
| `POST /api/v1/chats/{id}/turns/{turn}/approvals/{approval}/decline` | Decline one call that waits. No body. |
| `GET /api/v1/tools` | `ToolsReply`: every tool a turn may use (the registry's and the three knowledge lookups), each with `read_only`, `inside`, `default`, `policy` and `overridden`, and the three policies. |
| `PUT /api/v1/tools/{name}/policy` | `{"policy": "always" \| "ask" \| "never"}`. Answers with the tool's row. |
| `DELETE /api/v1/tools/{name}/policy` | Back to the default. |

| Code | Status | Meaning |
| --- | --- | --- |
| `not_found` | 404 | No such chat, turn or approval in this person's record, or no tool of that name. |
| `answered` | 409 | The approval was already answered. |
| `turn_ended` | 409 | The turn has ended, so its calls can no longer be answered. |
| `bad_request` | 400 | A policy that is not one of the three. |

**The answer is in the path**, so the audit log's entry for the call says what was decided, by whom (the person's actor) and when, with the request id. The row keeps the same actor and request id (decision [0223](decisions/0223-the-answer-is-in-the-path-and-the-policy-is-a-table.md)).

**The policy is a table, not a setting.** Tools come and go with connectors, and the rule that reads the policy reads no setting. `agents.approval_timeout` is the one setting.

### The `approval` event

Added to the turn's stream. No existing event changed.

| Field | Meaning |
| --- | --- |
| `id` | The approval's id, for the routes above. |
| `turn_id`, `chat_id`, `call_id` | The turn, the chat, and the tool call's id as its `tool_call` event gave it. |
| `tool`, `summary`, `input` | The tool, what the call will do in a sentence, and its arguments. |
| `reason`, `why` | `policy` or `outside`, and a sentence: "Asking because this chat read outside content, from read_notice_board." |
| `status` | `pending`, then `approved`, `declined`, `expired` or `stopped`. |
| `asked`, `decided` | When. |

It is sent when the call asks, and again when it is answered. A client keeps the newest by id. A turn's stream for a call that asks, then is approved:

    … tool_call → message (the reply, with its calls) → usage → approval (pending) → approval (approved) → tool_result → message (the results) → start …

## Tables

In `internal/tenant/migrations/20261125T113000_tool_approvals.sql`.

| Table | Holds |
| --- | --- |
| `tool_policies` | The person's own policy for a tool. A tool with no row has its default. |
| `tool_approvals` | One row for each call that asked, and each changing call that ran under `always` (`standing`): the step and reply it belongs to, the call, its input and summary, why it asked, its status, who answered and with which request, and when it was used. The rows go with the turn's chat. |
| `tool_outside` | Each turn that read outside content, the tool, and why, in a clause a card can give. The rows go with the turn's chat. |

## On the screen

**The approval card**, `web/app/src/Approval.tsx` and `approval.css`, from the approved card in `MainDark.html` and `MainLight.html`, built from the tokens. It sits under the reply, one for each call that asks: the title ("Run write_harbour_log", or the step's own words for a tool the screen knows), why it asks on the right, the server's summary, "Show what it will be sent" with the input, and **Approve** (the accent button) and **Not now**. Once answered it says so in a line: "Approved. It runs now.", "Declined. It was not run, and the model has been told." The cards are kept outside the screens, by chat, from the `approval` events of every stream the page reads, and a chat's are forgotten when its turn ends; a reload in the middle of a wait joins the turn's stream from its first event, so the card comes back (decision [0224](decisions/0224-the-card-is-drawn-from-approval-events-kept-outside-the-screens.md)).

The step row of a call that waits says what the model asked for, with "waiting for you" on the right, and does not pulse. A declined call's row says "declined".

**The Approvals settings screen**, `/settings/approvals`, `web/app/src/ApprovalSettings.tsx`: every tool, with whether it only reads and whether its result is Keystone's own, its description, a select of the three policies, and "Reset to default" when the person has set one. A sentence says that a tool that changes something asks once the chat has read outside content, whatever is set here.

`web.md` lists the calls the screen makes.

## Settings

In `settings/agents.json`.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `agents.approval_timeout` | 24h | tenant | How long a call waits for the person's answer before it is answered as not run. From 1s to 720h. |

## Checked by

- `go test ./internal/tools/...`: `Decide` over every policy, with and without outside content, for tools that read and tools that change something; an approval covers only its own tool and input, spaced any way; nothing outside the package can make one; a refused call never runs; the summary.
- `go test ./internal/turn` (`approvals_test.go`, with the real HTTP server and the fake provider):
  - a changing tool asks, has not run while it waits, is listed, and runs once when approved, with the answer in the audit log; it cannot be answered twice, or by another person;
  - declined, it never runs and the model goes on;
  - **a tool set to `always` runs without asking until the chat reads outside content; then it asks, in that turn and in later turns of the chat, and still runs without asking in a fresh chat**;
  - `never` is not offered and is refused; stop while it waits; **a wait survives a restart** and the call runs once; an approval is used once; an unanswered call expires;
  - the policy routes; a knowledge source is outside content and a page is not; a search that names a source is.
- `verify`, the standing check `approval` ([tools.md](tools.md)), extended: the rule over every policy; on the real server, a tool set to `always` runs without asking in a chat that read nothing from outside, asks with the reason `outside` after `read_notice_board`, has not run while it waits, runs once when approved, with the answer in the audit log, and cannot be answered twice; `never` is refused and not offered; a waiting turn survives a restart and runs its call once when approved on the next server. The postcard part from P7-01 is answered with a decline while it waits, and still proves the tool does not run without an approval. The code is in `tools/verify/approvals.go`. It was seen to fail when `Decide` was changed to let `always` run after outside content.
- `fixed rules`: `internal/tools/approval` and `internal/tools` stay the guards of rule 2 and import no setting.
- In a browser, on a server started from this branch on port 7931 with a data folder inside the worktree, as the test tenant, with a harness (not committed) that registered `write_harbour_log` and `read_notice_board`: the Approvals screen set `write_harbour_log` to always; the notice board turn asked with the card and ran once approved; a later turn of the same chat asked, the card came back after a reload, and "Not now" left the row "declined"; a fresh chat ran without asking. In light and dark, and at phone width.

## Provisional

- **How the card looks** waits on the owner's approval of the agent mockups (P1-05). It follows the approved card in `MainDark.html`; its open input, its answered line, the "waiting for you" row and the settings list have no mockup. The mockup's **Edit** button (change the call before it runs) is not built.
- **Is the knowledge store outside content?** Built as recommended: sources count, pages do not (owner question, knowledge 005). If pages count too, `lookupInside` in `policies.go` returns false for every lookup, and recall's pages mark the chat as well as its sources.
- **The words** of the card, the reasons and the refusals are a first draft.

## Not here yet

- Editing a call on its card before approving it.
- A standing approval for one chat or one session ("allow for the rest of this chat").
- A list of what waits for an answer across chats, and approvals for background tasks (P8): a card waits in its chat only.
  - Since P8-01 a background task's calls ask in its own chat like any turn's, and the task's card in the chat it was started from says it waits ([jobs.md](jobs.md)).
- A tool that marks its own result as outside or not from what it returned. Only the knowledge search does, in the turn.
- `read_chat` returning another chat's words that came from outside content. A chat is Keystone's own, and the words a model wrote about a web page in one chat are inside when another chat reads them.
- A chat edited back to before the turn that read outside content stays marked, as a held chat does.
