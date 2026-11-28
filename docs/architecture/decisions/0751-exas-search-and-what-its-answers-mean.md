# 0751. Exa's search, as its documentation reads, and what its answers mean

November 28, 2026 · decided in P7-07

## The question

P7-07 adds Exa as a kind of search provider. What does Keystone send, what does it read back, and which of Exa's answers is which state?

## What was read

On November 28, 2026:

- The search reference, <https://docs.exa.ai/reference/search>: `POST https://api.exa.ai/search`, the key as `x-api-key` or `Authorization: Bearer`; the body's `query` (required), `numResults` (default 10, from 1 to 100), `type` (default `auto`), and `contents` with `text`, `highlights` (true, or an object with `maxCharacters` and others) and `summary`; the answer's `results`, each with `title`, `url`, `publishedDate`, `author`, `id` and the contents asked for (`text`, `highlights` as a list of strings, `summary`), and `costDollars`, an estimate.
- The error codes, <https://docs.exa.ai/reference/error-codes>: an error answer is `{"requestId", "error", "tag"}`, to be read by its status first. 400 is an invalid request (`INVALID_REQUEST_BODY`, `INVALID_REQUEST`, `NUM_RESULTS_EXCEEDED` and others); 401 a missing or invalid key (`INVALID_API_KEY`); 402 spent credits or budgets (`NO_MORE_CREDITS`, `API_KEY_BUDGET_EXCEEDED`, `TEAM_BUDGET_EXCEEDED`), or a request with no key that may pay per request (`X402_PAYMENT_REQUIRED`); 403 a key without access (`FEATURE_DISABLED`, `TEAM_BLOCKED`) or content a policy refused (`PROHIBITED_CONTENT`, `CONTENT_FILTER_ERROR`); 429 a rate or concurrency limit, with `Retry-After` when it is there; 500, 503 (`SERVICE_OVERLOADED`, not billed) and 504.
- The rate limits, <https://docs.exa.ai/reference/rate-limits>: 10 requests a second on the free plan, more on paid ones; 429 when it is passed; only `Retry-After` among the headers; $10 of free credits each calendar month on the free plan.

## The decision

- **The request**: `POST <endpoint>`, `x-api-key: <key>`, `{"query": …, "numResults": …, "contents": {"highlights": {"maxCharacters": agents.web_search_snippet_chars}}}`. Nothing else: `type` stays Exa's `auto`. The endpoint is `web.exa_endpoint` (decision 0755), or an entry's endpoint with `/search`.
- **The results** are `results`, `title` and `url`, with the highlights joined as the extract; with none, the summary, then the text. Results are dropped as Tavily's are.
- **The answers**:

  | Answer | Means | State |
  | --- | --- | --- |
  | 200 | the results | ready |
  | 401 | the key is refused | refused |
  | 402 with a tag `X402_…` or `MPP_…` | Exa did not take the key | refused |
  | 402 otherwise | the credits or a budget are spent | out |
  | 403 tagged `PROHIBITED_CONTENT` or `CONTENT_FILTER_ERROR` | the query itself was refused | the search stops, with Exa's words |
  | 403 otherwise | the key has no access | refused |
  | 429 | a rate limit, for `Retry-After`, else `web.cooling_for` | cooling |
  | 400 | the request is malformed | the search stops |
  | anything else, a timeout, no connection, an answer too large or not JSON | Exa is unavailable | failing after `web.fail_after` |

## Why

- **Highlights are the extract**, because they are what Exa makes for a result's words, already cut to the length Keystone keeps, and they keep the answer small.
- **The key in `x-api-key`**, one of the two ways documented, so it is never in an `Authorization` header a redirect could carry (decision 0754).
- **A query a content policy refused is not a refused key.** Putting the provider in `refused` would hold it out until the key changed, for one query. It is not a failure of Exa's either. It stops the search with Exa's words, as a malformed request does, so the person sees why; another provider is not asked to answer what Exa's policy would not.

## What it costs

- A query Exa's policy refuses gets no results from any provider in that search. If that proves wrong in use, it can move on instead.
- Exa's answer reports `costDollars`; Keystone counts the entry's own `cost_per_search` instead, as for every kind.
- Nothing has called the real Exa. The live check with the owner's key is P7-07's owner gate.
