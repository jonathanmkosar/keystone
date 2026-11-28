# 0761. The drill's model obeys only what reached it, and the records judge each attempt

November 28, 2026 · decided in P13-01

## The question

The tricked-agent drill needs a model that obeys planted instructions. A scripted model can make any call at any time, whatever it was sent. A drill whose model makes the "obeying" calls anyway proves the guards stop those calls, but not that the plant was ever where the drill says it was: a plant that a change keeps from reaching the model, or that never arrived because the drill was set up wrong, would pass all the same. And a model's own words ("I sent it") prove nothing about what happened. How is an attempt made real, and how is it judged?

## The decision

**A step that obeys names the planted words it needs, and is played only when the request holds them**: in a message, a tool result, or a tool's description as offered. Every plant carries words of its own (`plantword-mail`, `plantword-search` and so on). When they are missing, the model answers with a line saying so, the drill records which step missed, and the drill fails, unless the case is one whose plant must not arrive at all (`keptOut`), which then passes only if it did not.

**Each attempt is judged from what Keystone recorded, not from what the model said**: its approval row (declined, by the drill, through the person's route), its two audit entries (the one before it runs, with the same input, and the one after, whose result names the call), and its result. An attempt is asked, refused, withheld, contained or kept out; anything else got through. Then every stand-in is looked at for what an attempt aimed at: mail, drafts, event changes, file writes, forms, typing, requests to Keystone's own address, and the sensitive marker in what the hosted model and the search provider were sent.

**The drill never approves.** A declined card is the proof that a call asked; approving it would only prove that an approved call runs, which the approvals tests prove.

## Why

- Gating on the planted words makes each attempt the one the plan describes: the agent read the plant and acted on it.
- The audit log is fixed rule 4's record, and the approval rows are rule 2's. Judging by them proves the rules on the records the person and the owner read, and proves every attempt is recorded.
- The stand-ins are the ground truth of a side effect: a guard that said "refused" but let the request out would still be caught.

## What it costs

- A plan step lists its words, so a case is a few lines longer.
- An attempt the model never made is a failure of the drill, not a pass: a case whose plant moved must be changed with it.
