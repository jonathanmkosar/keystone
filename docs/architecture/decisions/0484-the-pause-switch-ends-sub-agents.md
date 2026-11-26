# 0484. The pause switch ends sub-agents, and a call while it is on starts none

November 26, 2026 · decided in P8-02

## The question

The switch that pauses every agent stops a background task part of the way, and the task goes on with a new round when the switch goes off (decision 0391). A sub-agent belongs to a turn that is waiting for its result. Should it wait too, and what does a call made while the switch is on do?

## The decision

- **Switched on, every sub-agent still working is stopped** through its turn's context, at its supervisor's next look (at least every `chats.stream_poll`), and **ends as `paused`**. One still waiting for a lane ends at once. The parent turn then has every result and goes on, told they were paused.
- **A paused sub-agent does not go on** when the switch goes off: its turn has its result already.
- **A call made while the switch is on starts none**, and tells the model why.
- Sub-agents are not counted in the switch's `working` and `held`, and the pause route does not wake them.

## Why

- Holding a sub-agent would hold the person's own turn, which the switch is not meant to pause, for as long as the switch stays on.
- What a paused sub-agent did is in its chat and in its section of the result, so nothing is lost.
- Reading the switch at every look needs no change to the switch's routes or to `internal/jobs`, which other lanes share.

## What it costs

- The pause route may answer before a sub-agent has stopped; it stops within a look.
- A person who resumes must ask again for the work the paused sub-agents did not finish.
