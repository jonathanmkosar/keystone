# 0520. Research is three steps run by Keystone's code, on turns and sub-agents

November 26, 2026 · decided in P8-04

## The question

The plan says research mode is a long multi-source report built by sub-agents. A model can already start sub-agents with `run_subagents`, and a background task can already run for a long time. Is research a task whose prompt tells a model to plan, call `run_subagents` and write the report, or do the steps belong to Keystone?

## The decision

**The steps are Keystone's.** A research task is a row worked by a goroutine under the turn runner, as a background task is. It sends one turn of a chat of its own for the plan, starts one sub-agent for each part itself, through `subagents.Runner.StartParts`, and sends one more turn of its own chat for the report. A model never decides how many parts there are beyond the list it writes, never starts the parts, and never saves the report.

## Why

- **The limits are real only in code.** The number of parts, the split of the budget, the numbering of the sources and the check on the citations each need a point where Keystone holds the work between two model calls. A prompt that asks a model to keep to them is a request.
- **The citation rule needs the sources in hand.** Between the parts and the report, Keystone reads what the parts' recorded searches returned, numbers it once, and afterwards checks the report against it. A model that called `run_subagents` and wrote on would be checked against nothing.
- **Progress is known.** The plan, the parts and the report are stages in a row, so the progress view says where a task is without reading a model's words.
- **Nothing is built beside the turns.** Each step is still a turn or a sub-agent, so streaming, the chat record, the audit log, approvals, the sensitive rules, budgets, the pause switch and stop are the ones already built and checked.

## What it costs

- A fixed shape: plan, parts, report. A question that needs a second round of parts after the first does not get one.
- `internal/subagents` gained a second way in, for code, beside the tool. It repeats the tool's writing of a sub-agent's row, since the tool's own checks are of a model's call.
- The plan is read from a model's list by its lines. A reply that is no list becomes one part, the question itself.
