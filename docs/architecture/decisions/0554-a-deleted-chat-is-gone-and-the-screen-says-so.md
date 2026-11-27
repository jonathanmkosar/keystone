# 0554. A deleted chat is gone, and the screen says so

November 26, 2026 · decided in P4-06

## The question

Deleting many chats at once makes a slip cost more than deleting one. Should a batch delete put chats in a trash they can be brought back from, or offer an undo for a few seconds?

## The decision

- **No trash and no undo are built.** A chat deleted from the table is deleted as it is from its own menu: `chat.Delete`, its files and artifacts removed from disk before the transaction commits.
- **The confirmation says so in plain words**, as its last and strongest line: there is no undo and no trash, and a deleted chat cannot be brought back. It also names how many chats, what goes with them, and what stays.
- **The slip is made harder instead**: nothing is deleted without the confirmation; a new search or filter starts with nothing selected, so a selection is always of what is on the screen; "select all that match" states its count; the focus of the confirmation rests on Keep; and a chat that is at work is left (decision [0552](0552-a-batch-delete-skips-a-chat-that-is-at-work.md)).
- The standing check `manage chats` fails if the confirmation stops saying there is no undo.

## Why

- A trash is a second state for every chat. Every list, the search, the past-chat tools, share links, the memory pass, schedules and the incognito sweep would each have to know that a chat is there and not there, and fixed rule 3's holds and an incognito chat's promise ("deleted when left") would need an answer for a chat in the trash. That is an item of its own, not a line in this one.
- A short undo that only delays the delete is honest only if the page stays open: a phone that sleeps or a tab that closes would either delete without the person seeing the offer end, or not delete what they asked to. An undo that restores would need the files and artifacts kept somewhere, which is the trash again.
- Deleting really removes: an uploaded file and an artifact leave the disk. A person who deletes a chat because of what is in it should not find it kept for thirty days.
- Keystone has a backup (`tools/tenant backup`, [backup.md](../backup.md)). A chat deleted by mistake can be had back from the last one, by the admin, which is the right weight for a rare slip.

## What it costs

- A wrong tap on Delete, confirmed, loses those chats unless there is a backup.
- If the owner wants a trash, it is a new item: a `deleted_at` on the chat and every reader taught to pass over it.
