# 0685. What a provider's answer means for its state, and how its quota period is counted

November 27, 2026 · decided in P7-06

## The question

The spec names the states and what enters them. Several details are left to the build: which of Tavily's statuses is which state, a rate limit with a long wait, what period Keystone counts over when an entry gives no reset day, what is left after a provider's report, and when an out provider comes back by itself.

## The decision

- **Tavily's answers.** 401 and 403: refused. 429: a rate limit, with the wait from `Retry-After` (seconds or a date) when it is there. 432 (the plan's or key's limit) and 433 (pay-as-you-go): quota spent. 400 and 422: malformed, and the search does not move on. A timeout, a failure to connect, any 5xx or other status, an answer too large or not JSON: unavailable, which counts toward `web.fail_after`. A P7-03 search saw 429, 432 and 433 alike; now they differ.
- **A rate limit whose wait is `web.cooling_max` or more is a spent quota**, out until the wait has passed, not cooling for an hour.
- **The quota period.** An entry with a reset day counts from that day each month (a 31 in a shorter month is its last day); `rolling` counts 30 days from the first search; an entry with neither counts by the calendar month, in UTC, as the spend cap does. Keystone's count and free quota are over that period.
- **What is left** is what the provider last reported less the searches since; else the monthly quota less Keystone's count; else unknown. A provider that answers while it is out (a soft test) has its old report forgotten.
- **An out provider comes back by itself** at the reset the provider gave, else at the entry's reset day or rolling end, else, when Keystone's own count spent it, at the end of the calendar month. A provider that said it is out, with no reset known, waits for a soft test.
- Searches and cost are counted by provider and calendar month, for successful searches; a search costs the entry's cost per search once the period's free quota is used.

## Why

- **Each status means what Tavily documents**, and the spec's states need them apart: cooling for a short limit, out for spent credits.
- **A long wait is a quota by another name.** Cooling is for short windows; anything longer would hold the provider in a state the screens call "rate limited" for hours.
- **The calendar month in UTC** matches the spend cap (models 008), so "this month" means the same everywhere.

## What it costs

- Tavily's usage check reports credits, and an advanced search costs two: what is left is counted in basic searches.
- Nothing has called the real Tavily's usage check or seen its real 432 and 433 bodies: these follow its documentation, and the live check is the owner's (P7-07).
