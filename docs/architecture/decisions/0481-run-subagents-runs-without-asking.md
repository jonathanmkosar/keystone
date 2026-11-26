# 0481. run_subagents runs without asking

November 26, 2026 · decided in P8-02

## The question

`start_background_task` asks before it runs, because it starts work that spends money without the person watching (decision 0295). Should starting sub-agents ask too, and should it always ask once the chat has read outside content?

## The decision

**`run_subagents` says it only reads and that its result is Keystone's own**, as the artifact tools do (decision 0420), so it runs under the default policy `always` and does not ask because a chat read outside content. A person can set it to ask or never on Settings → Approvals.

## Why

- It changes nothing outside Keystone and loses nothing: it makes chats and runs turns.
- Everything a sub-agent does that changes something asks the person, in its own turn, under the same rule as any turn, and its chat carries the parent's outside mark, so a side effect there always asks after outside content (decision 0483).
- Its spend is bounded: each sub-agent has its own budget, held to the person's `agents.subagent_budget_usd`, and a turn starts at most `agents.subagents_per_turn`. The monthly cap applies to every call.
- Unlike a background task, sub-agents run inside a turn the person is watching, and end with it.
- A result that brings outside content back is marked and marks the chat, so "Keystone's own" never hides outside content from the approval rule.

## What it costs

- Content a model read could lead it to start sub-agents and spend up to their budgets without a question. The budgets and the per-turn limit bound that; a person who wants a question sets the tool to ask.
