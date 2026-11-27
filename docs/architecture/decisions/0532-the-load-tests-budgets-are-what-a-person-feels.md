# 0532. The load test's budgets are what a person feels, set before the first full run

November 26, 2026 · decided in P13-03

## The question

The load test (P13-03) fails when a time is over its budget. A budget chosen after the measurement, a little above it, proves nothing; so does one so wide that nothing fails. Where do the numbers come from, which figure is held to them, and what does the small profile in `verify` hold?

## The decision

**The numbers are from what a person feels, not from what the code does.** Three marks from long-standing work on response times: about a tenth of a second reads as at once; about a second keeps a person's train of thought; ten seconds loses their attention. Each measurement is placed against them by what the person is doing:

| Measurement | Budget | Reasoning |
| --- | --- | --- |
| The chat list | 100ms | It is drawn with the page. It should read as at once. |
| The home screen's notice (`operations/watch`) | 150ms | Read every time the home screen is shown; it must not be what the page waits on. |
| The chat search route | 200ms | A person types and watches the results. |
| Context packing, both kinds | 250ms | Keystone's own share of the wait before a model starts. A hosted model takes a second or so to begin; a quarter of a second more is not noticed, and a second more would be. |
| First streamed words, one person | 300ms | The same wait, measured to the first words on the screen, with a model that answers at once. |
| The usage report | 300ms | A page opened now and then: a click, then the page. |
| Opening the long chat | 400ms | The same, for the largest chat a person has. |
| `search_chats`, the Operations report, first words with every person sending at once | 500ms | A step inside a turn that already waits on a model; a page that is opened seldom and reads again each minute; the worst moment of a shared install. |
| Streaming a reply, one person; every person at once | 1s; 1.5s | For 2,400 characters. A fast model writes a few hundred characters a second, so Keystone passes a reply on several times faster than any model writes one. |

They are settings, `loadtest.budget_<name>` in `settings/loadtest.json`, and were written there before the first full run was made.

**The full run holds the 95th percentile**, over `loadtest.runs` runs (30): the wait a person meets about once in twenty times, which is the one they remember. The median is reported beside it.

**A time over its budget is a finding.** The cause is found and fixed where it is, with a test, and the budget stays. Two were: past-chat search (decision [0530](0530-a-search-cuts-snippets-only-for-what-it-gives-and-ranks-so-many-matches.md)) and the Operations report's reading of usage records.

**The small profile holds the median**, of `loadtest.small_runs` runs (5), to `loadtest.small_budget_percent` of each budget, 100 by default.

## Why

- A budget tied to the person stays right when the code changes. A budget tied to today's measurement is a regression alarm with no meaning: it fails when a machine is slower and passes when a person would be kept waiting.
- The 95th percentile and not the worst run: one run in thirty can meet a pause that is the machine's and not Keystone's.
- The small profile runs inside `verify`, beside other work on the machine, with five runs. Its 95th percentile is its worst run, which a busy machine decides; its median is steady. Its job is to prove the generator, the measurements and the comparison with the Monitor still work, and to fail when a time is wrong by a wide margin (a list with no page, a query that reads every row); the full run is where the budgets are proven. Its share of the budget is a setting, so it can be tightened on a machine that is known to be quiet.

## What it costs

- The small profile's times are a tenth of the budgets or less, so it does not catch a path that has become twice as slow. Only the full run does, and it is run by hand.
- The budgets are for the machine Keystone runs on. A much slower machine would need the full run again, and perhaps other numbers; they are settings for that reason.
