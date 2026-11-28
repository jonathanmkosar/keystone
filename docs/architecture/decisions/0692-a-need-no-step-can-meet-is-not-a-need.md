# 0692. A need no step of the ladder can meet is not a need

November 27, 2026 · decided in P14-04

## The question

The router's second rule skips a step whose model lacks what the call needs: `vision` for an image, `files` for a file, `tools` when tools are offered. Every turn offers tools (the date, past chats), and many ladders will have no step that sees. Taken as written, a message with a picture on a ladder with no vision model, or any message on a ladder whose models take no tools, is refused with `no_model`. The same message sent to a picked model is answered: the image is left out with a notice (decision 0382), and a model without tools is offered none.

## The decision

The turn tells the router of a need only when at least one step of the ladder can meet it. An image, a file or offered tools that no step can take are left out of `Facts`; the call then goes to the step the router chooses and is readied for it as for a picked model: the image left out with its notice, the file as text or left out, no tools offered.

## Why

- Auto should answer every message a picked model answers. Refusing a message for a capability nobody on the ladder has is a refusal with no way out but leaving Auto.
- When some step can meet the need, the rule works as the spec says: the steps that cannot are skipped.

## What it costs

- On a ladder with no vision model a picture is never seen on Auto, and the person learns it from the notice under the reply, as they would on a picked model.
