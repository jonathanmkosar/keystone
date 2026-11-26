# 0412. The embedding model is chosen local first, one model makes all of a person's vectors, and sensitive pages wait for a local or trusted one

November 25, 2026 · decided in P6-08

## The question

The plan: embedding is local first, and a sensitive page is embedded only by a local or trusted model. `routing.embedding_model` names the model, empty by default, and an empty rule elsewhere means the chat's own model, which cannot embed. What does an empty rule choose, may a hosted model be chosen, what happens to sensitive pages when the model is hosted, and what of the message recall turns into a vector?

## The decision

**The model.** `embedding.Choose`: the model `routing.embedding_model` names, when it is an embedding model the person can use, whatever its class; otherwise the first local embedding model by name, then the first trusted one. A hosted embedding model is used only when it is named: making vectors sends every normal page the person keeps to that model, and that is a choice a person makes, not one Keystone makes for them.

**One model for all of a person's vectors.** A query vector is compared with page vectors of its own model only, so a store whose pages came from two models would need two query vectors, and Waymarks takes one model in any case.

**Sensitive pages and a hosted model.** `embedding.MayEmbed` is fixed rule 3 for a page: a sensitive page goes only to a model that may read sensitive content. With a hosted model, sensitive pages are left without a vector. They are still found by their words, the refresh counts them, and the person is told how many and how to have them searched by meaning too (name a local or trusted model). The rule is asked of the label the store listed the page with and again of the label it was read with. `internal/embedding` imports no settings and is a guard in the `fixed rules` check.

**The query.** `embedding.MayEmbedQuery`: what a person writes in a chat that holds sensitive content may repeat it, so the query of a chat whose route is sensitive (it was sent sensitive content, or holds a sensitive attached chat) goes only to a local or trusted embedding model; otherwise recall searches by words, and the record says why.

## Why

- Local first, as the plan says, with no surprise spend and no page leaving the home network unless the person asked.
- A held chat's message is held for the same reason its history is (decision 0211).

## What it costs

- A person with only a hosted embedding model gets no search by meaning for their sensitive spaces.
- A person who names a hosted model and later adds a local one keeps the hosted one until they clear the setting.
