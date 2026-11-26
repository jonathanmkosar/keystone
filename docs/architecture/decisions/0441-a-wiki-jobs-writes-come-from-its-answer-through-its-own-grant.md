# 0441. A wiki job's writes come from its answer, through its own grant

November 25, 2026 · decided in P8-06 · provisional on the owner's answer

## The question

A compile job reads sources and writes pages. Under Keystone's approval rule (fixed rule 2, P7-02), a source of the knowledge store is outside content, and once a chat has read outside content every tool that changes something asks the person, one call at a time, whatever its policy. A Waymarks `write_page` tool would therefore wait for the person on every page of every run, and the jobs would not be unattended, which is what the plan asks of them. Making the write tools say they only read, or taking sources out of outside content, would weaken the rule. How do the jobs write without either?

## The decision

**A job writes nothing with a tool.** It is offered only tools that read. It ends its run with one JSON answer, in the shape its instructions give: the batches it wants (page writes and deletes) and the issue changes, with a report. When the run is done, Keystone's own code, not the model, writes the answer:

- **Checked whole first** (`internal/wikijobs/scope`, which reads no setting): the answer may ask only for what the job's grant allows (the agent kit's grant for that job), may cite only sources the job may read and the person has not withdrawn, may put a sensitive source's content only in a sensitive space, and may write at most `agents.wiki_max_batches` batches and `agents.wiki_max_new_pages` new pages. Anything else and nothing at all is written.
- **Written only to the knowledge store, through the job's own grant**: Waymarks batches under the job's own token, which Waymarks checks again on every call and records under the job's principal; or pages of the built-in store.
- **Each batch is one change** the owner can revert in Waymarks as a unit, and each write is in Keystone's audit log under the run's request id. A note in the run's chat says what was written.

This is the memory pass's way: a model's answer is read, and Keystone's code adds what it says to the person's own store, under a standing switch the person turned on. The job's switch, and the grant the owner gave its principal in Waymarks, are that standing consent; content the job read cannot widen either.

## Why

- Rule 2 governs side effects outside Keystone. A page written into the person's own knowledge store, through a grant that allows nothing else, is Keystone keeping its own memory, as the memory pass does with sources. The plan already counts pages as Keystone's own content because "the person and Keystone's own upkeep write it" (approvals, P7-02).
- The approval package is left as it is: no new way to make an approval, no tool marked as reading when it writes, sources still outside content.
- The agent kit is built for unattended agents with exactly these guards: one principal and grant per job, batches that revert as a unit, and a wiki that suspends an agent whose batches leave too many problems.

## What it costs

- The job cannot see a write's result before its next step: within one answer, a new page is linked to by a `ref` that Keystone resolves to the id Waymarks gives (`[[new:ref]]`), and a page written before the page it links to is written again in the same batch, as the kit's compile job does by hand.
- An answer is all or nothing at the check; at the write, a refused write drops its batch and stops the rest.
- **The owner's question**: should a wiki job's writes after it read sources run under its grant without his answer to each, as built, or should each batch ask in the run's chat? Recommended: as built; the grant, the revertible batch and Waymarks' suspension are the guards the kit intends. Until he answers, it is built as recommended and marked provisional.
