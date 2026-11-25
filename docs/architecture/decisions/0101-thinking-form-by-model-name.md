# 0101. The Anthropic adapter asks for adaptive thinking, and for a token budget only where a setting names the model

November 24, 2026 · decided in P3-04

## The question

The seam has one dial for thinking: `thinking_tokens`, the most the model may spend. The Messages API has two forms, and a model takes one or the other.

- Older models take `{"type": "enabled", "budget_tokens": n}`.
- Current models take `{"type": "adaptive"}`, where the model decides how much to think. They refuse a budget with a 400.

The registry entry says a model can think. It does not say which form it takes. How does the adapter choose?

## The decision

**Adaptive is the default.** A request with `thinking_tokens` above zero, for an entry with the `thinking` capability, is sent `{"type": "adaptive", "display": "summarized"}`. The number is not sent.

**A setting lists the models that take a budget.** `anthropic.budget_thinking_models` is a list of provider model names, where a name ending in `*` matches by its start. A model on it is sent the budget form, with the request's number kept under `max_tokens`. Its default names the older model families.

**Zero means nothing is sent about thinking.** Some current models think whatever they are sent, and refuse being told not to. Their thinking is passed on.

**Redacted thinking is kept in the signature.** The API sometimes sends a `redacted_thinking` block, which has data and neither text nor a signature. The seam has one thinking block. The adapter keeps the data in the signature behind the mark `redacted:` and sends it back as `redacted_thinking`. A real signature is base64 and never holds a colon.

## Why

- The models the owner will add first are current ones. The default should be right for them with nothing to set.
- Which form a model takes is a fact about the model that changes as models are added. That is a value someone might change, so it is a setting and not a list in code.
- The better home for it is the registry entry, one field per model. `internal/models` guards fixed rule 3 and was being read by three adapters built at once, so the entry was left alone for this item. See "What it costs".
- Asking for the summary costs nothing more: thinking is billed the same whether it is shown or not. Without it, the interface would show a pause and an empty thinking block.
- Keeping redacted data in the signature needs no change to the seam, and the seam already says a signature is opaque and goes back unchanged.

## What it costs

- A setting for the install is a blunt tool for a fact about one model. A gateway model whose name is not on the list and which takes only a budget gets a 400 until its name is added. The fix is a `thinking` field on the registry entry, with `adaptive` and `budget` as its values. That is a small change to `internal/models` for whoever next touches the entry.
- With adaptive thinking the person's number does nothing. The API's dial for depth is `effort`, which the seam does not have. If thinking depth matters in use, the seam needs a second dial, and that is a seam decision.
- The default list is from knowledge of the model names, and has not been checked against the live API.
- Another adapter that is handed a thinking block whose signature starts with `redacted:` will not know what it is. Thinking does not go from one provider to another in any case: a signature is one provider's own.
