# Spec: web search providers and fallback

Written from `docs/briefs/web-search-providers.md` (clear). The items are P7-06 to P7-08 in `docs/plan/items/`. When this spec and the code disagree, this spec wins until a decision record changes it. The four fixed rules hold, and every value someone might change is a setting.

## Terms

- **Provider**: one search service with one key: Tavily, Brave, Exa, Serper or a SearXNG. Two keys for one service are two providers.
- **Install provider**: set by the admin, with a key in the install vault, for every person.
- **Own provider**: set by a person, with a key in their own vault, for them only.
- **State**: whether a provider is in the rotation, and why not when it is out.
- **Soft test**: a cheap check of whether a provider that is out has quota again.

## Providers

Each provider is an entry, kept like a model registry entry:

| Field | Meaning |
| --- | --- |
| `name` | A short name, unique among the person's own providers and the install's. |
| `kind` | `tavily`, `brave`, `exa`, `serper` or `searxng`. |
| `endpoint` | The base address. Each kind has a default; SearXNG has none. |
| `key` | The name of the secret in the vault. None for SearXNG. |
| `monthly_quota` | Searches a month, if known. Optional. |
| `free_quota` | Of those, how many cost nothing. Optional. |
| `resets` | The day of the month the quota starts again, or `rolling` for 30 days from first use. Optional. |
| `cost_per_search` | US dollars, if it costs anything. Optional. |
| `enabled` | Yes or no. |

- **Install providers** are kept in `data/admin/`, beside the admin accounts (`SPEC-router-and-admin.md`, Part 2). Their keys are kept in an install vault, `data/admin/secrets.bin`, sealed under the install master key like a person's vault (decision 0006). Only the admin sets them, and no route ever returns a key.
- **Own providers** are kept in the person's database, with their keys in the person's vault as `search.<name>`. The existing `provider.tavily` becomes the person's first own provider, named `tavily`, when the new version first starts.
- **SearXNG** runs on the owner's machine. Its endpoint must be local by the same address rule as a local model (decision 0051), or marked trusted by the admin. Its own searches still leave the house, so it is no exception to decision 0252: web tools stay off in a held chat.

## States

State is kept for each provider: the install's in `data/admin/`, shared by everyone who uses them; a person's own in their database.

| State | Entered when | Leaves when |
| --- | --- | --- |
| `ready` | At start, or after a soft test or a search succeeds. | One of the below. |
| `cooling` | A rate limit on a short window (429 with a wait of under `web.cooling_max`). | The wait the provider gave, or `web.cooling_for` if it gave none. |
| `out` | The quota is spent: the provider says so, or what is left is at most `web.quota_reserve`, or Keystone's own count reaches `monthly_quota`. | The known reset time passes, or a soft test succeeds. |
| `failing` | `web.fail_after` failures in a row that are the provider's own (unavailable, timeout, a 5xx answer). | A soft test succeeds, after `web.fail_cooldown`. |
| `refused` | The key is refused (401, 403). | The key is changed. No soft tests: a refused key does not come back by itself. The owner of the key is told. |

**What is left** comes from three places, in this order: what the provider reports (Brave's headers, Tavily's usage check); Keystone's own count of successful searches since the reset; nothing, in which case it is unknown.

## Choosing a provider

The candidates for a search are the person's own ready providers in their order, then the install's ready providers in the admin's order. `web.strategy` decides the order they are tried in:

- `free_first` (the default): candidates with free quota left go first, keeping their order, then the rest in order. Free quota is left when `free_quota` is set and what is used this month is under it.
- `order`: in order, nothing else.
- `weighted`: spread across the candidates by their share of quota left. A provider whose quota is unknown counts as `web.unknown_weight`. A provider with no quota at all, such as SearXNG, is used only when the others are out.

A search tries at most `web.max_attempts` providers. It moves on, exactly as the model chain does (decision 0160), for a quota, a rate limit, an unavailable or timed-out provider, or a refused key. It does not move on for a request the provider called malformed: that would hide a bug. With every candidate out, the result says so in one line per provider ("Brave: out of quota until December 1"), and never shows a key.

One search's results all come from one provider. Numbering through a turn (decision 0250) is unchanged.

## Soft tests

A provider that is `out` or `failing` is tested, never more than one test at a time per provider:

1. **At a known reset**, it goes back to `ready`, and the next real search confirms it.
2. **With a free check**, the check is run when the provider's next test is due: Tavily's usage check, or any other check that costs nothing. A check with quota left puts it back to `ready`.
3. **Without one**, the next real search, from a chat that is not held, may go to it first, alone. Success puts it back; failure puts it out again.

The first test is `web.test_first` after it went out, and each failed test doubles the wait, up to `web.test_max`. A sweep every `web.test_tick` runs the free checks that are due.

## Records and screens

- Each search is in the audit log with the provider that answered, and every provider tried before it with why it was passed over. Each provider has a count of searches and of cost for the month.
- **A person's Settings → Web search**: their own providers (add, order, key, quota, enable) with each one's state, what is left and searches this month; and the install's providers by name and state only.
- **The admin account → Web search**: the install's providers, keys written and never shown, the strategy, and each provider's state, what is left, searches and cost this month. Install-wide search use joins the admin's install monitoring.
- A step row says which provider answered when it was not the first tried.

## Settings

| Key | Type | Default | Scope |
| --- | --- | --- | --- |
| `web.strategy` | string | `free_first` | install |
| `web.max_attempts` | int | 3 | install |
| `web.quota_reserve` | int | 0 | install |
| `web.cooling_for` | duration | 60s | install |
| `web.cooling_max` | duration | 10m | install |
| `web.fail_after` | int | 3 | install |
| `web.fail_cooldown` | duration | 15m | install |
| `web.test_first` | duration | 30m | install |
| `web.test_max` | duration | 24h | install |
| `web.test_tick` | duration | 5m | install |
| `web.unknown_weight` | float | 0.5 | install |

The existing `agents.web_search_*` settings stay, for every provider.

## Proof

- On stand-ins on this machine only: a provider out of quota is passed over and the next answers; the order follows each strategy; free quota is used first; a rate limit cools a provider for the wait it gave; three failures put one in `failing`; a refused key is never tested again until it is changed.
- Soft tests: back at a known reset; back after a free check with quota left; one real search let through alone, the wait doubling after each failure up to the cap; never two tests at once.
- Brave's headers and Tavily's usage check are read into what is left.
- A person's own provider is tried before the install's. A person never sees an install key, and neither does the admin once it is written; `no keys in output` covers both vaults.
- Every search is in the audit log with the provider that answered and those passed over.
- Web tools stay off in a held chat, whichever provider.
- The live check: the owner's keys for each kind, and his SearXNG.
