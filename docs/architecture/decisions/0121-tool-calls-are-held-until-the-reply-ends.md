# 0121. The Chat Completions adapter holds tool calls until the reply ends

November 24, 2026 · decided in P3-06

## The question

The seam opens one block at a time, in order, and a tool call's block opens with its id and its name. Chat Completions sends tool calls as pieces tagged with an index. The format lets pieces of two calls arrive mixed, and the endpoints differ: one sends the id and name first and the arguments after, one sends each call whole at index 0, one sends no id. How do the pieces become seam blocks?

## The decision

**Text and reasoning are sent on as they arrive. Tool calls are collected, and sent when the reply ends**, each as a whole block: start, one piece of input, stop. They follow the text, in order of index.

While collecting:

- pieces with the same index are one call, unless a piece carries a new id for an index that already has one, which starts a new call;
- a call with no id is given one, `call_` and sixteen random hex digits;
- arguments are read as JSON text or as an object;
- at the end, each call must have a name and input that is a JSON object. Empty input is `{}`. A call the output limit cut off is dropped. Any other broken call fails the reply as `other`, retryable.

## Why

- A seam block cannot be reopened. If two calls arrive mixed, one of them has to wait in any case. Holding all of them is the same code for every endpoint.
- A block opens with an id and a name. An endpoint that sends the name late, or no id, gives nothing to open the block with until more has arrived.
- Nothing is lost that a person sees. Tool input is not shown as it streams. The text before it still arrives piece by piece.
- Checking the input when the reply ends means a call the model wrote badly never reaches the tool loop as a block that looks whole. The collector would refuse it anyway, with a message that is about the stream and not about the model.
- Ids must not repeat across a conversation, because the seam ties each result to its call by id. A random id cannot collide with an earlier turn's. An index would.

## What it costs

- The interface cannot show a tool call's input being written. It shows the call when the reply ends. For a large input, such as a long file written through a tool, that is a wait with nothing on the screen but the text before it. If that matters, the first call at least could be streamed, since its pieces arrive before any other's.
- A reply that is cancelled part of the way through keeps its text and loses its tool calls. The seam's own `Partial` drops an unfinished call for the same reason: half a call is no use.
- The Anthropic and Responses formats send tool calls one at a time, so their adapters may stream them. The same model reached through this adapter shows its calls later. The reply adds up to the same thing.
