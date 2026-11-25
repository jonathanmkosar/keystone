# P7-02. Approval policy

Status: doing (agents)
Lane: agents + interface
Depends on: P7-01
Owner gate: Approval of the agent mockups (P1-05), for the approval cards only.
Built without the gate: The server side and its tests; the screen built from the draft mockup with design tokens, marked provisional until the mockup is approved.
Scope: Per-tool policy (always, never, ask); approval cards; turns that read outside content mark every side-effect tool as ask.
Proof: The approval check passes: a side effect after outside content always asks, whatever the tool's setting.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

**The contract.** No existing route, request, reply or event changes shape. Added:

- A stream event, `approval`: one call that needs a person's answer, with its id, the call's id, the tool, a readable summary of what it will do, its input, why it asks (`policy` or `outside`, and a sentence), and its status. It is sent when the call asks (`pending`) and again when it is answered (`approved`, `declined`, `expired` or `stopped`). A screen keeps the newest by id.
- `GET /chats/{id}/turns/{turn}/approvals`: the turn's approvals.
- `POST /chats/{id}/turns/{turn}/approvals/{approval}/approve` and `.../decline`: a person's answer to one call. No body, so the decision is in the path, and the audit log's entry for the call says what was decided and by whom.
- `GET /tools`, `PUT /tools/{name}/policy` (`{"policy": "always" | "ask" | "never"}`) and `DELETE /tools/{name}/policy`: the person's policy for each tool, and back to its default.

**Server side.**

1. `internal/tools/approval`, the guard of fixed rule 2, still imports nothing of Keystone's. It gains the policy (`always`, `ask`, `never`; the default is `always` for a tool that only reads and `ask` for one that changes something) and `Decide`: `never` refuses; a tool that changes something asks whenever the turn has read outside content, whatever its policy; `ask` asks; otherwise it runs. An `Approval` is still made only here: by `Decide` for a standing `always` with no outside content, and by `Answered` from a person's recorded answer. An approval names the call it covers (the tool and its input), and `internal/tools` runs a changing tool only with an approval that covers that call.
2. `internal/tools`: a tool says whether its result is Keystone's own (`Inside`). A tool that does not say so is taken to bring outside content, so a forgotten flag makes side effects ask. The three built-in tools are inside. Of the knowledge lookups, a page is inside and a source is outside, as the owner's open question recommends; a search is outside when it names a source.
3. `internal/turn`, new files: `approvals.go` (deciding a step's calls, the pause, the outside mark, resuming), `policies.go` (the policy table and its routes), `approvalroutes.go` (the answer routes). When a step has a call that asks, every call that asks gets a row in `tool_approvals` and an `approval` event, and nothing in the step runs until each is answered, the person stops the turn, or `agents.approval_timeout` passes. The turn holds its chat's queue while it waits. A server that stops while a turn waits leaves it as it is; the next start puts it back in the queue and it waits again. An approved call is used once: it is marked before it runs, so a restart never runs it twice.
4. Outside content marks the chat for the rest of the turn and for every later turn, because it stays in the chat's history: a tool that is not inside and ran, a source read or searched, or a source recall gave the call. `tool_outside` keeps the mark.
5. A tool whose policy is `never` is not offered.
6. One setting, `agents.approval_timeout`.

**Proof.**

- `go test ./internal/tools/...`: `Decide` over every policy, with and without outside content; an approval covers only its own call; nothing outside the package makes one.
- `go test ./internal/turn`: a changing tool asks, pauses, and runs once approved, or is refused when declined; `always` runs without asking, and asks after outside content; `never` is not offered and is refused; stop while waiting; a wait that survives a restart; expiry; the policy routes; a knowledge source marks the chat and a page does not.
- `verify`, `approval`, extended: after a tool that brings outside content, a changing tool whose policy is `always` asks, and does not run until approved; the same tool runs without asking in a chat that read nothing from outside; `never` is refused and not offered; a waiting turn survives a restart; an approval is used once. The existing postcard part is answered with a decline, and still proves the tool does not run without an approval.

**The screen.** An approval card under the reply, from the approved card in `MainDark.html`, in new files (`Approval.tsx`, `approval.css`), with one line in `Chat.tsx`. A small list of tools with a policy each on the Approvals settings screen, if it stays small.

**What is left**, for a session that picks this up (updated at each commit):

- Done: the server side. `internal/tools/approval` (policy, `Decide`, `Answered`, `Covers`), `internal/tools` (`Inside`, `Describe`, `Get`, `Summary`), the migration `20261125T113000_tool_approvals.sql`, `internal/turn/approvals.go`, `policies.go`, `approvalroutes.go` and their tests, the lines in `runner.go`, `store.go`, `tools.go`, `lookups.go`, `internal/app/app.go`, `agents.approval_timeout`, the fixture `turns/approvals.json`, the `approval` check's new parts in `tools/verify/approvals.go`, and `docs/api/openapi.json`.
- Done: the card (`web/app/src/Approval.tsx`, `approval.css`), the policy list (`ApprovalSettings.tsx` on `/settings/approvals`), a few lines in `Chat.tsx`, `api.ts`, `Conversation.tsx`, `steps.ts` and `Settings.tsx`, `web/dist/` rebuilt, and seen in a browser on port 7931.
- Next: the docs (`docs/architecture/approvals.md` with its `componentDocs` lines and overview row, a section in `web.md`, `turns.md` "Changed since the contract", decisions from 0220), the journal agents 002, and `Status: provisional` with the Result.

