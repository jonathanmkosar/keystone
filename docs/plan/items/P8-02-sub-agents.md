# P8-02. Sub-agents

Status: doing (agents)
Lane: agents + interface
Depends on: P8-01
Owner gate: Approval of the agent mockups (P1-05), for the lanes only.
Built without the gate: The server side and its tests; the screen built from the draft mockup with design tokens, marked provisional until the mockup is approved.
Scope: Parallel sub-agents with their own context and budget, shown as lanes.
Proof: Three sub-agents run in parallel lanes and their results reach the parent turn.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan (agents, November 26, 2026)

Built on the branch `agents/p8-02-sub-agents`, cut from `reboot` at 9a3b55a.

**The shape.** A sub-agent is built on turns, as a background task is (decision 0290): each works through one ordinary turn of a chat of its own, its work chat, titled "Sub-agent: …". So every model call and tool call it makes streams, is kept, is recorded under its own request id, asks under the approval policy, is held by the sensitive rules and is stopped the way a turn is, with no second loop. Its context is that chat alone: one message with a brief from settings, its task and what the parent passed, and none of the parent's history.

**Server side, a new package `internal/subagents`:**

- The tool `run_subagents` (`agents`: one to `agents.subagents_per_turn` of `{task, title?, context?, model?, budget_usd?}`). It writes each sub-agent's row and work chat and registers it with `unattended.Register` as `KindSubagent`, with its budget (the person's `agents.subagent_budget_usd`, or a smaller one the model asks for), in the same transaction. It refuses in a sub-agent's own chat (no nesting), past the most per turn, and while every agent is paused. It runs at most `agents.subagents_at_once` at a time and waits for all of them, up to `agents.subagent_timeout`; its result is a section per sub-agent (status, steps, spend, its last reply cut at `agents.subagent_result_max_chars`, and its chat's id for the whole transcript).
- Marks both ways: a sub-agent's chat starts with the parent's marks (outside content, held for sensitive content, incognito). A sub-agent that read outside content marks the parent chat and its result opens with the outside mark; a held sub-agent's result reaches only a parent chat that is held, and otherwise stays in its own chat (fixed rule 3).
- Approvals: nothing new. A side effect of a sub-agent asks in its turn; its lane in the parent chat draws the card and answers it through the approval routes.
- Spend: the per-run budget gate and the fallback or stop at the cap (`unattended.Gate`, `unattended.Moved`) hold with no new code once it is registered; when a sub-agent ends, its notices are told in its chat (`unattended.Tell`) and summed into its result (`unattended.Summary`).
- The switch: the supervisor reads `agents.paused` at every look; switched on, every running sub-agent's turn is stopped through its context and the sub-agent ends as `paused`, and the parent turn gets the results at once.
- Its own step limit, `agents.subagent_max_steps`, through one new turn option, `turn.Options.MaxSteps`. The memory pass skips a sub-agent's chat (`SkipMemory`). After a restart, sub-agents left unfinished are closed as interrupted, as their parent turn is.
- Routes: `GET /chats/{id}/subagents` (by `call` or `turn`), `GET /chats/{id}/subagents/{sub}`, `POST /chats/{id}/subagents/{sub}/stop`.
- Shared code, kept small: `tools.Tool.Timeout` and `tools.Input.CallID` in `internal/tools`, `turn.Options.MaxSteps` and one line in `internal/turn/tools.go`, added lines in `internal/app`.

**Lanes in the chat** (`web/app/src/Subagents.tsx`, provisional on the agent mockups, P1-05): the `run_subagents` step is drawn as the approved row "Ran 3 sub-agents in parallel" with a lane under it for each sub-agent: its title, state, steps as they happen, spend against its budget, its result, Stop, a link to its chat, and its approval card when it waits for the person.

**The proof**, with the fake provider: three sub-agents overlap in time (fake calls that wait for each other) and the parent's next call carries their three results; a sub-agent past its budget stops and says so; the pause switch stops them within seconds. A standing check `sub-agents` in `verify` runs it on the real server.

**Left for later:** sub-agent chats stay in the sidebar as task chats do (an owner question); a sub-agent may still be offered `start_background_task`, which asks as everywhere; research mode (P8-04) builds on this.

