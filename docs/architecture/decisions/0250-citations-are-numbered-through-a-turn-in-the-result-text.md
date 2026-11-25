# 0250. Citations are numbered through a turn, in the result's own text, and the screen reads them back

November 25, 2026 · decided in P7-03

## The question

A reply that used web search cites its sources as [1], [2], and the chat screen should link each number to its source. Who gives the numbers, how are they kept apart when a turn searches twice, and how does the screen learn which number is which address, when the record keeps a tool result's words and nothing else?

## The decision

**The tool gives the numbers, and they run on through one turn.** Each source a turn reads gets the next number: every search result, and every fetched page. A second search in the same turn, or a page fetched after a search, carries on from the last number. The person's next message starts again at 1. The tool keeps the count in memory for each chat, keyed by the id of the message the turn answers, which it reads from the chat record; two calls in one step, whose results are stored together only after both, still get numbers of their own. After a restart it reads the numbers the turn's stored results have already used.

**The numbers are written into the result's text**, in a fixed form: a first paragraph opening with `[Outside content]`, then for each source a block of three single lines, `[n] title`, the address, and a search result's extract. A fetched page has one block and then a block that says its text follows. The model reads the numbers where it reads the sources.

**The screen reads them back** from the web steps' results of the same reply (`web/app/src/citations.tsx`), and draws each [n] that names one as a link. `web.Sources` is the same reader on the server's side, which `verify` uses to prove that every citation in the recorded answer names a source the search returned.

**Reading stops at the first block that is not a source.** Each part of a source is one line, with runs of spaces and line breaks collapsed, so no title or extract can make a block of its own; and a fetched page's text comes after a block that is not a source. Nothing a web page says can add a source or change one.

## Why

- Numbers that start at 1 for each search collide as soon as a turn searches twice, which Claude models do often, sometimes twice in one reply. A [1] that could mean either is worse than none.
- Numbers given per turn read as Claude.ai's do. Numbers given per chat would reach [40] in a long chat.
- The record keeps a result's words. A form in those words is the same live and after a reload, needs no migration and no change to the chat record or the stream, and follows decision 0204, where the screen already reads a result's opening words.
- The model and the screen read the same text, so they cannot disagree about which number is which source.

## What it costs

- The form is a contract between `internal/tools/web/cite.go` and `web/app/src/citations.tsx`, with nothing but the docs and the `verify` step to hold them together. A change to one is a change to both.
- The count lives in memory. A turn that restarts mid-way is closed as interrupted (decision 0142), so this matters only if that changes; even then the record restores the count.
- Each search reads the chat's current branch once to find the turn's start.
- A model that cites a number no source has gets plain text, not a link. That is the honest drawing of a citation that leads nowhere.
