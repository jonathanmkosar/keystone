# Research mode

Folders: `internal/research`, the citation check `internal/research/cite`, the sub-agents' side in `internal/subagents/direct.go`, one function in `internal/unattended/budget.go`, and the screen in `web/app/src/Research.tsx`

A person asks a question with the Research control, and Keystone works it into a long report from many sources, in the background, within a budget of its own: a plan that splits the question into parts, a sub-agent for each part that searches the web and reads pages, and one report with numbered citations to the sources those sub-agents read, saved as a Markdown artifact in the chat, with a short summary as the reply. The plan's words: a long multi-source report built by sub-agents.

Built in P8-04. Decisions [0520](decisions/0520-research-is-three-steps-run-by-code-on-turns-and-sub-agents.md) to [0525](decisions/0525-the-note-is-written-before-the-report-is-saved.md) say why it is built this way.

## The shape

**A research task is not a second loop beside the turns.** It is a row in `research`, worked by a goroutine under the turn runner as a background task is ([jobs.md](jobs.md)), and its three steps are built on what is there (decision [0520](decisions/0520-research-is-three-steps-run-by-code-on-turns-and-sub-agents.md)):

| Step | What it is | What comes of it |
| --- | --- | --- |
| **Plan** | One ordinary turn ([turns.md](turns.md)) of a chat of the task's own, "Research: …", sent under the task's own request id. Its message is `agents.research_plan_prompt` and the question. | The reply is read as a list, one part a line, "title: what to find out", at most `agents.research_parts`. A reply with no such line is one part, the question itself. |
| **Parts** | One sub-agent ([sub-agents.md](sub-agents.md)) for each part, started by Keystone's own code, each in a chat of its own with one message: `agents.subagent_brief`, its task, `agents.research_part_brief` and the question. `agents.subagents_at_once` of them work at the same time. | Each searches with `web_search` and reads with `web_fetch` ([web-search.md](web-search.md)), and recall adds what the person's knowledge store may give that chat, as in any chat ([knowledge-recall.md](knowledge-recall.md)). Its last reply is its findings. |
| **Report** | One more turn of the task's chat. Its message is `agents.research_report_prompt`, the question, each part's findings, and the numbered sources. | The reply is the report. Its citations are checked, the sources it cites are listed by Keystone, and it is saved. |

So every model call and tool call is a turn's: it streams, it is kept in the chat record, it is recorded in the audit log, it asks under the approval policy, it is held by the sensitive rules, and it is stopped the way a turn is. The person can open the task's chat and each part's chat and read every step.

**When it ends**, a note is written in the chat it was started from, as a background task's is: "Research “…” is done.", the summary with its citations, where the report is, how many sources it cites of how many were read, and what it spent. Then the report is saved as a Markdown artifact of that chat ([artifacts.md](artifacts.md)), so it opens in the panel beside the chat, keeps its versions, and a later turn can read or change it with the artifact tools. A task that was stopped or did not finish writes a note that says so.

## Sources and citations

**A source is one a part's recorded search or fetch returned**, and nothing else (decision [0521](decisions/0521-a-source-is-one-a-parts-recorded-web-result-returned.md)). When the parts have ended, Keystone reads each part's chat record: every `tool_result` of a `web_search` or `web_fetch` call that is not an error and carries the outside mark is read with `web.Sources`, the reader the chat screen's citations use. A number a model wrote with no such result behind it names nothing; so does another tool's result, a failed call, and a source written inside a page's own text.

**The numbering.** Each part's tools number its sources from 1 in its own chat. Keystone numbers them once across all parts, by part and then by the part's own number; a page two parts read is one source; a page a search found and a fetch then read is one source, marked as read in full. Each part's findings are rewritten to those numbers before the report step reads them, and a citation in the findings that names no source its part read is taken out there.

**The check**, `internal/research/cite`, plain functions on text and numbers that import nothing of Keystone's:

- `cite.Check(report, read)` takes every citation that names no source in `read` out of the report, renumbers the ones left from 1 in the order they first appear, and returns the sources they name and the numbers that named nothing.
- A citation is one or more numbers in square brackets: `[3]`, or `[3, 5]`. A Markdown link's text, a reference's definition and a fenced code block are left as they are. Two numbers side by side that became one source are written once.

