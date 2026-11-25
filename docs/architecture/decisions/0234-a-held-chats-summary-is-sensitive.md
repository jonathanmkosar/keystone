# 0234. The summary of a held chat is sensitive, and goes only where the chat may go

November 25, 2026 · decided in P5-02

## The question

A chat that was sent content from a sensitive space is held to models that may read it (decision [0211](0211-a-chat-sent-sensitive-content-is-held.md)). Its turns, and so its summary, may repeat that content. The summary task has its own rule, `routing.summary_model`, which may name a cheap hosted model. Fixed rule 3 says sensitive content reaches only local or trusted models. How is that kept for summaries?

## The decision

**Twice, as recall keeps it.**

1. **Who makes it.** The summary route is planned like every route of the chat: `routing.Plan` is told the call is sensitive when `recall.Held` says the chat is held, and keeps only models that may read sensitive content, for the rule's model, the chat's own and the fallback chain. A held chat whose summary rule names a hosted model is summarised by the chat's own local or trusted model, or not at all.
2. **Who is sent it.** A summary is stored with `sensitive` set when the chat was held when it was made, read before and after the call. `compact.Usable` gives a sensitive summary only to a call whose every model may read sensitive content: the least of the call's route, as recall's answer is (decision [0210](0210-recall-searches-once-before-the-route.md)).

**`internal/compact` is a guard of rule 3.** It imports neither the settings registry nor the tenant's overrides, and is listed in `fixedRuleGuards`, so `verify` fails if it ever does.

## Why

- A summary is a rewrite of the chat's history, and the history is what holds a chat. A summary of sensitive content is sensitive content.
- The route already says no to a hosted model for a held chat; the stored mark makes the summary safe on its own, should a later item send it somewhere a chat's route does not go, such as an attached chat (P5-06) or a handoff.
- The mark is read when the summary is made, and is never taken back, as the chat's own mark is not.

## What it costs

- A held chat with no local or trusted model in its route gets no summary, and its oldest turns are left out with the note. Its chat calls would fail with `sensitive` first.
- A summary made before a chat was held is not sensitive, and may go where it went before: it was made from turns that held nothing sensitive.
