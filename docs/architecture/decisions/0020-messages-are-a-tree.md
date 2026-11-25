# 0020. Messages are a tree, and each parent remembers its current child

November 24, 2026 · decided in P4-02

## The question

Editing a message or retrying a reply must make a branch and never overwrite. The person steps between branches and expects each one to be as they left it. How are branches stored, and how does a chat know which one is current?

## The decision

There is no table of branches. Each message has a `parent_id`. An edit or a retry is a new message with the same parent as the one it replaces, so a branch point is a parent with more than one child.

Each message also has a `current_child`, and each chat a `current_root`. The current branch is the path that follows these from the first message down. A new message becomes its parent's current child. Selecting a message sets the pointers above it and leaves the ones below it alone.

A branch has no id of its own. It is named by any message on it.

The reasons:

- Edit and retry are the same act: one more child. Nothing is copied and no message is ever updated.
- Each branch point remembers its own choice. Stepping to a sibling and back shows the same messages as before, however deep the branch below went.
- The switcher on the screen is the list of a message's siblings, which is one lookup by parent.
- A message that arrives in a branch the person has left changes only its parent's pointer, so it does not pull the screen to that branch.

## What was not chosen

- **A table of branches, each a straight run of messages.** An edit near the top would copy or share everything above it, and "which messages are on this branch" becomes a join across branch rows. The tree says the same thing with one column.
- **One pointer on the chat to the current leaf.** Reading the path is easy: walk up from the leaf. But stepping to a sibling must then guess which leaf below it to land on, and stepping back does not return to where the person was unless every choice is stored somewhere anyway.

## What it costs

- The path is read with a recursive query, one step for each message on it. Timed once on the build machine, a path 600 messages deep read in about 3 milliseconds and one 3,000 deep in about 20. Selecting the deepest message took about the same.
- The pointers are a second thing to keep right. They are written only by `Append` and `Select`, each in one transaction.
- A chat can have more than one first message, because the first message can be edited. Code that wants "the first message" must ask for the path.
