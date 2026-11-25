# 0110. The Responses adapter stores nothing at the provider, and reasoning travels in the signature

November 25, 2026 · decided in P3-05

## The question

The Responses API can keep each response and let the next call name it, so the provider holds the conversation and the reasoning behind it. Or a call can set `store` to false and carry everything itself. Which does Keystone use, and if nothing is stored, how does a model's reasoning get from one call of a turn to the next?

## The decision

**Every call sets `store: false` and names no earlier response.** The whole conversation is sent each time, from Keystone's own chat record.

**Reasoning comes back encrypted and is kept in the thinking block.** For an entry with the `thinking` capability the adapter asks for `reasoning.encrypted_content`. A reasoning item becomes a thinking block: the summary is its text, and its signature is `responses/1:` followed by JSON with the item's id and the encrypted content. On the next call the adapter turns that block back into a reasoning item.

**A thinking block whose signature does not start with `responses/1:` is left out of the request.** It came from another provider, and this one could not read it.

## Why

- The plan keeps the chat in the chat record, and says nothing outside the adapters sees a provider's format. A conversation held at the provider would be a second copy, outside the tenant's folder, that backup, deletion and the audit log do not reach.
- The seam already has a place for exactly this: a thinking block's signature is "opaque, and goes back to the same provider unchanged". Nothing in the seam had to change.
- A stateless call can be retried, replayed from a fixture, and sent to a different model after a fallback. A call that names a stored response can do none of these.
- The prefix makes the block safe to carry across a model change. Without it, a signature from the Anthropic adapter would be sent to this provider as encrypted reasoning and the call would be refused.

## What it costs

- Every call sends the whole conversation, so the input is larger. The provider's cache takes most of the price of that, and the usage reports it.
- Whether the provider accepts a reasoning item sent back this way while nothing is stored is inferred, not checked. If the live check shows it does not, the fallback is to leave reasoning items out, which costs the model its earlier thinking and nothing else.
- A turn that falls back from one provider to another loses the first one's reasoning. Its text and tool calls carry over.
- The signature is longer than the encrypted content alone, and is stored with the chat.
