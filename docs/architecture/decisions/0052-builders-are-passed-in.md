# 0052. An adapter's builder is passed in, and its key comes from the vault by name

November 24, 2026 · decided in P3-02

## The question

An entry names an adapter. Something has to turn the entry into a `seam.Provider`. One adapter exists, the fake, and three are to come, each of which opens connections. Where does that code go, how do the adapters reach it, and how does a key get to them?

## The decision

**A package of its own, `internal/adapters`.** `Open(ctx, store, set, name)` reads the entry from the registry, finds the builder, reads the key, and calls the builder.

**The builders are a value the caller passes: a `Set`, a map from adapter name to `Builder`.** A `Builder` is a function from an entry and a key to a provider. Nothing registers itself when the program starts. Whoever starts the server makes the set from the adapters it wants.

**The registry knows the four adapter names, and not their code.** An entry may name any of the four. An entry whose adapter has no builder in the set is stored and listed, and `Open` refuses it with `ErrNotBuilt`.

**The entry holds the name of a secret, never a key.** `Open` reads that name from the tenant's vault, `Store.Secrets()`, and from nowhere else. A missing secret is `ErrNoKey`, with the name. If a builder puts the key in its error, `Open` takes it out.

## Why

- The registry is a guard for rule 3 and should import as little as it can (decision [0051](0051-the-registry-guards-rule-three.md)). The fake reads settings, and the real adapters will import network packages. Neither belongs under the registry.
- A set that is passed in can be seen and changed by a test. A test gives `chat_completions` a stand-in builder and checks which key it was handed. With adapters that register themselves at start-up, the only way to try `Open` would be to have the real adapter.
- It also keeps the `offline` check honest. A real adapter's folder goes on that check's list when it is built. The package that picks among them does not import one until then.
- A key in an entry would be a key in the database, in every backup of it, and in whatever lists the registry. The vault is already encrypted for each tenant, and is the one call that returns a value. An environment variable would be one key for every tenant, readable by any process the account runs.
- Storing an entry before its adapter is built lets the registry, the screens and the test tenant go ahead of the adapters.

## What it costs

- The server has one more thing to put together at start-up, and a missing line there shows up as `ErrNotBuilt` when a model is first used, not when the program starts. The API shell can list the set's names against `models.Adapters()` when it starts.
- `Open` reads the vault on every call. Nothing is kept between calls, so a changed key takes effect at once and a removed one stops working at once. If that is ever too slow, the place to keep a provider is the caller, for one turn.
- A key's name can be checked only for its shape when an entry is stored. Whether the secret exists is found when the model is opened.
