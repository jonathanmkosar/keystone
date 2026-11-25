# Model registry

Folders: `internal/models` and `internal/adapters`

The registry is the list of models one tenant can use. Each tenant has its own, in its own database. An entry says where a model is, which adapter speaks to it, how much it can read and write, what it can do, what it costs, and whether it is local, hosted or trusted.

Adding a model is adding an entry. No code changes, and nothing is rebuilt.

Two packages share the work:

- `internal/models` holds the entries: what one is, what makes one valid, and where they are stored. It opens no connection and reads no setting.
- `internal/adapters` turns an entry into a provider that can be called, with the key from the tenant's vault.

## An entry

```json
{
  "name": "example-large",
  "label": "Example Large",
  "adapter": "anthropic",
  "endpoint": "https://api.provider-one.example",
  "provider_model": "example-large-1",
  "key": "provider.one",
  "context_window": 200000,
  "output_limit": 16000,
  "capabilities": ["tools", "vision", "files", "thinking"],
  "price": {"input": 3, "output": 15, "cache_read": 0.3, "cache_write": 3.75},
  "class": "hosted"
}
```

| Field | Meaning |
| --- | --- |
| `name` | The registry name, the one a seam `Request` carries in `model`. 1 to 63 lower-case letters, digits, dots, dashes and underscores. |
| `label` | What the model picker shows. One line, 80 characters at most. |
| `adapter` | `fake`, `anthropic`, `responses` or `chat_completions`. |
| `endpoint` | The base address the adapter calls. `http` or `https`, a host and an optional path. |
| `provider_model` | The provider's own name for the model, which the adapter sends. |
| `key` | The name of the secret in the tenant's vault that holds the provider key. Never the key. Left out when the endpoint asks for none. |
| `context_window` | The most tokens one call may hold, input and output together. |
| `output_limit` | The most tokens the model writes in one reply. Never more than the context window. |
| `capabilities` | Any of `tools`, `vision`, `files`, `thinking`, `embedding`. |
| `price` | US dollars for a million tokens: `input`, `output`, and `cache_read` and `cache_write` where the provider has them. A local model costs nothing. |
| `class` | `local`, `hosted` or `trusted`. See below. |
| `ca_cert` | Optional. One certificate authority to trust for this entry's `https` endpoint, as PEM text or the path of a PEM file. Added with the Chat Completions adapter, which reads it: [adapter-chat-completions.md](adapter-chat-completions.md). |

`fixtures/models/examples.json` has an entry for each adapter and each class.

## The class, and fixed rule 3

Rule 3 in the plan: content from a sensitive space reaches only models the registry marks local or trusted. The class is that mark.

| Class | Means | May read sensitive content |
| --- | --- | --- |
| `local` | The model runs on this machine or on the home network. | Yes |
| `trusted` | The model is somewhere else, and the admin has chosen to trust it. | Yes |
| `hosted` | Someone else runs the model. | No |

`Model.MayReadSensitive()` is the only place the class is turned into yes or no. Anything that is not `local` or `trusted` is no, and that includes an entry with no class and an entry that could not be read.

Because the rule rests on it, `internal/models` is a guard for `rules.sensitive_content` in the `fixed rules` check. It imports neither the settings registry nor the tenant settings, itself or through another package, so no setting can change what a class means. The one limit it has comes in as an argument. Decision [0051](decisions/0051-the-registry-guards-rule-three.md) says more.

The registry gives the answer. It does not yet stop a call. Routing (P3-08) and recall (P6-05) ask `MayReadSensitive` before they send anything.

### What `local` is checked against

An entry marked `local` is refused unless its endpoint is on this machine or the home network. This is read from the address alone. Nothing is looked up.

- `localhost`, or a name with no dot in it;
- a name ending in `.local`, `.lan`, `.home.arpa`, `.internal` or `.localhost`;
- a loopback, private or link-local address, or one in `100.64.0.0/10`.

A model on a machine you trust that has some other name is marked `trusted`. The list is fixed in code and is not a setting, for the reason above.

## What is refused

`Check` refuses an entry and gives the reason, which names the field and what would be accepted. `Decode` reads an entry from JSON, refuses a field it does not know, and then runs `Check`.

- A name that is not a registry name, or a missing label.
- An adapter that is not one of the four, a class that is not one of the three, a capability that is not one of the five or is listed twice.
- For the `fake` adapter, which runs inside Keystone: an endpoint, a key, or a class other than `local`.
- For the other adapters: no endpoint, or no `provider_model`.
- An endpoint that is not `http` or `https`, has no host, or holds a user name, a password, a query or a fragment. Those are the places a key gets pasted.
- An endpoint that uses `http` and is not on this machine or the home network.
- A class of `local` with an endpoint that is somewhere else.
- A `key` that is not shaped like a secret's name.
- A context window or an output limit of zero or less, or an output limit above the context window.
- A price below zero.
- A `ca_cert` on the `fake` adapter or on an `http` endpoint, one that holds a private key, one longer than 32,000 characters, or a path that is not one line. The value is never repeated in the reason.