**The report as saved** is: a title and the question; the model's text with its citations checked; `## Sources`, written by Keystone from the sources the report cites, each a link, and "read as a search result's extract" where the page itself was not fetched; and `## About this report`: how many parts and sources, how many citations were taken out, any part that did not finish and why, and the budget when it was reached. A list of sources the model wrote at the end anyway is taken off first.

The task keeps the sources the report cites (`sources`), how many were read (`sources_read`) and how many citations were taken out (`dropped_citations`), and the note says all three.

## Its budget

**One sum for the whole task**: the person's setting `agents.research_budget_usd`, $3 as shipped against a task's $1, or `budget_usd` on the start route. `GET /research` gives it, so the Research control shows it before a task starts (decision [0522](decisions/0522-a-research-tasks-budget-is-one-sum-split-between-its-runs.md)).

The task and each of its parts are unattended runs ([unattended-spend.md](unattended-spend.md)), each with a request id and a budget of its own, so the gate, the fallback at the cap and the notices hold for every call with nothing more. The sum is split so the runs together can never spend more than it:

1. The task's own run is registered with the whole budget, and its plan is sent.
2. Before the parts start, the task's run is lowered to `agents.research_report_share_percent` of the budget (or what the plan spent, if that was more), and the rest is split evenly between the parts as each sub-agent's budget.
3. When every part has ended, the task's run is raised to the budget less what the parts spent: what they did not spend goes to the report.

`unattended.SetBudget` is the one new function there. A part's budget is a share of a budget the person set, so it is not held to `agents.subagent_budget_usd`, which limits what a model may give a sub-agent.

**At its budget a task stops cleanly with what it has, and says so:**

| What the budget, or the monthly cap, refused | Then |
| --- | --- |
| The plan | The task fails with `spend_cap`: "The research stopped at its budget before it found anything." and the refusal. With a local model in the person's chain and `agents.unattended_fallback` on, that model plans instead. |
| A call of a part | The part goes on with a local model in the chain, or ends with what it had written. The task goes on with what its parts have. |
| The report | With a local model in the chain, that model writes it. Otherwise Keystone puts the report together from the parts' findings, as each wrote them, with the same check on its citations. |

In each case `at_budget` is set, the report's "About this report" and the note say "The research reached its budget of $…, having spent $…, so it stopped there: this report is what it had found.", and a model that stood in is named.

## The fixed rules

| Rule | How it holds |
| --- | --- |
| 1, tenant isolation | Every row, chat, turn, sub-agent, artifact and route is the signed-in person's store's. The routes are walked by `tenant isolation` and the isolation drill. |
| 2, approval | Everything fetched is outside content. A part's side effect asks in its own turn, under the same rule as any turn. The task's chat starts marked as having read outside content when the chat it was started from had (`carry.Down`); it is marked before the report step can run, since that step reads what the parts read; and the chat that gets the note is marked before the note is written, and the report is saved only after (decision [0525](decisions/0525-the-note-is-written-before-the-report-is-saved.md)). Nothing a part returns, and nothing in a report, can approve anything. |
| 3, sensitive content | A task started from a held chat is held from its first call, and so is each of its parts (`carry.Down`), so only models that may read sensitive content serve it; `web_search` and `web_fetch` are off in a held chat, as everywhere, so it reads nothing from the web. A part that was sent sensitive content gives its findings only to a task whose own chat is held (`carry.Up`). A task whose own chat was sent sensitive content, started from a chat that is not held, keeps its report and its summary in its own chat, and the note says so in Keystone's words alone (decision [0524](decisions/0524-a-held-tasks-report-stays-in-its-own-chat.md)). The report of a held chat is a sensitive artifact. |
| 4, call recording | Every model call and tool call is a turn's, recorded under the task's request id or its part's. |

`internal/research` enforces no fixed rule of its own: it calls the guards, `internal/subagents/carry`, the turn runner's marks and `recall.Held`. `internal/research/cite` reads no setting.

No memory pass reads the task's own chat (`Runner.SkipMemory`): its messages are Keystone's prompts and what the parts read on the web, not the person's words. A task started from an incognito chat works in incognito chats.

