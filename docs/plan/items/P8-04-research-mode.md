# P8-04. Research mode

Status: provisional
Lane: agents
Depends on: P8-02, P7-03
Owner gate: Approval of the agent mockups (P1-05), for the screen only.
Built without the gate: The server side and its tests; the Research control, the progress card and the note built from the design tokens, marked provisional until the mockups are approved.
Scope: A long multi-source report built by sub-agents, with citations, saved as an artifact.
Proof: A test-tenant research task produces a cited report within its budget.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan (November 26, 2026)

Branch `agents/p8-04-research-mode`, cut from `reboot` at cfa1d15. A session that picks this up reads this plan, `git log origin/reboot..HEAD` and `git diff` first.

**The shape.** A research task is a row in a new table, `research`, worked by a goroutine under the turn runner, as a background task is. It is built on turns and sub-agents, not beside them:

1. **Plan.** One turn of the task's own chat ("Research: …"), sent under the task's own request id, whose message is `agents.research_plan_prompt` and the question. The reply is read as a list of parts, at most `agents.research_parts`; a reply that does not read as a list is one part, the question itself.
2. **Parts.** One sub-agent for each part, started by code through a new entry in `internal/subagents` (no model calls `run_subagents` for it), each in a chat of its own with `agents.research_part_brief`, the question and its part. They search and read with `web_search` and `web_fetch`, and recall adds the person's knowledge as it does in any chat.
3. **Write.** The sources are read back out of the recorded web results of each part's chat, numbered once across all parts, and each part's findings are renumbered to match. One more turn of the task's chat, `agents.research_report_prompt`, writes the report from the findings and the source list. The citations of the reply are checked in code (`internal/research/cite`): a number with no source a part's recorded search or fetch returned is taken out and counted; the ones kept are renumbered in the order they appear, and a Sources section is written by code.
4. **Deliver.** The report is saved as a Markdown artifact in the chat the research was started from, and a note there carries the summary. A research task whose own chat was sent sensitive content, started from a chat that is not held, keeps its report in its own chat (`carry.Up`).

**Its budget.** `agents.research_budget_usd`, a person's setting, larger than a task's, shown on the Research control before it starts. It is one sum: `agents.research_report_share_percent` of it is kept for the plan and the report, and the rest is split evenly between the parts as each sub-agent's budget. The task and each part are unattended runs, so the gate, the fallback and the notices hold. What the parts did not spend goes back to the report step (`unattended.SetBudget`, new). A part past its budget ends with what it had; a report step the budget refuses is written by code from the parts' findings, and the report and the note say the research stopped at its budget. With nothing found, the task fails with `spend_cap` and says so.

**The rules.** Marks go down into the task's chat and its parts' (`carry.Down`), so a held chat's research runs only on models that may read it and its web tools are off, as in any held chat. The task's chat is marked as having read outside content before the report turn; the chat that gets the note is marked before the artifact is written. Every model and tool call is a turn's, so it is recorded. The pause switch stops the step under way and the task goes on when it is switched off; Stop ends it; a restart resumes it at its stage, with what its parts had.

**Routes.** `GET /research` (the budget and limits a task would start with), `GET` and `POST /chats/{id}/research`, `GET /chats/{id}/research/{research}`, `POST …/stop`. The drill's `pathValue` gets `{research}`.

**Settings**, in `settings/agents.json`: the budget, the report's share, the number of parts, sources for each part, the report's length, the summary's length, the open limit, and the three prompts.

**The screen.** A Research control beside the composer's pickers (the budget shown, then the question is sent as research), and a progress card under the conversation: the plan, a lane for each part, the sources found, spend against the budget, Stop; then the report's card and the note with its citations linked. Provisional on the agent mockups (P1-05).

**Proof.** `go test ./internal/research/...` with a scripted provider and a stand-in search service on this machine: a cited report within its budget (the artifact exists, every citation maps to a source a part's recorded result returned, an invented one is dropped, spend is under the budget); a task at its budget stops and says so; held chats; stop, pause and restart. A recorded test-tenant turn, `fixtures/test-tenant/turns/research.json`, and a standing check `research mode` in `verify`.

**Order.** (1) this plan; (2) the server side with its tests; (3) the recorded turn and the standing check; (4) the screen; (5) the write-up: `docs/architecture/research-mode.md`, decisions from 0520, journal agents 011, status.

### Result (agents, November 26, 2026)

Provisional on the agent mockups (P1-05) for the screen only. The server side is complete and the proof passes with the fake provider and a stand-in search service.

- **Built as planned**: `internal/research` (a row for each task, worked by a goroutine under the turn runner: a plan turn, a sub-agent for each part, a report turn, the note and the artifact; five routes) and `internal/research/cite`, the check on citations; `subagents.Runner.StartParts`, `Parts` and `StopParts` in a new file, for sub-agents that code starts; `unattended.SetBudget`; the Research control, the progress card and the note (`web/app/src/Research.tsx`); a recorded test-tenant research; eleven settings; the migration `20261126T073341_research.sql`; the standing check `research mode`; the doc `docs/architecture/research-mode.md`; decisions 0520 to 0525.
- **The proof**: a test-tenant research task plans two parts, each searches the stand-in, and the report is a Markdown artifact in the chat that asked, in which every citation maps to a source a part's recorded search returned, the invented ones are taken out and counted, and the spend is within the budget (`TestAResearchTaskProducesACitedReportWithinItsBudget`, and step 1 of the check on the real server with the recorded turn). A task at its budget stops and says so: with nothing found it fails with `spend_cap`, and with its parts done it writes its report from their findings (`TestAResearchTaskAtItsBudgetStopsAndSaysSo`, and step 2 of the check). Seen in a browser as the test tenant.
- **Changed from the plan**: the note is written before the report is saved, so the chat is marked as having read outside content first (decision 0525); the task's run is registered as a background task named "Research: …", with no new kind in the registry (decision 0522); parts cut short by a pause or a restart are not run again (decision 0523).
- **Not run**: `web_fetch` end to end, which reads only public addresses and so cannot reach a stand-in on this machine: a fetched page as a source is checked on a chat record written by the test. No real model and no real search.
- **Provisional**: the look of the control, the card and the note, until the agent mockups are approved.
- **Owner questions**: the research budget's default, and whether the chats a research task makes stay in the sidebar (journal agents 011).
