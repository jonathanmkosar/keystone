# 0731. Only the call that started the work names its model; the parent chat's pick does not

November 28, 2026 · decided in P14-06

## The question

Before Auto, a background task, a research task and a sub-agent with no model ran on "the model the chat it was started from would use next". A person who picked a large hosted model by hand in a chat, and then started a task from it, had the task on that model. Is that pick the caller naming a model, so the task stays off Auto? And what can each kind of work name?

## The decision

- A model is named only by the call that started the work: a schedule's `model`, a sub-agent's `agents[].model`, and `StartParts`'s `model` for research parts. The chat it was started from, on Auto or not, names nothing.
- A background task and a research task take no model, so they start on Auto whenever Auto is offered (decision 0730).
- Research tasks and their parts follow the same rule as background tasks and sub-agents, since that is what they are.

## Why

- The spec's words are "unless the call that started them names a model". A chat's pick is how the person wants that chat answered, while they watch it; unattended work is where Auto saves the most, since nobody is there to pick.
- One rule for every kind of work is easier to say on the screens and in the docs than one that depends on how the parent chat was last answered.
- A sub-agent that must use one model can still be given it, by the model that starts it.

## What it costs

- A person who picked a model by hand in a chat and starts a background task there gets Auto for the task, and cannot name a model for it until `StartBody` takes one. Listed in "Not here yet" in `unattended-auto.md`.
