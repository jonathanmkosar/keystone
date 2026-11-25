# 0280. The memory pass's reserve is a part of the monthly cap, and a refused call moves on to a model that costs nothing

November 25, 2026 · decided in P6-06

## The question

The plan gives the memory pass "its own spend reserve". The memory pass runs after every turn, unasked, so with a hosted model behind it, it could spend a share of the month that the person never chose to spend. What is the reserve: a limit on the memory pass inside the monthly cap, or a part of the cap set aside for it, so that chats stop earlier and the memory pass can still run when they have spent the rest? And what happens when it is reached?

## The decision

**A limit inside the cap.** `knowledge.memory_reserve_usd`, $2 a month by default, is the most the memory pass may spend in a calendar month on models that are not local. What it spends counts against the reserve and against the monthly cap. Chats are not limited by it at all.

**The gate.** The memory route's gate asks the reserve and then the cap before each of its models. A model that is not local is refused when this month's memory spend (`usage.SpentOn`, the usage records of the kind `memory`) and the most the call could cost, worked out as the cap works it out, are together more than the reserve. A local model is never asked about. A reserve that cannot be read stops the call.

**A refused call fails with the kind `spend_cap`**, the kind the cap uses, so the route moves on as it does at the cap (decision [0160](0160-a-capped-call-falls-back-to-a-local-model.md)): a local model behind it reads the exchange at no cost. With none, the pass is skipped and says why, with the month's memory spend, the reserve and what the call could have cost. The chat's reply was given before the pass began, so nothing the person sees is stopped.

**The person may set it.** The scope is `tenant`: raising it cannot take a person past the cap the admin set.

## Why

- "So the memory pass stops before it eats the monthly cap": a limit inside the cap does exactly that, and changes nothing about how the cap treats a chat, which the `spend cap` check proves and the person already understands.
- Setting a part of the cap aside would lower every person's chat cap by the reserve, unasked, to protect a job that runs in the background. The owner's memory pass runs on his local model and costs nothing, so for him that part would sit unused.
- Using the cap's own error kind means the fallback chain, the audit log's `refused` outcome and the usage record of a call that cost nothing all work as they do at the cap, with no new case.
- $2 is a few hundred passes on a cheap hosted model and is small against the $100 cap. It is there for when the owner's local model is down and the route falls back to a hosted one.

## What it costs

- At the cap, a chat stops and the memory pass stops with it: nothing is held back for the memory pass. With a local model in the route it goes on, at no cost.
- Two memory passes of two chats at the same moment each read the month before the other is recorded, as two chats do at the cap, so the reserve can be passed by one call's cost.
- The reserve's warning threshold is not used: nothing tells the person the reserve is near. The skipped pass says so once it is reached.
- An owner question remains: whether the reserve should instead be set aside from the cap. Recommended: no, as above.
