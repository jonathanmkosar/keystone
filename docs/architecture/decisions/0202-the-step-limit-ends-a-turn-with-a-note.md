# 0202. At the step limit a turn ends as done with a note, and the last calls are answered without running

November 25, 2026 · decided in P7-01

## The question

`chats.turn_max_steps` is the most model calls one turn makes. Until now, one more call ended the turn as failed with the code `step_limit` (decision [0143](0143-a-tool-call-is-answered-with-an-error-for-now.md)), and the last reply's tool calls were left with no result. The item asks for the turn to end cleanly at the limit, with a note the person can read. What exactly happens to the last calls, the turn, and the stream?

## The decision

**When the last model call a turn may make still asks for tools**, each of its calls is declined: answered with an error result that says the turn reached the most steps it may take, recorded in the audit log as `refused`, and stored as the usual message of results. No tool runs.

**The turn then writes a note** as an assistant message: the text of `agents.step_limit_note`, with the stop reason `step_limit`. It is streamed as a `message` event and becomes the turn's reply message.

**The turn ends as `done`**, with the stop reason `step_limit` on the turn. A chat with no title gets one. No `error` event is sent, and the code `step_limit` is no longer used by new turns.

**The stream's shapes do not change.** One value is added to what `stop_reason` may hold on a message and on a turn.

## Why

- Answering the calls keeps the record one a provider takes. The next message in the chat works with no repair, and `buildMessages` does not have to drop the calls.
- Not running them keeps the limit a limit: the tools of the step past it would be work the model never sees the result of.
- A note in the record is what the person reads when they come back to the chat, and what the model reads in the next turn, so "carry on" works. An error event would be gone after a reload.
- The turn did what it was allowed to do. Calling it failed would put a red error and a retry on the screen for a limit the install set on purpose.
- A stop reason, not a new event, is the smallest change to the contract, and the screen already draws a note for a message's stop reason.

## What it costs

- The model is not called after the note, so the reply's last words are Keystone's, not the model's summary of how far it got. A later item could give the model one more call with no tools offered, at the cost of a step over the limit.
- A client built against the contract that looked for the error code `step_limit` will not see it. The only client is this interface, which changes with it.
- The note is one install setting, in one language, for everyone.
