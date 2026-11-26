# 0483. A held sub-agent's result reaches only a held parent, and outside marks go both ways

November 26, 2026 · decided in P8-02

## The question

A sub-agent's words come from its parent, and its result goes back. Fixed rule 2 says content an agent read never grants approval; fixed rule 3 says sensitive content never reaches a model that may not read it. What must travel with the words, each way? A registry tool does not know the parent turn's route, so it cannot tell whether every model the parent may reach could read sensitive content.

## The decision

The rule is `internal/subagents/carry`, two functions on plain marks, which imports nothing of Keystone's and is a guard of rules 2 and 3 in `verify`:

- **Down**: a sub-agent's chat starts with every mark of its parent's, written before its turn can run: held for sensitive content when the parent is, and marked as having read outside content when the parent has.
- **Up**: the result of a sub-agent that was sent sensitive content reaches the parent's turn only when the parent chat is held too; otherwise the parent is told the result stays in the sub-agent's chat. A result that passes and read outside content marks the parent chat first, and opens with the outside mark. A mark that cannot be written gives no result at all.

What Keystone itself says of a sub-agent, its status, steps, spend and refusals, always passes.

## Why

- A held parent's route reaches only models that may read sensitive content, so its sub-agents' results are safe there; any other parent may reach a hosted model.
- Holding the parent mid-turn would not change the route the turn already chose, so passing the result and holding afterwards could leak it.
- The mark on the result and on the chat makes every later side effect in the parent ask, as a web page read in the parent would.

## What it costs

- A parent that is not held, whose every model could read sensitive content (a person with local models only), still does not get a held sub-agent's result. The person can open the sub-agent's chat. A per-call tool made for the parent's route, as the past-chat tools are, would lift this; it is left for later.
