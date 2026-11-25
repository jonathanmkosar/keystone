# Adding a model

A model is one entry in one person's registry. Adding an entry is all it takes: no code changes and nothing is rebuilt. What an entry holds is in [`docs/architecture/models.md`](../architecture/models.md).

An entry never holds a key. It names a secret in the person's vault, and the key is stored there first.

Every name, model and price below is invented. Use the provider's own model name and prices.

## The steps

1. Find the tenant id:

       go run ./tools/tenant list

2. If the endpoint asks for a key, copy the key and store it under a name of your choosing. It is read from standard input and never printed ([secrets.md](secrets.md)):

       Get-Clipboard | go run ./tools/tenant secret set <tenant-id> provider.one

3. Write the entry as a JSON file, such as `entry.json`. The examples below are a start.
4. Add it:

       go run ./tools/tenant model add <tenant-id> entry.json

   To pipe the entry in, give `-` for the file: `Get-Content entry.json | go run ./tools/tenant model add <tenant-id> -`.
5. Check it. The list names each entry's key and says when the vault does not hold it:

       go run ./tools/tenant model list <tenant-id>

The model is in the person's model list on their next turn. The server does not need a restart.

To change an entry, remove it and add it again:

    go run ./tools/tenant model remove <tenant-id> <name>

Removing a model leaves its key in the vault. `go run ./tools/tenant secret delete <tenant-id> <secret-name>` removes that.

## An entry for each adapter

`price` is US dollars for a million tokens, from the provider's price page. `context_window` and `output_limit` are the provider's numbers for the model. `capabilities` lists what the model can do, from `tools`, `vision`, `files`, `thinking` and `embedding`.

### `anthropic`: the Anthropic Messages API

The key is stored as `provider.anthropic`. The adapter calls `{endpoint}/v1/messages`.

    Get-Clipboard | go run ./tools/tenant secret set <tenant-id> provider.anthropic

```json
{
  "name": "example-large",
  "label": "Example Large",
  "adapter": "anthropic",
  "endpoint": "https://api.anthropic.com",
  "provider_model": "example-large-1",
  "key": "provider.anthropic",
  "context_window": 200000,
  "output_limit": 16000,
  "capabilities": ["tools", "vision", "files", "thinking"],
  "price": {"input": 3, "output": 15, "cache_read": 0.3, "cache_write": 3.75},
  "class": "hosted"
}
```

### `responses`: the OpenAI Responses API

The key is stored as `provider.openai`. The adapter calls `{endpoint}/responses`.

    Get-Clipboard | go run ./tools/tenant secret set <tenant-id> provider.openai

```json
{
  "name": "example-reasoner",
  "label": "Example Reasoner",
  "adapter": "responses",
  "endpoint": "https://api.openai.com/v1",
  "provider_model": "example-reasoner-1",
  "key": "provider.openai",
  "context_window": 128000,
  "output_limit": 16000,
  "capabilities": ["tools", "vision", "thinking"],
  "price": {"input": 1.25, "output": 10, "cache_read": 0.125},
  "class": "hosted"
}
```

### `chat_completions`: OpenRouter, LiteLLM, Ollama, the gateway

A hosted endpoint, with its key stored as `provider.openrouter`. The adapter calls `{endpoint}/chat/completions`.

    Get-Clipboard | go run ./tools/tenant secret set <tenant-id> provider.openrouter

```json
{
  "name": "example-open",
  "label": "Example Open",
  "adapter": "chat_completions",
  "endpoint": "https://openrouter.ai/api/v1",
  "provider_model": "example-lab/example-open-1",
  "key": "provider.openrouter",
  "context_window": 64000,
  "output_limit": 8000,
  "capabilities": ["tools"],
  "price": {"input": 0.5, "output": 1.5},
  "class": "hosted"
}
```

A model on this machine, such as one Ollama serves, needs no key and costs nothing:

```json
{
  "name": "house-small",
  "label": "House small",
  "adapter": "chat_completions",
  "endpoint": "http://127.0.0.1:11434/v1",
  "provider_model": "example-small:8b",
  "context_window": 8192,
  "output_limit": 2048,
  "capabilities": ["tools"],
  "price": {"input": 0, "output": 0},
  "class": "local"
}
```

An endpoint on the home network with a certificate of its own, such as the gateway, adds `"ca_cert"` with the path of the authority's PEM file ([adapter-chat-completions.md](../architecture/adapter-chat-completions.md)).

## The class

`class` decides what a model may be sent, and it is a security choice.

- `local`: on this machine or the home network. The registry refuses `local` for any other address.
- `hosted`: run by someone else. It never receives content from a sensitive space, and its cost counts against the monthly cap.
- `trusted`: not local, and you have chosen to trust it with content from a sensitive space. Its cost counts against the cap too.

## What is refused

The command prints why. The common ones:

- A key pasted into `key`. The field takes the secret's name, such as `provider.one`.
- `http` for an address that is not on this machine or the home network.
- A field the registry does not know, or two entries in one file. One entry for each `model add`.
- A name the person already has.

## The test tenant

The test tenant ignores its registry. It always answers from its recorded turns with the fake provider, whatever is added (decision [0144](../architecture/decisions/0144-the-test-tenants-model-is-in-no-registry.md)), and `model add` says so. To try a real model, add a tenant ([adding-a-tenant.md](adding-a-tenant.md)) and give that one the model.
