# 0681. The moved Tavily provider keeps provider.tavily as its key, and is made once, at start or at the first search

November 27, 2026 · decided in P7-06

## The question

The spec says the existing `provider.tavily` "becomes the person's first own provider, named `tavily`, when the new version first starts", and that own providers keep their keys as `search.<name>`. Is the key copied to `search.tavily`? And what of a key stored as `provider.tavily` after the first start, as the runbook and the Models and keys screen still say to?

## The decision

- **The entry keeps `provider.tavily` as its key's name.** Nothing is copied, and the person's key stays where it was. Every other own provider keeps its key as `search.<name>`; `Provider.Check` allows `provider.tavily` for this one Tavily entry only.
- **It is made once for each person, whenever Keystone first sees a `provider.tavily` with no move recorded**: in the sweep's first pass, which starts with the server, or at the person's next search, whichever comes first. The move is recorded in `search_meta`, so a person who removes the entry does not get it back.
- It goes first in the person's order, before any provider they added before the key was stored.
- A person with no `provider.tavily` has nothing to move, and is looked at again at their next search, so a key stored later still works.

## Why

- **The owner's Tavily keeps working with no step from him**, and so does every way of storing that key that exists today: `tools/tenant secret set`, the Models and keys screen, a restore from a backup. A copy would go stale the first time he changed the key one of those ways.
- **At start and at a search** covers both an install that is upgraded (the sweep moves every person before any search) and a key stored after the start (the search moves it).

## What it costs

- One own provider's key has a different name from the rest. The screens (P7-08) show the key's name, so this is visible, not hidden.
- Each search reads one row of `search_meta`, and for a person with no move recorded, their vault.
