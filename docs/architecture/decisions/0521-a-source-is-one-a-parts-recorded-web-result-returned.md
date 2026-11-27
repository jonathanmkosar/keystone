# 0521. A source is one a part's recorded web result returned, and a citation with none is taken out

November 26, 2026 · decided in P8-04

## The question

A research report cites its sources by number. A model can write a number that names nothing, or a source it never read. What counts as a source, and what happens to a citation that has none?

## The decision

**A source is an entry in a `tool_result` of a `web_search` or `web_fetch` call, kept in a part's chat record, that is not an error and carries the outside mark**, read with `web.Sources`. A search result counts, with its extract as what was read, and the report's list says so; a fetched page counts as read in full.

**A citation that names no such source is taken out of the text and counted**, first in each part's findings and then in the report (`internal/research/cite`). The citations that are left are renumbered from 1 in the order they appear, and Keystone writes the list of sources, with their addresses, from the record. A list of sources a model wrote is taken off.

## Why

- **The record is the one thing a model cannot write.** A result in the chat record was returned by a tool Keystone ran and logged. A number, a title or an address in a model's reply is the model's.
- **Taken out, not flagged in place.** A reader skims a report's numbers as proof. A marked but visible bad citation still reads as one. The count is kept on the task, written in the report's "About this report" and said in the note, so the person knows it happened.
- **Search results count** because a part often reads only the extract, and the extract is text the service returned for that address. Calling it a source and saying it was an extract is truer than dropping it, and truer than calling it a page that was read.
- **The same reader as the chat screen.** `web.Sources` stops at the first block that is not a source, so nothing in a page's own text can add one.
- **Keystone writes the list** so every address in it is one a tool returned, and the numbers in the text and the list always agree.

## What it costs

- A true statement whose citation was wrong stays in the report with no number. The check proves a citation points at something read; it does not prove the source says what the sentence says.
- A source a part read through another tool, such as a connector, is not a source here. Only the web tools are read.
- The knowledge store's pages that recall gave a part are not numbered sources.