A reason never repeats the endpoint or the `key` field, in case a key was pasted into one of them.

`fixtures/models/refused.json` has one entry for each reason, and the tests check each is refused for that reason.

## Where the entries are kept

The `models` table in the tenant's database has one row for each model: its name, the entry as JSON, and when it last changed. The migration is `20261124T230100_models.sql`. Decision [0050](decisions/0050-a-model-is-a-row-of-checked-json.md) says why the entry is one JSON value and not a column for each field.

An entry is checked before it is stored and again every time it is read. A row written with SQL, or one stored before a rule changed, is never used because it is in the table: `Get` refuses it, and `List` shows it with the reason so it can be replaced or removed.

| Call | What it does |
| --- | --- |
| `Add(ctx, store, limits, model)` | Stores a new model. Refuses a name the tenant has (`ErrExists`) and one more than the limit (`ErrFull`). |
| `Replace(ctx, store, model)` | Stores a changed entry over the one with the same name. |
| `Remove(ctx, store, name)` | Takes a model out. |
| `Get(ctx, store, name)` | Returns one model, or `ErrUnknown`. |
| `List(ctx, store)` | Every row in order of name, each with when it changed and, for a refused one, the problem. |
| `Usable(ctx, store)` | The models that pass `Check` today. The model picker and the routing rules read this. |

Each takes the tenant's store handle, so it reaches that tenant's registry and no other. A refused entry is an error that wraps `ErrInvalid`.

`Price.CostMicros(input, output, cacheRead, cacheWrite)` works out what a call cost from the seam's four token counts, in millionths of a dollar, which is the unit the audit log keeps.

## From an entry to a provider

`adapters.Open(ctx, store, set, name)` returns the tenant's model and a `seam.Provider` that calls it.

1. It reads the entry with `models.Get`.
2. It finds the builder for the entry's adapter in `set`. An adapter with no builder is `ErrNotBuilt`.
3. If the entry names a `key`, it reads that secret from the tenant's vault, `Store.Secrets()`. A secret that is not there is `ErrNoKey`, and the error gives the secret's name. A key comes from the vault and from nowhere else: not from a setting and not from the environment.
4. It calls the builder with the entry and the key.

A `Set` maps an adapter's name to its `Builder`, a function from an entry and a key to a provider. Whoever starts the server makes the set. `adapters.Fake(scripts, options)` returns the set that holds the fake. Each of the three real adapters (P3-04 to P3-06) adds a builder to a set and its folder to the `offline` check's list. Until then an entry that names one can be stored and listed, and opening it says the adapter is not in this build. Decision [0052](decisions/0052-builders-are-passed-in.md) says why the set is passed in.

A builder must never put the key in an error. If one does, `Open` takes it out before the error goes on.

## Settings

In `settings/models.json`.

| Key | Default | Meaning |
| --- | --- | --- |
| `models.max_per_tenant` | `50` | The most models one person's registry may hold. |

`adapters.Limits(registry)` reads it and returns the `models.Limits` that `Add` takes.

## In code

```go
m, err := models.Decode(raw)                  // or build a models.Model
lim, err := adapters.Limits(registry)
err = models.Add(ctx, store, lim, m)

set, err := adapters.Fake(scripts, fake.Options{})
m, p, err := adapters.Open(ctx, store, set, "scripted")
reply, err := seam.Call(ctx, p, seam.Request{Model: m.Name, Messages: messages})
```

## Checked by

`go test ./internal/models ./internal/adapters`.

- Every example entry is accepted and comes back the same from JSON. Every refused entry is refused for its reason.
- A refusal does not repeat a value pasted into `key` or the endpoint.
- Only `local` and `trusted` may read sensitive content. An unknown class, an empty one and a refused entry may not.
- A model added to one tenant is there after the database is reopened, and is not in another tenant's registry.
- A name used twice, one more than the limit, and a change to a model that is not there are each refused.
- Rows written with SQL are refused when read: a hosted address marked `local`, an entry under another name, text that is not JSON, and an unknown field.
- A model added to the registry is opened and answers a call through the fake. An adapter with no builder is refused by name.
- The key reaches the builder from the vault and not from the environment. A missing secret is refused by name. A builder's error with the key in it comes out without it.

In `verify`: the `fixed rules` check lists `internal/models` as a guard for rule 3, and the `no keys in output` check has a surface for the registry ([tools.md](tools.md)).

## Not here yet

- Routes and a screen to add and change models. The functions are ready for the API (P2-05) and the settings screens (P11-01). There is no command for it either.
- Who may change a tenant's registry. The functions take no role, so the route decides. See the owner's question in the journal, models 003.
- The three real adapters, P3-04 to P3-06. Their own fields, such as a gateway's certificate, are added to the entry when they are built.
- Routing, the fallback chain and the call that is refused because of its class, P3-08. The budget, P3-07.
- An install-wide list of models that every tenant starts with. The test tenant (P3-09) adds its own.
