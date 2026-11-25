# 0231. A summary extends the one before it, and stands for the start of one branch

November 25, 2026 · decided in P5-02

## The question

A chat keeps growing after its first summary. Is each summary made again from every turn it covers, or from the one before? And what does a summary stand for, in a chat whose messages are a tree of branches?

## The decision

**Each summary is made from the summary so far and the turns that follow it.** The model is asked for one summary that takes them in, and the answer is stored as a new row whose `extends` names the one before. The rows of a chat form chains, and a chain is started from nothing only once for a branch.

**A summary stands for the messages from the branch's first down to one message, `through_id`.** Messages are a tree (decision [0020](0020-messages-are-a-tree.md)), so the path to a message is one branch and no other, and the two ids are the whole range. Before a call, the turn takes the newest summary whose `through_id` is on the branch being sent, builds those messages as the call sends them, and checks that they are the call's first messages and end where a turn begins. Only then is it given to the context builder, with how many messages it covers.

**A branch that leaves before `through_id`** (an edit or a retry earlier in the chat) finds no summary that applies, and has its own chain once it outgrows its room. Going back to the first branch finds its chain again.

**A summary that no longer fits its share** of a call, because the chat moved to a model with a smaller window or the share was lowered, is extended too: the next request asks for at most as many words as this call's share holds, so the model writes it shorter.

**A job takes in as many turns as the summary model's window holds**, cutting a single turn that is too large on its own, and makes up to `chats.compact_calls_max` calls, each extending the last.

## Why

- Making a summary again from every turn it covers costs a call as large as the chat's whole history, each time. Extending costs a call the size of the new turns.
- An id range is what the plan asks to be stored, and in a tree two ids are enough. Checking the built messages, and not only the ids, means a summary is never sent for messages that were built differently, such as thinking blocks another model wrote.
- Rows are kept, not replaced, so a person who steps between branches does not pay for their summaries again.

## What it costs

- An extended summary is the model's rewrite of its own earlier text. Detail drifts out over many extensions. The prompt asks it to keep what the summary so far says unless a later turn changes it.
- Rows are never removed, except with their chat. A long chat keeps every summary it was ever given.
- A turn whose first message merges with the one before it, which only a turn that failed before its reply can make, never matches, and that branch goes without a summary until the next turn.
