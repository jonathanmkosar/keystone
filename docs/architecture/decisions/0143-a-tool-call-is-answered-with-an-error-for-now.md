# 0143. Until the tool loop, a tool call is answered with an error and the model is called again

November 25, 2026 · decided in P4-03

Replaced by the tool loop (P7-01): a call is now run ([0200](0200-a-tool-is-data-and-a-function-in-a-registry.md)), and the step limit ends a turn as done with a note ([0202](0202-the-step-limit-ends-a-turn-with-a-note.md)). The third decision below, that a call left without a result is left out of later requests, still holds.

## The question

A turn offers the model no tools, because there is no tool loop yet (P7-01). A reply can still stop for a tool call: the test tenant's recorded ferry turn does, and a model may ask for a tool it was never offered. A tool call with no result in the next message is refused by every provider, the fake included. What does a turn do with one?

## The decision

**Each call is answered with a `tool_result` marked as an error**, whose text says no tool was run. The results are appended to the chat as a message, a `tool_result` event is streamed for each, and the model is called again.

**A turn makes at most `chats.turn_max_steps` model calls.** One more ends it with `step_limit`.

**A tool call left without a result is left out of later requests.** That happens when a turn is stopped or breaks off between the call and its result. `buildMessages` drops the call, and any result whose call was dropped.

## Why

- The record stays one that any provider accepts, whatever happened in an earlier turn. The alternative, ending the turn at the tool call, leaves a chat whose next message is refused.
- The chat screen gets real `tool_call` and `tool_result` events to draw, from the test tenant, before any tool exists.
- The shape is the tool loop's own: answer every call, append, call again, count the steps. P7-01 replaces the answer and adds the tools to the request.

## What it costs

- In the test tenant the ferry turn's second step still gives the timetable, as recorded, though the tool "failed". It is test data and the screen shows the result as an error.
- A real model that asks for a tool it was not offered costs one more call to be told no.
