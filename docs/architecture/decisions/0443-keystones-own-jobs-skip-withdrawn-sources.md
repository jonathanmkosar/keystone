# 0443. Keystone's own jobs skip withdrawn sources

November 25, 2026 · decided in P8-06

## The question

Waymarks keeps every source unchanged, so when the person forgets a fact on the Memory screen, Keystone withdraws the source: it keeps its id and leaves it out of every search, read and citation it makes (decision 0371). That decision left an owner question: should Keystone tell Waymarks' agents about a withdrawn source, with a notice source under the intake token, so the upkeep jobs do not compile it into a page? With P8-06 the upkeep jobs are Keystone's own.

## The decision

**Keystone's jobs never see a withdrawn source.** Their tools leave it out of `list_sources`, `search` and the built-in store's `list_uncompiled_sources`, and `read_source` refuses it with a sentence saying the person asked Keystone to forget it. The check of an answer refuses a citation of it, so even a model that knew its id cannot put it on a page.

No notice source is sent. Agents outside Keystone that read the same Waymarks are not told.

## Why

- A filter Keystone applies itself is certain; a notice source relies on an agent reading it and obeying it, and it puts another source into the wiki for every fact forgotten.
- After the cutover (P12-01), Keystone's jobs are the only agents that write to the owner's Waymarks, so the filter covers every writer.

## What it costs

- An agent outside Keystone, such as New Keystone's before the cutover, may still compile a withdrawn source. The owner's question of decision 0371 stands for those, with the recommendation that the notice is not needed once the cutover is done.
- A page compiled from a source before it was withdrawn keeps its fact until a job or the person changes the page.
