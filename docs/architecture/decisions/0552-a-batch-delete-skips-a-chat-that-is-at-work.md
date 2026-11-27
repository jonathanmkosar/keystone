# 0552. A batch delete skips a chat that is at work

November 26, 2026 · decided in P4-06

## The question

`DELETE /chats/{id}` stops a turn that is running in the chat and then deletes it. `POST /chats/delete` deletes many at once, often chosen with "select all". What does it do with a chat in which a reply is still being written, or in which a background task or a sub-agent is still working?

## The decision

- **It leaves the chat and says so.** The reply's `skipped` names it with the reason `turn_running` or `task_running` and a sentence for the person. Nothing is stopped.
- **At work means**: a turn of the chat is queued or running (one waiting for an approval included), or a background task or a sub-agent whose status is `queued` or `running` works in the chat or was started from it.
- **The look and the delete are one step.** `removeIdle` takes the chat's queue in the runner, the same mark the goroutine that works a chat's turns holds. If that goroutine has it, a turn is running. Once the delete has it, no turn of the chat can start until it lets go.
- **The rest of the batch goes on.** One chat left does not fail the call: it answers 200 with what was deleted and what was not.
- An id that is not one of the person's chats is skipped the same way, as `not_found`.

## Why

- A chat a person deletes from its own menu is one they are looking at: stopping its reply is what they mean. A chat taken by "select all 137" may be answering in another tab, or be the chat a scheduled run is writing in this minute. Stopping that because of a checkbox the person did not look at is a surprise, and a deleted chat cannot be brought back.
- A background task whose work chat goes ends as failed with `chat_deleted`, and the chat it would have written its result in gets a failure instead. Leaving both until the task ends costs the person one more click later.
- Without holding the queue, a message sent between the look and the delete would start a turn in a chat that is then deleted under it. With it, the turn either goes with the chat, or is refused because the chat is gone.
- Failing the whole call for one busy chat would make "select all" useless on the day a schedule is running.

## What it costs

- The person has to delete a busy chat a second time, after it ends or after stopping it. The note names it, and it stays selected.
- The two delete routes differ: one stops, one skips. The route's summary and the component doc say so.
- A task or a sub-agent that starts in the moment after its chat was looked at is not seen. The task then ends as `chat_deleted`, as it does today when its chat is deleted from the menu.
