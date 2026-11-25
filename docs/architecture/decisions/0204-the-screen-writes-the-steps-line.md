# 0204. The chat screen writes each step's readable line from the tool's name, input and result

November 25, 2026 · decided in P7-01

## The question

The approved step rows in `MainDark.html` read as sentences: "Searched Waymarks for boiler service", "Read Boiler, Vaillant ecoTEC plus", with a few words on the right such as "3 pages". The stream sends a tool call's name and input, and later its result. Who turns that into a sentence, and how does a step that was not run say so, when the stored record keeps only the result's words?

## The decision

**The screen writes the line**, in `web/app/src/steps.ts`. For each tool that comes with Keystone it has three phrasings: what it is doing while it runs ("Searching your chats for “lighthouse”"), what it did once it answered ("Searched your chats for “lighthouse”"), and what the model asked for, for a step that gave no result ("Check the date and time"). It may read the opening words of a result for the few words on the right ("3 matches") or for a chat's title ("Read “Planning the lighthouse walk”").

**A tool the screen does not know** is still shown: "Used send_postcard: to “The lighthouse keeper”, message “Thank you for the light.”", from its name and the first plain values of its input, cut short.

**A step that came back as an error is labelled from the result's words**: "not run" (it needs an approval, the turn was at its step limit, or tools are off) and "stopped" in the plain tag, "failed" and "no such tool" in the error colour. The words matched are the server's own, in `internal/tools` and `internal/turn/tools.go`.

**Nothing was added to the stream.** The step limit's note is drawn from its message's stop reason, `step_limit`.

## Why

- The record keeps the tool's name, its input and its result, and nothing else. A line the screen writes from those is the same live and after a reload. A line or a status sent only in the stream would be lost on reload, unless the record kept it too, which means a migration and a change to the chat record's format for words a screen draws.
- The words belong to the interface, in the person's language, next to the rest of its words. A phone client written later can choose its own.
- A tool from a later item gets a readable line on the first day, and a better one when a phrasing is added beside the others.

## What it costs

- The screen reads the server's sentences to tell "not run" from "failed", and the opening words of `search_chats` and `read_chat` results for the few words on the right. A change to those sentences changes the screen's labels, with nothing to catch it but the eye. If that happens twice, the tool result should carry a status in the record.
- Each new tool needs a phrasing here to read well. Without one it reads as "Used tool_name: …".
- The tag says "not run" for a refusal by the approval rule, the step limit and tools being off alike. The step's open body has the reason.
