# 0281. A held chat's facts land in a space the store says is sensitive, or nowhere, decided before any model reads

November 25, 2026 · decided in P6-06

## The question

A chat that was sent content from a sensitive space is held to models that may read it (decision [0211](0211-a-chat-sent-sensitive-content-is-held.md)). Its exchange may repeat that content, so the facts drawn from it are sensitive content too. Fixed rule 3 says they reach only local or trusted models. Where may they be kept, who may draw them, and how does Keystone know a space is sensitive when the store adds a space it does not hold as a normal one?

## The decision

**A space of the person's choosing, confirmed by the store.** `knowledge.memory_sensitive_space`, empty by default, names the space for such facts. The facts go there only when the store's catalog lists pages in that space and labels every one of them sensitive. An empty setting, a space with no page in it, and a space that is not labelled sensitive mean the facts are kept nowhere.

**Decided before any model reads.** `memory.Target` is asked first. When the answer is nowhere, the pass is skipped and no model is sent the exchange: there is no point paying for facts that cannot be kept, and no facts exist to leak.

**Drawn only by a model that may read them.** The memory route of a held chat is planned as sensitive, like every route of the chat, so a model that may not read sensitive content is left out of it, as the rule or as a fallback. `memory.Add` refuses sensitive facts from a model that may not read them, whatever route they came by.

**Asked again after the call.** If the chat was held while the model read, its facts are taken to be sensitive: they go to the confirmed space, or nowhere, and `Add` refuses them from a model that may not read them.

**The label they landed with is read.** A sensitive source that the store did not label sensitive is a line in the log and a reason on the pass: the space was made ordinary under the pass.

**The package reads no setting.** `internal/knowledge/memory` is a guard of `rules.sensitive_content`, and does not import the chat record, which reads settings.

## Why

- The rule's own words: content from a sensitive space reaches only local or trusted models. A fact that repeats it is that content.
- A name alone proves nothing. `AddSource` makes a space it does not hold as a normal one, so naming a space that does not exist would put sensitive facts in an ordinary space. The catalog is the store saying, in its own words, what the label is, and a label left blank reads as sensitive (decision [0031](0031-label-reads-as-sensitive.md)).
- Asking before the call keeps rule 3 out of the model call altogether in the common case, and costs nothing.

## What it costs

- The sensitive space must hold at least one page. A space that holds only sources, or a new one, cannot be confirmed: the person makes a page in it first, or the facts are kept nowhere.
- The catalog is read for each pass of a held chat. The person map already reads it for each turn.
- A chat held by a later turn, while an earlier turn's pass ran on a hosted model, loses that pass's facts, though that exchange held nothing sensitive. It is rare, and the safe side.
