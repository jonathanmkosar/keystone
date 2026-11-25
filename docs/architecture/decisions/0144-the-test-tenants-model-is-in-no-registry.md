# 0144. The test tenant's model is in no registry, and the fake provider's titles come from the message

November 25, 2026 · decided in P4-03

## The question

The test tenant must always get the fake provider with its recorded turns, and nothing but the fake (decision [0080](0080-the-test-tenant-is-marked-in-its-own-database.md)). The loader stores no model for it. And a chat needs a title after its first reply, but the fake can only say what a script says. How are both met without a special case in the chat screen?

## The decision

**For a store that `testtenant.IsTest` marks, the turn runner does not read the registry.** It uses one model, `recorded-turns`, made in code: the fake adapter, class `local`, with a window and an output limit from two settings. The provider is `testtenant.Provider`. A `model` named in the turn is ignored.

**The mark is read for every turn, from the database.** Nothing is cached, so a test tenant that is added while the server runs is the test tenant on its next turn.

**The same model is what the API shows**: `GET /chats/{id}/model` answers with it, with the source `test` and `test: true`.

**A model on the fake adapter writes no title.** The title is the first `chats.title_derived_words` words of the person's message. The same derived title is used when a real title call fails or runs out of time.

## Why

- "Nothing but the fake" should not depend on what is in a table. If the test tenant's registry were read, one entry added to it, by a route or by SQL, would send test data to a real provider. With the registry never read, there is no entry to get wrong.
- A model made in code needs no step in the loader, no migration of existing test tenants, and no key.
- A scripted title would be the same words for every chat, or one script for each message anyone might type. The catch-all script would title every chat "This is the test tenant…". The message's own first words are different for each chat and need no fixture.
- A derived title as the fallback means a chat is never left untitled because a small model was slow.

## What it costs

- The test tenant cannot be used to try a real model. That is the rule, and an ordinary tenant is one command away.
- A derived title is a cut-off sentence. It is replaced by a real one as soon as the chat is on a real model, and a person can always rename it.
- The window and the limit of an invented model are two more settings.
