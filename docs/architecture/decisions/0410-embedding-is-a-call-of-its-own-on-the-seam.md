# 0410. Embedding is a call of its own on the seam, declared by the embedding capability, and made by the fake and the Chat Completions adapter

November 25, 2026 · decided in P6-08

## The question

The plan makes embedding a model seam capability, local first. The seam has one call, `Provider.Stream`, built around messages and a streamed reply. The registry already has an `embedding` capability, and routing already says an embedding model takes embedding calls and nothing else. How does an embedding call go through the seam, which adapters make one, and what name does a vector carry?

## The decision

**A call of its own.** `seam.EmbedRequest` (the model and the texts) and `seam.EmbedResponse` (one vector for each text, and the usage), and an `Embedder` interface with one method, `Embed`, beside `Provider`. A whole answer comes back at once: nothing streams. A failure is a `*seam.Error` with the seam's kinds, so the same retry rules and the same messages without keys apply.

**An adapter's provider embeds when it has the method.** `adapters.OpenEmbedder` opens an entry as `Open` does and asks for `Embed`, and refuses an entry without the `embedding` capability. The `Set` of builders is unchanged.

**Two adapters make vectors.** The Chat Completions adapter speaks the OpenAI-style `POST {endpoint}/embeddings`, which the local gateways, LiteLLM, Ollama's compatible server and OpenRouter answer, so the owner's local and hosted embedding models all fit one adapter. The fake makes deterministic vectors from hashed words and meaning groups given as test data. The Anthropic adapter has no embedding call to speak; the Responses adapter's provider has one, and an OpenAI embedding model is registered on the Chat Completions adapter, which already speaks that call.

**A vector carries the provider's name for the model**, the entry's `provider_model`, or its registry name when it has none (the fake). Two entries for one provider model on two endpoints make vectors a store may compare, and a Waymarks instance, whose owner names the model it takes, is named in the provider's terms, not in a name one Keystone person chose.

## Why

- A message-shaped request for texts would carry roles, tools and thinking that mean nothing here, and a stream that is never a stream.
- Asking the provider for the method keeps the builders, the vault and the key handling as they are, and a test can hand in any embedder.
- One adapter for every endpoint that copies OpenAI's shape is what the plan's adapter list already says.

## What it costs

- An entry may claim `embedding` on the Anthropic or Responses adapter, and is refused when it is opened rather than when it is added.
- The registry entry has no vector size. A store that takes one size, as Waymarks does, refuses a vector of another when it is pushed.
