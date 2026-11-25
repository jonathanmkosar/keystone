# Recall

Folder: `internal/knowledge/recall`

Recall brings what a person's knowledge store holds into a chat. With each message the store is searched, the hits at or above a relevance threshold are read, and each model call is given them, labelled with where they came from. Three lookups let a model go deeper: search the store, read a page, read a source. Content from a sensitive space reaches only a model that may read it (fixed rule 3).

The rest of this doc, with the decisions behind it, comes in the commit after the code.
