# 0400. A project's memory is a space of the knowledge store

November 25, 2026 · decided in P9-01

## The question

The plan gives projects "project-scoped chats and memory". The memory pass writes durable facts to the person's knowledge store, and recall reads them back. How is a project's memory kept apart from the rest, and how does a chat in the project reach it first, with the store interface as it is?

## The decision

**A project's memory is a space.** The interface has spaces and no tags or fields on a source, and a space is what every adapter can write to and search alone (`NewSource.Space`, `Query.Space`). Each project names its space when it is made, from `projects.memory_space` (`project-{slug}` by default), and keeps it when it is renamed; the person can change it or clear it.

**The memory pass writes a project chat's facts there**, in place of `knowledge.memory_space`. A store makes a space it does not hold as a normal space. A chat that was sent sensitive content is not changed: its facts go to the confirmed sensitive space or nowhere (decision 0281), whatever its project.

**Recall in a project chat prefers it.** The store recall searches is wrapped (`projects.Prefer`): a search of every space asks the project's space first and then the whole store, and gives the project's hits first, each once, within the search's limit. Every hit keeps the label its store gave it, so recall's rule 3 is untouched. `projects.recall_prefer` turns it off.

## Why

- It works with Waymarks, the built-in store and any later adapter today, with no change to the interface or its contract suite, which other lanes are also using.
- A space is visible to the person in their store, so project memory is something they can see, move and correct with the store's own tools.
- Wrapping the store, rather than changing recall, keeps recall a guard of rule 3 exactly as it was: the wrapper reorders hits and never touches a label.
- Naming the space once keeps a renamed project's memory together.

## What it costs

- A fact lives in one space: a fact that belongs to a project and to the person in general is in the project's space only. Tags in the interface would allow both; they are a change to the knowledge lane's interface, left for later.
- The project's hits come first within the search's limit, so a project with many weak matches can crowd out a strong match elsewhere. Recall's relevance threshold still drops the weak ones from the call.
- The knowledge lookups the model calls itself do not prefer the project's space yet.