## Stop, the pause switch and a restart

- **Stop**: `POST …/stop` writes the stop on the task; its goroutine stops the turn under way and every part still working, and ends it as `stopped`. No report is written. Stopping the plan's or the report's turn in the task's own chat stops the task too. Deleting the chat a task was started from deletes the task and stops what it had under way.
- **The switch that pauses every agent** (`agents.paused`), read at every look: the task's own turn is stopped, its parts are ended by their supervisor as any sub-agent is (decision 0484), and the task reads `paused`. A task started meanwhile is held and calls nothing. When the switch goes off, the task sends its plan or its report again, or goes on to its report with what its parts had (decision [0523](decisions/0523-a-task-goes-on-with-what-its-parts-had.md)).
- **A restart**: the turn runner closes a turn that was running as interrupted, and the sub-agents' `Recover` ends the parts that were working. `research.Runner.Recover`, which runs next, starts each unfinished task's goroutine: a plan or a report that was cut short is sent again, and a task whose parts were cut short goes on to its report with what they had. The report says which parts did not finish. A note or a report not yet written is written, once.
- The task's own chat takes at most `agents.research_max_rounds` turns; past that it fails with `round_limit`.

## The routes

Every path starts with `/api/v1`. `{id}` is a chat's id: the one the task was started from, or its own. `{research}` is a task's id, sixteen hex characters.

| Route | Request | Reply |
| --- | --- | --- |
| `GET /research` | | `Terms`: `budget_micros`, `parts`, `sources_per_part`, `report_words`, `open`, `open_max`, `paused`. What a task started now would run with. |
| `GET /chats/{id}/research` | | `ListReply`: `research`, the tasks started from the chat or working in it, oldest first. |
| `POST /chats/{id}/research` | `StartBody`: `question`, and optionally `title` and `budget_usd`. | `Research`, 201. |
| `GET /chats/{id}/research/{research}` | | `Research`. |
| `POST /chats/{id}/research/{research}/stop` | No body. | `Research`, as it stands after the stop. |

| Code | Status | Meaning |
| --- | --- | --- |
| `not_found` | 404 | No such chat or task in this person's record. |
| `bad_request` | 400 | No question, a budget that is not a number of dollars, or a start from a task's own chat. |
| `too_many_research` | 429 | The person has `agents.research_open_max` tasks under way. |

There is no stream: the progress view reads one task about once a second while it is under way, and the steps themselves stream on their own turns.

### `Research`

| Field | Meaning |
| --- | --- |
| `id`, `chat_id`, `work_chat_id` | The task, the chat it was started from, and its own chat. |
| `title`, `question` | Its name in a few words (the first words of the question when none was given), and what was asked. |
| `status` | `queued`, `running`, `paused`, `done`, `stopped` or `failed`. |
| `stage` | `plan`, `parts` or `write`, while it has not ended. |
| `progress` | Where it is, in a few words: "Planning the parts", "Researching: 1 of 3 parts done", "Writing the report", "Paused: …", "Done". |
| `waiting` | A call of its own turn, or of a part's, waits for the person's approval. |
| `budget_micros`, `spent_micros`, `at_budget` | The whole task's budget, what it and its parts have spent, in millionths of a US dollar, and whether it reached its budget. |
| `plan` | The parts the question was split into: `title` and `task`. |
| `parts` | The sub-agents, one for each part, each a `Subagent` as [sub-agents.md](sub-agents.md) has it: its state, where it is, its steps, its spend, its chat. |
| `sources`, `sources_read` | While it works, every source the parts have read so far, numbered across them. Once the report is written, the sources it cites, numbered as it cites them: `n`, `title`, `url`, `part` (the first part that read it) and `fetched`. `sources_read` counts every source read. |
| `dropped_citations` | The citations taken out because they named no source a part read. |
| `summary`, `notice` | What the note says of the report; and what else the person is told: a part that did not finish, a model that stood in. |
| `artifact_id`, `artifact_chat_id` | The report, and the chat it is in: `chat_id`, or `work_chat_id` when the report may not go there. |
| `error` | On a failed task, `code` and `message`. The codes are `nothing_found`, `round_limit`, `chat_deleted`, `failed`, and the code of the turn its plan failed with (`spend_cap`, `no_model`, `sensitive`, …). |
| `note_message_id`, `created`, `started`, `ended` | |

