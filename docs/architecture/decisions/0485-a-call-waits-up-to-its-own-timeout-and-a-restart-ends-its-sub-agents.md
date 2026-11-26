# 0485. A call waits up to its own timeout, and a restart ends its sub-agents

November 26, 2026 · decided in P8-02

## The question

A tool may run for `agents.tool_timeout`, 60 seconds as shipped. Sub-agents may take longer, and one may wait for the person's approval for up to a day. How long does `run_subagents` wait, and what becomes of its sub-agents when Keystone restarts in the middle?

## The decision

- **A tool may carry a limit of its own** (`tools.Tool.Timeout`), used when it is longer than the call's. `run_subagents` has `agents.subagent_timeout`, 30 minutes as shipped, a wait for approval included. Past it, the supervisor stops the sub-agents still working and each ends as `failed` with `timeout`; the turn goes on with the tool's timeout error.
- **A restart ends them.** The turn runner closes the parent turn as failed (`interrupted`, decision 0142); then `subagents.Runner.Recover` stops any sub-agent turn the turn runner put back in its queue because it waited for an approval, and ends every unfinished sub-agent as `failed` with `interrupted`.

## Why

- A sub-agent waiting for the person should not be cut off after a minute, nor hold a turn for a day.
- Nothing waits for a sub-agent's result once its parent turn is closed; running it on would pay for work no one reads (as decision 0142 has it for a turn).

## What it costs

- A person who leaves an approval in a lane for half an hour finds the sub-agent ended; they ask again.
- A restart loses the sub-agents' work in flight, though what they did is in their chats.
