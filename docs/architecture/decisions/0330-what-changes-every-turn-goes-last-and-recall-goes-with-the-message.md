# 0330. What changes on every turn goes last, and recall goes in the person's message

November 25, 2026 · decided in P5-03

## The question

A provider caches the start of a call. The Anthropic Messages API reads the tools, then the system text, then the messages, and a cached start is found again only when every byte of it is the same. OpenAI and most gateways cache the longest prefix they have seen, which comes to the same thing. Since P5-01 the standing start (rules, profile, person map, project, style) came first and stayed the same, but recall sat in the system text after it, and recall changes with every message. So the system text changed on every turn, and with it everything after it: the whole history was read again at the full price on every turn of a long chat, and a local gateway worked through the whole prompt again. Where should what changes go?

## The decision

**The request carries the parts in a fixed order, `contextbuild.Layout`, with what changes least first:**

1. the tools, sorted by name;
2. the standing start;
3. the parts that change only when compaction runs or turns are left out: attachments, the summary, pinned messages sent on their own, the note;
4. the earlier turns, as the chat record holds them;
5. recall and the current turn, which change on every call.

**Recall goes in the current turn's first message, as text blocks before the person's words.** The system text holds only parts 2 and 3, so it stays the same while a chat goes on, and so does every byte of the history. In a turn of tool steps the recall stays in the turn's first message, so each step sends the same start as the step before.

The packing does not change: what is kept, dropped and held room for is decided as before (decisions 0170 to 0172); only where each kept piece is written moves. The record says the order it used (`Record.Order`).

## Why

- It is the only place recall can go without spoiling the cache of everything after it. At the end of the system text it spoils the history; in the history it would have to be sent again with every later turn.
- Before the person's words, not after: the model reads the material and then the question, the order Anthropic's own advice on long context gives, and the question stays the last thing in the call.
- Each recalled item already says where it came from ("From the person's knowledge store, found by searching for their message"), so it reads as material, not as the person speaking. In the user's turn it also has less weight than it had in the system text, which is the safer side for text that came from a store.
- The tools by name: the registry, the knowledge lookups and the connectors (P7-04) each add tools in their own order. Sorted, the same set is the same bytes on every call, and a new tool moves none of the others.
- The same order helps every provider: OpenAI's automatic cache, OpenRouter's, and a local gateway's own reuse of the prompt it has already read, which makes its first token come sooner.

## What it costs

- The person's message in the call is not exactly what the chat record holds while recall is with it. Anything that reads the person's words from a request has to allow for that. The fake provider was one: a script's `user_text` now also matches the message's last text block, which is the person's own words. Every match that held before still holds.
- The current message is sent with recall on its turn and without it on every later turn, so its bytes as history differ from its bytes as the current message. The history mark of the next turn starts after it, and a turn reads all of the chat but its last turn from the cache. That is the price of not keeping old recall in the chat.
- The note, the summary and pinned messages still sit in the system text. When compaction runs or the first turn is left out, the system text after the standing start changes once, and that call reads only the standing start from the cache.
- The search-first rule is part of the rules, so a store that is down for one turn changes the standing start of that call (decision 0214).
