# Brief: web search providers and fallback

Type: new
Requester: the owner
Owner: the owner
Status: clear
Last updated: November 27, 2026

## The ask

"We should add configuration for multiple web searcher and algo for fallback. For example when reach quota we move to the next or we can round robin it as well but smartly take out until ready to be back in. We can soft test quota when there's disabled until they have a quota again."

## The problem

Keystone searches the web through one provider, Tavily, with each person's own key. When that key's quota runs out or Tavily is down, every search fails until someone notices. Free monthly quotas on several providers go unused.

## Done looks like

- An admin sets install-wide search providers with their own keys, and a person can add providers with keys of their own, which are tried first.
- A search goes to the best provider that is ready, and moves on to the next when one is out of quota, rate limited, down or refusing its key.
- A provider that is out is taken out of the rotation, and comes back when a soft test shows it has quota again.
- Providers with free quota left are used before paid ones, in the order set.
- Each provider's state, what is left of its quota and its searches this month are on screen: a person's own in their Settings, the install's in the admin account.

## Out of scope

- Search inside a chat held for sensitive content. Web tools stay off there (decision 0252), whichever provider.
- Choosing a provider by what the query is about.

## Constraints

- Keys stay in a vault and reach only the provider's request (decision 0006). Install keys are never shown back, to anyone.
- Every search is recorded with the provider that answered and every provider that failed before it.
- Every value someone might change is a setting.

## What already exists

- `web.Searcher`, one method, with Tavily as its only implementation; the person's key as `provider.tavily`; Tavily's refusals (401, 403) and limits (429, 432, 433) told apart (`docs/architecture/web-search.md`).
- Tavily has a usage check, `GET /usage`, that costs no credits. Brave reports what is left in every reply, as `X-RateLimit-Remaining` and `X-RateLimit-Reset` with a monthly window. Other providers say only when a limit is reached.
- The model fallback chain's rule (decision 0160): move on for the endpoint's own failures, never for a bad request.

## Decisions

| Decision | Why | Hard to reverse? | Owner |
|---|---|---|---|
| Settled. Both: a person's own providers first, then the install's (the owner, November 27, 2026) | One set of keys serves everyone; a person can still bring their own | No | the owner |
| Settled. Brave, Exa, Serper and a self-hosted SearXNG beside Tavily (the owner, November 27, 2026) | Several free monthly quotas, and one provider on the home machine with no quota at all | No | the owner |
| Settled. Free quota first, then the order set; round robin weighted by quota left and strict order are settings (the owner, November 27, 2026) | Free searches are used before paid ones, and the order stays predictable | No | the owner |
| Settled. A soft test uses the provider's free check or quota headers where it has them, otherwise one real search after a wait that grows each time it fails (the owner, November 27, 2026) | It costs nothing where it can, and little where it cannot | No | the owner |

## How we will prove it works

See `docs/plan/SPEC-web-search.md`, "Proof".

## Open questions

| Question | Owner | Blocks start? |
|---|---|---|
| Keys for Brave, Exa and Serper, and the address of a SearXNG, for the live check | the owner | No. It blocks the live check only |

## Log

- November 27, 2026: started and cleared in one round. Read `docs/architecture/web-search.md` and the providers' quota documentation before asking.
