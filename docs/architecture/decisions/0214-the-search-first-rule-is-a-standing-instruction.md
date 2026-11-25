# 0214. The search-first rule is a paragraph of the standing instructions, sent when a store is open

November 25, 2026 · decided in P6-05

## The question

The plan says a standing rule tells the model to search before it answers anything about the person's world. Where does that rule go in a call, and when is it sent?

## The decision

**The rule is the setting `knowledge.search_first_text`, added to the context builder's `rules` part** as a paragraph after `chats.context_system_text`. No new part or slot is made.

**It is sent when the person's knowledge store opened for the turn**, and left out with `none` or a store that did not open. Empty sends none.

## Why

- It is a rule, and the `rules` part is where the standing instructions are. The builder already never leaves that part out and puts it first.
- The text does not change from one call to the next while the person has a store, so the start of the call stays the same and a provider can cache it.
- Telling a model to look in a store that is not there, or that did not open, would send it to lookups that fail.
- The text is a setting, as `chats.context_system_text` is, so a person can word it for themselves.

## What it costs

- A store that is down for one turn changes the start of that call, and its cache is missed once.
- The rule speaks of lookup tools before any are offered. It says "when knowledge lookup tools are offered", so it reads true either way.
