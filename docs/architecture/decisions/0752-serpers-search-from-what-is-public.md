# 0752. Serper's search, from what Serper makes public, and what its answers mean

November 28, 2026 · decided in P7-07

## The question

P7-07 adds Serper as a kind of search provider. Serper's API reference is its playground, behind a sign-in. What does Keystone send, what does it read back, and which answer is which state?

## What was read

On November 28, 2026:

- Serper's home page, <https://serper.dev/>: an example answer with an `organic` list, each result with `title`, `link`, `snippet` and `position`, some with `sitelinks`, `date` or `attributes`; "2,500 free queries", with no card. The playground, <https://serper.dev/playground>, shows nothing without signing in.
- No public page of Serper's documents its address, its key header or its errors. The address `https://google.serper.dev/search`, a POST with a JSON body of `q` and `num`, and the key in `X-API-KEY` are what its clients and guides send, and what a thread on n8n's community forum of July 26, 2025 shows, <https://community.n8n.io/t/serper-dev-authentication-issues/156527>: the key in `X-API-KEY` on a POST, and a key not taken answered with 403 and `{"message":"Unauthorized. Sign up for a free account.","statusCode":403}`.
- Guides to Serper's pricing say one credit buys up to ten results and more than ten cost two, and that credits last six months from purchase. Reports of a spent balance quote a message about credits; none found gives its status.

## The decision

- **The request**: `POST <endpoint>`, `X-API-KEY: <key>`, `{"q": …, "num": 10}`, or `"num": 20` for more than ten results. The endpoint is `web.serper_endpoint` (decision 0755), or an entry's endpoint with `/search`.
- **The results** are `organic`, `title`, `link` and `snippet`, cut to the count asked, and dropped as Tavily's are.
- **The answers**:

  | Answer | Means | State |
  | --- | --- | --- |
  | 200 | the results | ready |
  | 401, 403 | the key is refused | refused |
  | 402 | the credits are spent | out |
  | 400 whose `message` names credits | the credits are spent | out |
  | 400 otherwise, 422 | the request is malformed | the search stops |
  | 429 | a rate limit, for `Retry-After`, else `web.cooling_for` | cooling |
  | anything else, a timeout, no connection, an answer too large or not JSON | Serper is unavailable | failing after `web.fail_after` |

## Why

- **`num` in tens** because asking for five costs what ten does, and twelve what twenty does.
- **A 400 that names credits is a quota**, because that is how reports quote a spent balance; anything else at 400 is taken as Serper's word that the request is wrong, so the search stops and the bug shows.
- **Serper's free queries are not monthly.** An entry's `monthly_quota` and `resets` are the owner's to set from what his account shows.

## What it costs

- This adapter rests on less than the others: Serper's own reference could not be read. The live check with the owner's key is where it is confirmed, and the status of a spent balance most of all. Until then P7-07 is provisional on it.
