# 0682. A person's own providers always come first, and the strategy orders within each group

November 27, 2026 · decided in P7-06

## The question

The candidates are "the person's own ready providers in their order, then the install's ready providers in the admin's order", and `web.strategy` "decides the order they are tried in". Read alone, `free_first` would put an install provider with free quota ahead of a person's own paid one, and `weighted` would mix the two. The proof says a person's own provider is tried before the install's, and the brief settled "a person's own providers first, then the install's".

## The decision

The strategy orders the person's own providers among themselves, and then the install's among themselves; every own candidate is tried before any install candidate. A soft test's search (decision 0686) is the one exception: it goes first, alone.

## Why

- **It is what the owner settled**, and what the proof asks.
- **A person who adds a provider means it.** They brought a key so their searches go there; the install's free quota is a fallback, not a reason to pass their own over.

## What it costs

- With `free_first`, an install provider's free searches are used only once a person's own providers are out, or when a person has none.
