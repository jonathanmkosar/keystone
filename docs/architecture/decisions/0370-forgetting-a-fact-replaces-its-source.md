# 0370. Forgetting or correcting a fact replaces its source, and only what the memory pass added can be changed

November 25, 2026 · decided in P6-07

## The question

The plan's memory screens let a person view, correct and forget what Keystone remembers. The memory pass adds the facts of one reply as one source, and every store keeps a source unchanged. A person thinks in facts, not sources. What does forgetting or correcting one fact do to the store, what may a person change, and how is a fact made gone from the store and from recall?

## The decision

**The unit is a fact; the store's unit stays the source.** Forgetting a fact adds the source's other facts back as one new source, with the old one's space, name, origin and date, and then the old source goes. Correcting does the same with the fact rewritten. When no fact is left, no new source is added.

**A new capability, `RemoveSource`, and a call of the same name on `knowledge.Store`.** A store that has it takes the source out for good: it can no longer be read or found, and a page that cited it no longer does and moves on a revision. The built-in store has it, and deletes with SQLite's secure delete on and optimises its source index afterwards, so the words are written over in the file and leave the index. The contract suite has a `remove source` check, and two faults it must catch.

**Only what the memory pass added.** The list and every change go through the memory pass's own ledger, `memory_passes`, in the person's own database. A source the ledger does not name is refused, so a person cannot remove what came from elsewhere, and cannot reach another person's store (fixed rule 1). The ledger row follows the new source and keeps no word of the forgotten fact.

**The order is add, then remove.** The facts that are kept go back first, so a failure leaves the old source as it was; if the old one then cannot go, the new one is taken back.

**Fixed rule 3.** The new source goes to the old one's space. When the old one was sensitive, the new one must land sensitive, or it is taken back and the change refused (`ErrLanded`). No model is asked anything.

**The profile follows.** A profile line that says the fact (list mark, letter case, spaces and a closing full stop aside) is taken out, or corrected. The map holds pages and no source, so a forgotten fact was never in it, and it is rebuilt as before when a page changes.

## Why

- A source is unchanged in both stores, Waymarks by its own rules; replacing it is the one change every store can follow.
- Fact by fact is what a person reads on the screen, and the pass's ledger already holds the facts of each source.
- Going through the ledger is the plainest way to keep "forget" to Keystone's own intake, which is also what Waymarks' rules allow (decision 0371).
- Adding before removing never loses a kept fact to a failure halfway.

## What it costs

- A source's id changes on every change, so a page that cited the old source does not cite the new one. Nothing compiles memory sources into pages yet (P8-06).
- Two changes to one source at the same moment: the second finds the ledger moved on and is refused as "not added", and the person reads the screen again.
- What a remove does not reach: a backup made before it; the audit log's record of the memory call that found the fact (fixed rule 4, never changed); the recall records of past turns, which name what they found; and the chat the fact came from, which is the chat record's and is deleted with the chat.
