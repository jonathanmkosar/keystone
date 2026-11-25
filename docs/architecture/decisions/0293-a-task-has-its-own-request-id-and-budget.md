# 0293. A task has its own request id, and its budget is a gate the turn runner asks

November 25, 2026 · decided in P8-01

## The question

The plan gives each background run its own budget. A task's calls are turns' calls, which carry the request id of the API call that sent them. How is a task's spend counted, and where is its budget enforced?

## The decision

**Each task gets a request id of its own** when it starts, and every turn of the task is sent under it. The API call that started the task is kept beside it as `started_request`.

**Its spend is the sum of the usage records with that request id**, for models that are not local.

**The budget is enforced before each call**, by the same gate as the monthly cap. `turn.Options` has one new field, `Gate`, a function asked after the cap for each call to a model that is not local; `internal/app` sets it to `jobs.Gate`, which finds the task by the turn's request id and refuses the call when the task's spend and the most the call could cost are together more than its budget. The refusal is the cap's own kind, `spend_cap`.

## Why

- With its own request id, everything a task did reads together in the audit log and the usage records, as a turn's does. A task started by a tool would otherwise share the request id of the turn that started it, and its spend could not be told apart.
- The usage records already count every call, the failed ones and the chat's summaries among them, so the sum is exact with nothing new kept.
- Asking before the call, at the most it could cost, means a task cannot pass its budget, as the cap cannot be passed (0161). Using the cap's kind of refusal means the route falls back to a local model when the person's chain has one, which is the plan's "unattended work falls back to local models or stops", and otherwise the round fails with a message that says why.
- Finding the task by the turn's request id, which is written with the turn, leaves no moment when a task's turn could make a call before the gate knows it is a task's.

## What it costs

- One change to `internal/turn/route.go`, and a query for each paid call of any turn (one row, by key).
- Two tasks of one person that call at the same moment can each pass the check and together spend more than one budget's worth, as with the cap.
