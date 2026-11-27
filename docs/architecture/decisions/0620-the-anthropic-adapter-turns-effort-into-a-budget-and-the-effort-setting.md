# 0620. The Anthropic adapter turns effort into a thinking budget, and adds the API's effort for a model that takes it

November 27, 2026 · decided in P14-01

## The question

The spec's table says a request's effort becomes `output_config.effort` for models on `anthropic.effort_models`, a thinking budget from `anthropic.effort_budget_low`, `_medium` and `_high` for other models with `thinking`, and nothing otherwise. Three things it leaves open: whether a model that takes effort is also asked to think; what an `xhigh` or `max` effort is as a budget; and what "a thinking budget" means for a model that only thinks adaptively and takes no budget.

## The decision

- **The effort first becomes a budget**: low, medium or high from the settings, and the high one for `xhigh` and `max`. That budget then goes down the path `thinking_tokens` already takes (decision 0101): the budget form for a model on `anthropic.budget_thinking_models`, kept under `max_tokens` and left out under 1024; adaptive thinking for every other model; nothing for an entry without `thinking`. `thinking_tokens` is not read.
- **A model on `anthropic.effort_models` gets `output_config.effort` as well**, the level as asked. So a current model gets adaptive thinking with its effort, an older one that takes both (Opus 4.5) gets its budget and its effort, and a model that takes effort and cannot think gets the effort alone.
- **A model that thinks adaptively and is not on the effort list** gets adaptive thinking and no number, as it does for `thinking_tokens` today.
- **A token count** carries the thinking and no `output_config`.

## Why

- **One path for thinking.** The adapter already knows which models take a budget and which think adaptively. Sending a budget to a model that refuses one, because effort asked for "a budget", would break a call that works today with `thinking_tokens`.
- **Effort is a depth for thinking.** On the current models leaving `thinking` out means no thinking at all on some (Opus 4.7, 4.8), so effort alone would ask for depth in thinking that never happens. Asking for adaptive thinking with it gives what the router meant.
- **`xhigh` and `max` have no budget of their own** in the spec. The high budget is the most the settings name; inventing two more settings for levels the router does not ask for as shipped (`router.max_effort` is `high`) was not worth it.
- **Effort does not change the input**, and a count takes no output setting, as it takes no `max_tokens`.

## What it costs

- A model on the effort list that cannot think must not be given the `thinking` capability by mistake, or it is asked to think. That is the registry entry's job already.
- A level a model does not take (`xhigh` to Opus 4.6, `max` to Opus 4.5) is sent as asked and refused by the API as `invalid`. Recorded in the journal as a question for the owner.
