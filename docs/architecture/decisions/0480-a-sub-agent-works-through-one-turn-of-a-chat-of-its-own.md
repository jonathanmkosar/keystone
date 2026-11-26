# 0480. A sub-agent works through one turn of a chat of its own

November 26, 2026 · decided in P8-02

## The question

The plan's sub-agents run in parallel with their own context, and their results return to the turn that started them. A sub-agent needs a tool loop, context building, routing, recording, approvals, the sensitive rules, budgets and stop. Does it get a loop of its own inside the parent's tool call, or is it built on what turns already do?

## The decision

**A sub-agent is one ordinary turn of a chat of its own**, its work chat, made when `run_subagents` is called and titled "Sub-agent: …", as a background task works through turns of its own chat (decision 0290). Its message is `agents.subagent_brief`, its task and the context the model gave it, and nothing else of the parent chat. The tool writes each sub-agent's row and chat, and a supervisor under the turn runner sends their turns a few at a time (`agents.subagents_at_once`), follows them, and hands the tool its result when all have ended. Each sub-agent's turn is sent under a request id of its own.

Its step limit is its own, `agents.subagent_max_steps`, through one new turn option, `turn.Options.MaxSteps`, asked for the step limit of a chat's turns. No memory pass reads its chat.

## Why

- Everything a turn does holds for a sub-agent with no copy: streaming, the chat record, the audit log under its request id, the approval policy, recall's holds and routing for sensitive content, the budget gate and the cap's fallback, stop through the turn's context, and recovery after a restart. A second loop would have to keep all of that in step.
- Its own context falls out of the chat: the context builder sees only its message.
- Its whole transcript is a chat the person can open, and `read_chat` can read.
- Turns of different chats already run in parallel; only the number at once needed a limit.

## What it costs

- Each sub-agent is a chat in the sidebar, as a task's chat is: three a call add up (an owner question).
- A sub-agent is offered every tool its parent is, `start_background_task` among them, which asks as it does everywhere; `run_subagents` refuses in a sub-agent's chat.
- The turn runner gained one option and `internal/turn/tools.go` one changed line.
