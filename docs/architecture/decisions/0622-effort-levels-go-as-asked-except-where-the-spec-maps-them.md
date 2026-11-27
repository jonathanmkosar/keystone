# 0622. An effort level goes to the provider as asked, except where the spec maps it

November 27, 2026 · decided in P14-01

## The question

The seam has five levels, `low` to `max`. The Responses row of the spec maps `xhigh` and `max` to `high`. The Anthropic and Chat Completions rows say nothing. Should those adapters cut a level to what a model takes, or refuse a level, or send it as it is?

## The decision

- **The seam refuses a level it does not know.** `ValidateRequest` takes the five levels, spelt in lower case, or none.
- **Responses** sends `xhigh` and `max` as `high`, as the spec says.
- **Anthropic** sends the level in `output_config.effort` as asked. As a thinking budget, `xhigh` and `max` use the high one (decision 0620).
- **Chat Completions** sends the level in `reasoning_effort` as asked, when its setting is on.

## Why

- **The adapters do not know each model's levels.** The levels differ by model and change with each release; a table of them in code would be a constant someone has to change. The provider's refusal says plainly what it does not take.
- **The router caps it.** `router.max_effort` is `high` as shipped, which every model that takes effort accepts, so `xhigh` and `max` only reach a provider when the owner raises the cap.
- **Chat Completions endpoints differ the most**, which is why the field is behind `chat_completions.send_reasoning_effort`, off as shipped.

## What it costs

- An owner who raises `router.max_effort` to `max` with a model that does not take it gets a refused call, `invalid`, which the fallback chain does not try elsewhere. A per-entry highest level would fix that; it is a question for the owner in the journal.
