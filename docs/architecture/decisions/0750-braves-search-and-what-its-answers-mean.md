# 0750. Brave's search, as its documentation reads, and what its answers mean

November 28, 2026 · decided in P7-07

## The question

P7-07 adds Brave's Web Search API as a kind of search provider. What does Keystone send, what does it read back, and which of Brave's answers is which state?

## What was read

On November 28, 2026:

- The Web Search API's getting-started page, <https://api-dashboard.search.brave.com/app/documentation/web-search/get-started>: the address `https://api.search.brave.com/res/v1/web/search`, the key in the `X-Subscription-Token` header, `q` for the words and `count` for the results ("max 20, default 20", and fewer may come back), `offset` at most 9; the results in `web.results`, each with `title`, `url` and `description`. The examples are `curl` with no method, so GET.
- The query parameters page, <https://api-dashboard.search.brave.com/app/documentation/web-search/query>: the same `q`, `count`, `offset`, and `safesearch` (default `moderate`). It does not describe `text_decorations` or `result_filter`, so neither is sent.
- The rate-limiting guide, <https://api-dashboard.search.brave.com/documentation/guides/rate-limiting>: four headers with one value for each window, `X-RateLimit-Limit` (`1, 15000`), `X-RateLimit-Policy` (`1;w=1, 15000;w=2592000`, window sizes in seconds), `X-RateLimit-Remaining` (`1, 1000`) and `X-RateLimit-Reset` (`1, 1419704`, seconds until each resets); a limit reached answers 429; only successful answers count against the quota.
- No page of Brave's own documents its error codes. A post on Brave's community forum of June 17, 2025, <https://community.brave.app/t/api-key-not-working-for-brave-search-api-endpoints/626567>, shows a key Brave does not know answered with status 422 and `{"error":{"code":"SUBSCRIPTION_TOKEN_INVALID","detail":"The provided subscription token is invalid.","meta":{"component":"authentication"},"status":422},"type":"ErrorResponse"}`. Brave's help pages say a prepaid plan pauses when its balance reaches nothing.

## The decision

- **The request**: `GET <endpoint>?q=…&count=…`, `count` at most 20, `X-Subscription-Token: <key>`, `Accept: application/json`. The endpoint is `web.brave_endpoint` (decision 0755), or an entry's endpoint with `/res/v1/web/search`.
- **The results** are `web.results`, `title`, `url` and `description`, with any markup (Brave puts `<strong>` around the words searched for) taken out and entities decoded. A result with no title, no extract or an address that cannot be cited is dropped, as Tavily's are.
- **What is left** comes from every answer's headers. With `X-RateLimit-Policy`, the month is the longest window, when it is a day or more; without it, the last window, as Brave lists them (`HeaderQuota`, P7-06). A policy with no window of a day or more says nothing about a quota, so nothing is known.
- **The answers**:

  | Answer | Means | State |
  | --- | --- | --- |
  | 200 | the results, and what is left | ready |
  | 401, 403 | the key is refused | refused |
  | 422 (or 400) whose code holds `TOKEN`, or whose component is `authentication` | the key is refused | refused |
  | 422 or 400 otherwise | the request is malformed | the search stops |
  | 402 | the plan's credits are spent | out |
  | 429 with nothing left in the month's window, or a code that names a quota | the month's quota is spent | out until the month's reset |
  | 429 otherwise | a rate limit, for the reset of the spent window, else `Retry-After`, else `web.cooling_for` | cooling |
  | anything else, a timeout, no connection, an answer too large or not JSON | Brave is unavailable | failing after `web.fail_after` |

## Why

- **A 422 is not always a bad request at Brave.** Treating Brave's invalid-token answer as malformed would stop every search at a provider whose key is wrong, which is the one case the fallback exists to move past.
- **The month comes from the policy when it is there**, because a plan with only a per-second window would otherwise read its one remaining request as the month's quota and go out at once.
- **A spent month is a quota, not a long rate limit**: it says "out of quota until December 1" on the screens, and comes back at the reset Brave gave.
- **No other parameter is sent** that the pages read do not describe.

## What it costs

- Brave's error codes for a spent quota and a rate limit are not documented where they could be read; the headers decide, and a code with `QUOTA` in it is read as a quota in case Brave sends one. A 402 is read as spent credits from Brave's help pages, not from an error reference.
- Nothing has called the real Brave. The live check with the owner's key is P7-07's owner gate.
