# 0111. For the Responses adapter a thinking budget becomes an effort, by two settings

November 25, 2026 · decided in P3-05

## The question

The seam's request says how many tokens a model may spend on thinking. The Responses API does not take a number. It takes an effort: low, medium or high. How does one become the other, and what does a budget of zero mean?

## The decision

**Two settings mark the steps.** A budget of `responses.effort_high_from` tokens or more asks for high. A budget of `responses.effort_medium_from` or more asks for medium. A smaller budget, down to one token, asks for low. The defaults are 16384 and 4096.

**A budget of zero sends no `reasoning` field.** The model is left at the provider's default.

**The field is sent only for an entry with the `thinking` capability.**

**A summary is always asked for with it**, as `summary: "auto"`, so there is something to show as thinking.

## Why

- The seam cannot carry an effort without every other adapter having to invent a budget for it. A budget is the more exact of the two, so it is the one the seam keeps, and the adapter that cannot use it exactly rounds it.
- The steps are a matter of taste and of price, and they will move as models change. That makes them settings.
- The efforts beyond these three differ from model to model. A request that names one a model does not have is refused, so the adapter keeps to the three that every reasoning model takes.
- Zero cannot mean "do not think", because there is no value that switches reasoning off on every model. Leaving the field out is the one thing that is always accepted.
- A model without reasoning is believed to refuse the field. The registry already says which models think.

## What it costs

- The budget is not kept. A request for 5000 tokens of thinking and one for 15000 ask for the same thing, and the model may spend more or less than either. The usage reports what was spent.
- A reasoning model still reasons when the budget is zero, and its reasoning counts against `max_output_tokens`. A small output limit on such a model can end with no text. The budget item (P3-07) and routing should leave room.
- An entry for a reasoning model that does not list `thinking` gets its reasoning neither asked for nor carried to the next call.
