# 0383. The adapters refuse an image to a model without vision

November 25, 2026 · decided in P9-02

## The question

The turn readies every call for the models its route can reach, so no image should reach an adapter for a model without vision. Should the adapters trust that, leave such an image out themselves, or refuse the request?

## The decision

Each of the three real adapters (Anthropic Messages, OpenAI Responses, Chat Completions) refuses, while it builds the request and before anything is sent, a request that holds an image block, in a message or in a tool's result, for a registry entry without `vision`. The error is the seam's `invalid`, which no fallback takes, and says the image was not sent. `seam.Images` counts the image blocks.

## Why

- It is the last lock on the rule, at the only place a request leaves Keystone, and it holds whatever calls the adapter: a turn, a summary, a background task, or code not written yet.
- Refusing is loud. Leaving the image out quietly would hide a bug upstream and send the model a request it was not built for; a refusal shows in the turn, the audit log and the usage record.
- `invalid` does not fall back, so a refused call does not go on to the next model with the same image.

## What it costs

- A registry entry for a model that can see but does not list `vision` cannot be sent an image until the entry is changed to list it.
- The fake adapter does not check: it plays scripts and is never called with a real person's images.
