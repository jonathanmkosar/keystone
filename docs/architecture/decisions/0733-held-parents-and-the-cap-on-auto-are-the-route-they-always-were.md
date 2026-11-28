# 0733. Held parents and the cap on Auto are the route they always were; the router is not told about the cap

November 28, 2026 · decided in P14-06

## The question

The spec says a held parent's sub-agent is held (decision 0483) and that at the cap unattended work falls back to a local model or stops (decision 0392). On Auto, does either need code of its own: a check that a held sub-agent's verdict cannot name a hosted step, or a router that moves down the ladder at the cap?

## The decision

- **Held parents: nothing new.** `carry.Down` holds the sub-agent's chat when its turn is written, before it runs, so the router reads it as held and its rule 1 leaves out every step that may not read sensitive content, whatever the verdict says. The router is a guard of fixed rule 3 already, checked in `verify`.
- **The cap: nothing new.** The step the router chose is the ask of the same route (`routedChat`), whose chain asks the monthly cap and then the run's budget, and whose `Moved` hook is `unattended.Moved`. A refused hosted step falls back to the next model of the person's chain with a notice, or stops when fallback is off.
- The router is not told about the cap in this item. Teaching it to skip hosted steps at the cap is an owner question.

## Why

- Each rule has one guard. Code that repeated the router's held rule, or the gate's cap, for unattended work could drift from them; going through them means a test of the whole path (a held parent's sub-agent with a verdict naming the hosted step) proves the real guard.
- Skipping hosted steps at the cap changes what Auto does for a person's own chats too, and what the reason line says, which is the router's and P14-05's to decide with the owner.

## What it costs

- At the cap, a ladder whose local step is not also in the person's fallback chain stops unattended work instead of moving down to it. Raised as a question in journal agents 016, with the recommendation to skip hosted steps at the cap as a down local machine's steps are skipped.
