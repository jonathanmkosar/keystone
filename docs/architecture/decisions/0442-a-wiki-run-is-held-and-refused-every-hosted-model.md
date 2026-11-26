# 0442. A wiki run is held from its first call and refused every hosted model

November 25, 2026 · decided in P8-06

## The question

A wiki job reads the person's knowledge store, which may hold sensitive pages, so the brief says it runs on a local or trusted model only, never a hosted one. A run is an ordinary background task in a chat of its own: its route is the schedule's model and then the person's fallback chain, which may hold hosted models, and the chat later gets a recap and maybe compaction from the summary model. How is every call of a run kept to models that may read sensitive content, without new code in the turn's routing?

## The decision

Three guards, each a part that was already there:

1. **The job's model.** A job whose model (its own, or `routing.memory_model`) is not local or trusted cannot be switched on or run now, and its schedule is paused if its model becomes one (`scope.MayServe`).
2. **The chat is held.** At the run's first model call, when the turn asks the connectors for its tools, the run's chat is held for sensitive content (`recall.Hold`), as recall holds a chat that received a sensitive hit. From then on every call of the chat (the task's later steps and rounds, the recap, compaction) is planned as sensitive and goes only to local or trusted models; the web tools are off there, and the notes Keystone writes in it keep the hold.
3. **The gate.** `wikijobs.Gate` wraps the turn's gate (the background tasks' budget gate): any call of a wiki run's turn to a hosted model is refused before it is sent, the first call included, with the kind `unavailable`, so the route goes on to its next model and the run fails if none is left. A turn whose chat cannot be told is taken as a wiki run's.

And **no memory pass** reads a run's chat (a new turn option, `SkipMemory`), so a job's report never returns to the store as a source.

## Why

- The hold is the rule-3 mechanism every other part already obeys; holding the run's chat puts all its later calls under it with no new routing code.
- The hold comes at the first call's tool offer, after that call's route was planned; the gate covers that first call, and any fallback, whatever the hold.
- `unavailable` is a kind the chain falls back on and that the unattended hook ignores, so a refused hosted model is never reported as the spend cap, and the person's choice about falling back at the cap does not decide rule 3.

## What it costs

- The first call of a run is recalled for and planned like any first call; recall attaches what it finds to the job's instructions, filtered for the model as always.
- A person who chats in a run's chat afterwards stays on local or trusted models there, as in any held chat.
