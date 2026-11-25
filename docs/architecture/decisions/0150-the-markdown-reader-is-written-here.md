# 0150. The Markdown reader is written in the interface, not taken from a package

November 25, 2026 · decided in P4-04

## The question

A model writes its replies in Markdown: paragraphs, lists, headings, tables, code blocks. The chat screen has to show that as it reads, while the text is still arriving. Does the interface take a Markdown package, or read it itself?

## The decision

It reads it itself. `web/app/src/Markdown.tsx` is about 300 lines: a pass that splits the text into blocks, one pattern for what happens inside a line, and a function that turns both into React elements. No package is added.

It reads paragraphs, headings, ordered and unordered lists with nesting, quotes, rules, tables, fenced code blocks, and inside a line code, bold, italic, struck text, links and bare addresses.

## Why

- **Nothing a model writes can become markup.** The reader builds React elements from text and never sets HTML. There is no HTML pass to sanitise, so there is no sanitiser to keep up to date. A link is made only from an `http`, `https` or `mailto` address.
- **The content security policy stays as it is.** The server sends `default-src 'self'`. A reader that sets no HTML and no inline style needs nothing loosened.
- **Few dependencies.** The usual choice, a Markdown renderer for React with tables, brings in dozens of packages. The interface has three.
- **It is written for text that is still arriving.** A code block with no closing fence runs to the end. The whole text is read again on each redraw, and that is fast enough at the length of a reply.

## What it costs

- It is not CommonMark. It reads what models commonly write and leaves the rest as plain text: HTML in the text is shown as text, and there are no reference links, footnotes, setext headings or task lists. A reply that leans on one of those reads a little wrong.
- No syntax colouring in code blocks, and no mathematics. Both would be packages. They are a later choice.
- The reader has no tests of its own. It was checked by eye against one long reply with every kind of block.

## When to look again

If a week of daily use (P4-05) turns up replies it reads badly more than now and then, take a package and keep the rule that no HTML is set from text.
