# 0161. The cap stops a call that could pass it, counted at the most the call could cost

November 25, 2026 · decided in P3-08

## The question

A call's cost is known when it ends. The cap has to be asked before it starts. Is a call stopped when the month is already at the cap, or when this call could take it past?

## The decision

**A call is stopped when the month's spend and the most the call could cost are together more than the cap.** The most it could cost is its estimated input, with the budget's margin, at the dearer of the input price and the cache-write price, plus the whole room kept for the reply at the output price.

The warning uses the same sum against `usage.cap_warn_percent` of the cap.

A cap that cannot be read, or a month that cannot be summed, stops the call.

## Why

- The plan's wording for the standing check is "a hosted call that would pass the cap is stopped". Stopping only once the month is over the cap lets every month end over it, by up to one whole call.
- One call to a large model with a long reply can cost dollars. With a cap someone has set low on purpose, for a child's account or a trial, "at most one call over" is not a small amount.
- The estimate leans high for the same reason the budget's does (decision [0131](0131-the-estimate-counts-characters.md)): the cost of guessing high is a call stopped a little early, and the cost of guessing low is money.
- Failing closed on a cap that does not read follows the rest of Keystone: a setting that is broken must not be the way past a limit.

## What it costs

- The last part of the cap cannot be spent on a call with a large output limit. With a 16,000-token limit at $15 a million, calls stop about 24 cents short of the cap unless the turn asks for a shorter reply. `chats.turn_max_output_tokens` lowers it.
- Two calls sent at the same moment each read the month before the other is recorded, so together they can pass the cap by one call's cost. A lock across a tenant's calls would close it, and is not worth holding a chat for while there is one person to a tenant.
- A call that breaks off is recorded at no cost, though a provider may bill for what it wrote. The provider's own bill is the truth, and this cap is a guard beside it.
