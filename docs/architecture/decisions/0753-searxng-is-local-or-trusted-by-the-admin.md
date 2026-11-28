# 0753. A SearXNG is on the home network or trusted by the admin, and its answers are never a refused key

November 28, 2026 · decided in P7-07

## The question

P7-07 adds a self-hosted SearXNG as a kind of search provider, with no key and no quota. The spec says its endpoint must be local by the address rule of a local model (decision 0051), or marked trusted by the admin. Where is the mark kept, who may set it, what does Keystone send, and which of SearXNG's answers is which state?

## What was read

On November 28, 2026:

- The search API, <https://docs.searxng.org/dev/search_api.html>: `GET` or `POST` at `/` or `/search`, with `q` (required), `categories`, `language`, `pageno`, `time_range`, `format` (`json`, `csv` or `rss`), `safesearch`; a format must be switched on under `search:` in `settings.yml`, and "Requesting an unset format will return a 403 Forbidden error".
- The JSON answer, in SearXNG's code (`searx/webutils.py`, `get_json_response`, <https://github.com/searxng/searxng>): `query`, `results`, `answers`, `corrections`, `infoboxes`, `suggestions` and `unresponsive_engines` (pairs of an engine and its error); a result (`searx/result_types/_base.py`) has `url`, `title`, `content`, `engine`, `engines`, `score`, `category` and others.
- The limiter, <https://docs.searxng.org/admin/searx.limiter.html> and `searx/limiter.py`: a blocked address is answered with 429, with no `Retry-After`; its bot detection looks at headers such as `Accept-Language`.

## The decision

- **The entry needs an endpoint**, and it must be this machine or the home network by decision 0051's rule, read from the address alone: `localhost`, a name with no dot, a name ending in `.local`, `.lan`, `.home.arpa`, `.internal` or `.localhost`, or a loopback, private, link-local or `100.64.0.0/10` address. The rule is the model registry's own, exported as `models.OnHomeNetwork` in a new file of `internal/models`, so there is one list.
- **Or it is marked trusted**, a new field of the entry, `trusted`, kept as a column (the tenant migration `20261128T160000_search_provider_trusted.sql`, and the same column added to `data/admin/search.db` when it is opened). **Only an install entry may carry it**: a person's own SearXNG with the mark is refused, at Add and at Update. Only a SearXNG may carry it.
- **A SearXNG on this machine or the home network is called without the environment's proxy**, as a local model is. A trusted one elsewhere uses it, as any provider does.
- **The request**: `GET <endpoint>/search?q=…&format=json`, `Accept: application/json`, `Accept-Language: en`. No key.
- **The results** are `results`, `title`, `url` and `content`, cut to the count asked, and dropped as Tavily's are.
- **The answers**:

  | Answer | Means | State |
  | --- | --- | --- |
  | 200 with results | the results | ready |
  | 200 with no results and some engine in `unresponsive_engines` | its engines could not search | failing after `web.fail_after` |
  | 403 | JSON is not switched on in its `settings.yml`, and the words say so | failing after `web.fail_after` |
  | 429 | its limiter: a rate limit, for `web.cooling_for` | cooling |
  | 400 | the request is malformed | the search stops |
  | anything else, a timeout, no connection, an answer too large or not JSON | SearXNG is unavailable | failing after `web.fail_after` |

## Why

- **The admin's word is the admin's alone.** The spec gives the mark to the admin, and a person's own entry is set by the person, so a person's own SearXNG must look local. The owner's SearXNG is the house's, and belongs with the install's providers.
- **A column, not a setting.** A list of trusted hosts in a setting would trust an address for every person's entry, and the spec says an entry is marked.
- **A SearXNG is never `refused`.** It has no key, and `refused` lasts until the key changes, so a 403 read as a refused key would hold it out for good. The 403 it sends is a setting on the instance; once the owner fixes it, a soft test brings it back.
- **No results with failing engines is not an answer.** An instance whose engines are blocked is up, but cannot search; reading that as "nothing found" would end the search with nothing while another provider could answer.
- **`Accept-Language`** is sent because the limiter's bot detection, when it is on, refuses a request without one, and that refusal would read as a rate limit.

## What it costs

- The address rule reads an address; it does not prove where the instance runs (decision 0051). A home machine reached by a name outside the list, such as a mesh network's, must be an install entry the admin marks trusted.
- A person's own SearXNG, like an entry of any kind with an endpoint, makes Keystone send a request to the address the person gave, here one on the home network. The answer is read only as SearXNG's JSON and nothing of it reaches an error. Whether a person may add a SearXNG of their own at all is a question for the owner (journal agents 017).
- Nothing has called a real SearXNG. The live check with the owner's instance is P7-07's owner gate.