## On the screen

`web/app/src/Research.tsx`, `researchCalls.ts` and `research.css`, with lines in `Chat.tsx` and `Tasks.tsx` and one word in `Subagents.tsx`. Provisional on the agent mockups (P1-05): there is no mockup for any of it, so it is built from the tokens in `design/README.md` and from the task card, the sub-agents' lanes and the artifact card. [web.md](web.md) has the calls.

- **The Research control**: a switch beside the composer's pickers, on the new chat screen and in a chat. On, it is outlined in the accent, and a line above the composer says what the next message will start: "It may spend up to **$3.00**, splits the question into up to 4 parts, each reading about 4 sources, and writes a report of about 1,200 words with its sources cited. It runs in the background. Files are left out.", from `GET /research`, and that it waits when every agent is paused or as many tasks as the person may have are under way. The next message sent is then the question: no turn is sent, the control goes off, and a refused start says why under the conversation. Where the composer's row is narrow the hint beside it gives way on one line, and on a phone the control is its icon alone.
- **The card of a task under way**, under the conversation, read about once a second: "Research", its title and state (Planning, Researching, Writing, Paused, Waiting for you), the question, where it is with its spend against its budget ("Researching: 1 of 2 parts done · $0.00 of $3.00"), the plan, then a lane for each part, which is the sub-agents' own lane with its state, steps, result, approval card and Stop, the sources found so far, each a link with its site and the part that read it, a link to its own chat, and **Stop**. A card that has just appeared is scrolled into view.
- **The note**: a message that is a research task's note is drawn as "Research report" (or "Research" for one that stopped or did not finish), with "stopped at its budget" when it did, the note's words with each citation a small numbered link to its source, the report's card, which opens the artifact beside the chat, the sources the report cites, "Show the 2 parts" for the lanes, a link to the task's own chat, and Copy. A report kept in the task's own chat has a link there in place of the card.

Seen in a browser on November 26, 2026, on a throwaway server built from this branch on port 7955 with a data folder inside the worktree (removed afterwards), put together as `internal/app` puts it, with an invented search key in the test tenant's vault and the search pointed at a stand-in on this machine, on port 7956, that waited some seconds before answering; signed in as the test tenant, with the recorded research. From the new chat screen and from a chat: the control and its line with the budget before anything started; the card with its two lanes at work, then one done and one working, and the sources found; the note with its two citations as links, the report's card, the sources and the parts; the report open beside the chat, with its Sources and About sections; the same after a reload; in dark and light; at phone width with no sideways scroll; and Stop from the card, after which the note said the research was stopped. The only console errors were the sign-in probe's 401s. It has not been seen with a real model or the real web.

## Tables

In `internal/tenant/migrations/20261126T073341_research.sql`.

