# 0371. With Waymarks, a forgotten source is withdrawn in Keystone, its other facts are added back under the intake token, and no issue is raised yet

November 25, 2026 · decided in P6-07

## The question

Forgetting a fact removes its source from a store that can (decision 0370). Waymarks has rules of its own: a source is kept unchanged and there is no call that removes one; erasing one for good is `POST /sources/{id}/erase`, which only the instance's owner may call, with the owner's token; people do not write pages, they raise issues, and agents with the write and delete grants fix them. Issues point at a page and quote its text. Keystone holds three agent tokens: a reader, an intake grant that only adds sources, and an embedder (decision 0090). What does forgetting and correcting do with Waymarks?

## The decision

**The Waymarks adapter does not claim `RemoveSource`.** Its call answers "not supported". Keystone never holds the owner's token: a person's admin key in an assistant's vault is the opposite of what Waymarks' grants are for.

**Keystone withdraws the source.** `knowledge.Withdraw` keeps its id, for the adapter, in the tenant's own database (`knowledge_withdrawn`, migration `20261125T193700_memory_screens.sql`). Every store `knowledge.Open` hands back for the tenant leaves a withdrawn id out of its searches, its reads and the citations of its pages. So recall, the knowledge lookups and the memory pass never read or find it again, and the source is gone from everything any model is sent through Keystone. The row keeps no word of the source.

**The other facts, and a corrected one, go back under the intake token**, as a new source: a correcting source in the plan's words. A Waymarks without an intake token cannot take them, and a change that would keep facts is refused; withdrawing a source whole still works.

**No issue is raised.** An issue needs a page to point at, and the raise-issues grant, which none of Keystone's three tokens has. A memory source is cited by no page until the wiki upkeep jobs compile one (P8-06).

**The screen says so.** Its note for a store without `RemoveSource` says the store still holds the withdrawn source until its owner erases it there, and the reply to a change says the same.

## Why

- It follows Waymarks' rules as they are: nothing Keystone does removes a source, and the owner alone erases.
- Withdrawing is what makes "gone from recall" true now, for any store that keeps its sources, with no new token and no call Waymarks lacks.
- Keeping the list in Keystone means a second store, or a store swapped in later, is not touched by it: an id belongs to one adapter.

## What it costs

- The instance still holds the source, and Waymarks' own agents and screens can read it. The owner erases it there (`docs/runbooks/erase.md` in Waymarks), with the source's number, which the screen shows.
- Waymarks' upkeep agents do not know Keystone withdrew it, and could compile it into a page. A notice they could read, without the forgotten words, is an owner question.
- A withdrawn id is read on each search that finds a source and each read of a source or of a page that cites one: one small query in the tenant's own database.
