# 0292. A task's result is a note, written by a turn that calls no model, in the chat's queue

November 25, 2026 · decided in P8-01

## The question

When a task ends, its result must appear in the chat it was started from, so that a person who reopens the chat sees it and the chat's later turns can use it. How is it written there, when the person may be in the middle of a turn in that chat?

## The decision

**The result is an assistant message**, with the stop reason `task`, saying the task is done and giving its result, or that it was stopped, or why it did not finish. It goes under the last message of the chat's current branch.

**It is written by a turn of the chat that calls no model** (`turn.Runner.Note`): a row in `turns` with its events, so the message has a turn, a stop reason and a stream like any other, and the chat's queue is held while it is written. A turn that is running is waited for, and a turn sent meanwhile waits behind the note.

## Why

- Appending to the chat record from outside the queue could land between a turn's message and its reply, and make two replies to one message. Holding the queue, which is how the turn runner keeps a chat's turns in order, rules that out.
- As a turn's message it carries a stop reason the screen can draw as a task's result, and its turn ties it to the chat's turns in the audit and the record, without a new kind of message.
- An assistant message is what later turns read as something Keystone said, so the chat's model knows the task's result when the person asks about it. A message from the person's side would put words in their mouth.

## What it costs

- A note waits for the chat's queue: a turn waiting for an approval in that chat holds the note back until it is answered. The task's own row has its result meanwhile.
- The note's text is in Keystone's words, not the model's, around the model's result.