| Table | Holds |
| --- | --- |
| `research` | One row for each task: its two chats, title and question; its status, stage, and whether the switch holds it or a stop was asked; its own request id, the call that started it, its actor and model; its budget and spend, and the settings as it started (parts, sources for each, the report's length); its plan, the turns of its plan and its report, and the name of its batch of sub-agents; the sources and how many were read and taken out; the report as saved, its summary, whether it reached its budget and what else the person is told; the artifact and the chat it is in; its error; and the turn and message of its note. It goes with the chat it was started from; its own chat and its parts' stay. |

The parts are rows of `subagents`, with the task's chat as the chat they were started from and `research-<id>` as their call.

## In code

    r := research.NewRunner(research.Options{Install, Log, Config, Turns, Subagents, Artifacts, PartChars})
    turns := turn.NewRunner(turn.Options{..., SkipMemory: r.SkipMemory(inner)})
    r.Bind(turns)                         // the first turn runner given stays
    r.Recover()                           // once, when the server starts, after the sub-agents'
    routes = append(routes, research.Routes(r)...)
    r.Start(ctx, store, chatID, startedRequest, actor, research.StartBody{Question, Title, BudgetUSD})
    r.Stop(ctx, store, chatID, id), r.Terms(ctx, store)
    research.Budget(ctx, store)           // the person's budget for one task
    research.ReadConfig(get), research.DefaultConfig()
    cite.Check(report, read), cite.Remap(text, to), cite.Numbers(text), cite.Rewrite(text, to)

`internal/app` does all of this (`newResearch` in `internal/app/research.go`, and lines in `app.go`).

What it added to other parts, each in a file of its own:

    subs.StartParts(ctx, store, chatID, turnID, callID, model, []subagents.Part{{Title, Task, Context, BudgetMicros}})
    subs.Parts(ctx, store, chatID, callID)        // the batch's sub-agents, by place, each with where it is
    subs.StopParts(ctx, store, chatID, callID)    // stops those still working, all at once
    subagents.Ended(a), subagents.ErrPaused
    unattended.SetBudget(ctx, db, requestID, micros)

`StartParts` starts sub-agents for Keystone's own code: no model calls `run_subagents` for them, so each one's budget is the caller's and the limit on what one turn may start does not apply. Everything else is a sub-agent's: its own chat and request id, its registration as an unattended run, the supervisor, the pause switch, the marks carried down, the step limit, the routes and the lanes.

## Settings

In `settings/agents.json`. `research.ReadConfig` reads the install's, and `app.ReadConfig` calls it. The budget is read from the person's stored value when a task starts, and the number of parts, the sources for each and the report's length are kept on the task as it starts.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `agents.research_budget_usd` | 3 | tenant | The most one task may spend on models that are not local: its plan, every part and its report together. |
| `agents.research_report_share_percent` | 30 | install | The share of the budget kept for the plan and the report. The rest is split evenly between the parts. |
| `agents.research_parts` | 4 | install | The most parts a question is split into, from 1 to 12. |
| `agents.research_sources_per_part` | 4 | install | How many sources each part is asked to read. It is what the part is told; its step limit and its budget are what hold it. |
| `agents.research_report_words` | 1200 | install | About how many words the report is asked to be. |
| `agents.research_summary_max_chars` | 700 | install | The most characters of the summary written in the chat as the note. |
| `agents.research_open_max` | 2 | install | The most tasks one person may have under way at once. |
| `agents.research_max_rounds` | 6 | install | The most turns a task's own chat may take: its plan, its report, and one more each time a restart or a pause cut one short. |
| `agents.research_plan_prompt` | see the file | install | The plan step's instructions. `{parts}` becomes `agents.research_parts`. |
| `agents.research_part_brief` | see the file | install | What each part is given beside its task. `{sources}` becomes `agents.research_sources_per_part`. |
| `agents.research_report_prompt` | see the file | install | The report step's instructions. `{words}` becomes `agents.research_report_words`. |

`agents.subagents_at_once`, `agents.subagent_max_steps`, `agents.subagent_timeout`, `agents.subagent_result_max_chars` (the most of each part's findings the report step reads) and `agents.subagent_brief` ([sub-agents.md](sub-agents.md)) hold for the parts; `agents.unattended_fallback` and `agents.paused` ([unattended-spend.md](unattended-spend.md)) for the task and its parts; the web tools' settings ([web-search.md](web-search.md)); `artifacts.max_bytes` and `artifacts.title_max_chars` ([artifacts.md](artifacts.md)); and `chats.stream_poll` and `chats.stop_wait` ([turns.md](turns.md)).

## The test tenant

`fixtures/test-tenant/turns/research.json` holds four recorded turns, each answering by words of a shipped prompt, so any question asked with the Research control is answered: the plan (two parts, the museum's opening hours and what is on show), each part (a `web_search`, then its findings, one with a citation it made up), and the report (which cites two sources that were read and one that was not). The test tenant has no search key, so on the shipped server each part's search says "not set up", nothing is read, and the report cites no source and says how many citations were taken out; `verify` stores an invented key and points the search at a stand-in on this machine.

## Checked by

- `go test ./internal/research/...`, with the real HTTP server, the turn runner, the sub-agents' runner, a scripted provider and a stand-in search service on this machine:
  - **A test-tenant research task produces a cited report within its budget** (`TestAResearchTaskProducesACitedReportWithinItsBudget`, the item's proof): the plan is read as two parts; each is a sub-agent in a chat of its own that searches the stand-in; the report step is sent the findings in the research's own numbers, the page both parts read as one source, and no source nobody read; the artifact exists, as Markdown, in the chat that asked; every citation in it maps to a source the stand-in returned and a part's chat recorded, renumbered in the order they appear; the citation a part made up, the one the report made up and the model's own list of sources are gone, and two are counted; the note is the chat's last message with the summary, and the chat is marked as having read outside content; the task's own run and its parts could together spend no more than the budget; the plan, the report and both searches are in the audit log; no memory pass reads the task's chat; and another person gets 404 from every route.
  - **A task at its budget stops and says so** (`TestAResearchTaskAtItsBudgetStopsAndSaysSo`): with a budget too small for its plan it fails with `spend_cap`, says so in the chat, and the paid model is never called; with a budget its plan and its part fit in and its report does not, the report is put together from the part's findings, cited and checked, and the report and the note say it stopped at its budget; with a local model in the chain that model writes the report and the task names it; a negative budget is refused.
  - **A held chat** (`TestAHeldChatsResearchStaysWithModelsThatMayReadIt`): the task's chat and its part's are held, the hosted model is never called, the part's search is refused as a held chat's is and the stand-in is never searched, no citation stands, and the report is a sensitive artifact. **A held task** (`TestAHeldTasksReportStaysInItsOwnChat`): its report stays in its own chat, and the chat that is not held gets Keystone's words alone and no mark.
  - **Stop, the switch and a restart** (`flow_test.go`): Stop ends both parts and writes no report; the pause switch stops the plan step, holds a task started meanwhile with no call made, and both finish when it goes off; a restart while one part worked ends that part, and the task goes on to a report that says which part did not finish; an empty question, a start from a task's own chat and one task too many are refused.
  - **The plain functions** (`report_test.go`): the plan read as a list; the summary; a model's own list of sources taken off; only recorded `web_search` and `web_fetch` results are sources, not a number in a reply, another tool's result, a failed call or a source inside a page's text; a page searched and then fetched is one source read in full; a held part gives a chat that is not held nothing; and the report Keystone puts together.
  - `internal/research/cite`: a citation with no source is dropped and the rest renumbered; with nothing read no citation stands; a group of numbers; a link, a definition and a code block left alone; a part's numbers remapped.
- `go test ./internal/unattended`: a run's budget is changed, and a negative one and a run that is not registered are refused.
- `verify`, the standing check `research mode` (`tools/verify/research.go`): on the real server with the shipped settings and prompts, the test tenant's recorded research gives a Markdown artifact in the chat that asked, every citation in it names a source the stand-in returned to a part's recorded search, the two invented citations are gone and counted, the note is the chat's last message, the runs could together spend no more than the budget, and every call is in the audit log; and an invented person's task on a hosted model, with a budget too small for its plan, stops and says so without calling it. It was seen to fail with the citation check switched off. The routes are walked with every other route: `tenant isolation`, `calls recorded`, `no keys in output` and the isolation drill, whose `pathValue` has `{research}`.

## Not here yet

- **Parts cut short are not run again.** After a pause or a restart the report is written from what the parts had (decision 0523).
- **The task's own chat and its parts' chats stay in the sidebar**, as task chats and sub-agent chats do. The owner's question on sub-agent chats covers them.
- **Operations does not list research tasks.** It lists background tasks and scheduled runs; a research task's run is in the registry as a background task named "Research: …", and its spend is in the usage records.
- **The pause routes' counts** (`working`, `held`) leave research tasks out, as they leave sub-agents out.
- **A model cannot start research.** Only the person does, from the composer.
- **The question is not a message of the chat.** No turn is sent for it: the card and the note carry it, and a chat that held nothing else reads "Nothing has been said in this chat yet." above the card.
- **A model picked for the next message is not used.** A task runs on the model the chat would use by itself, as a background task does.
- **Files attached to the message are left out**, and the line above the composer says so.
- **Citations as links inside the panel.** The note's citations are links; the report in the artifact panel has its numbers as text and its sources as a linked list.
- **Two calls at once** can each pass a budget's check and together spend more than it by one call's cost, as at the cap.
- **Reading PDFs and pages built by script**, which `web_fetch` does not do yet.
