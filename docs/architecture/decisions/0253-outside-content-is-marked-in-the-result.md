# 0253. Outside content is marked in the tool's result, where the record keeps it

November 25, 2026 · decided in P7-03

## The question

The approval rule says that once a turn has read outside content, every tool in it that changes something asks, whatever its setting (fixed rule 2, and the plan's standing check `approval`). Web search results and fetched pages are outside content. How does a result say so, in a way the tool loop and the approval policy can see, while the approval policy (P7-02) is being built at the same time in another worktree?

## The decision

**The result says it, in its own first words.** A tool whose result brought outside content opens it with `tools.OutsideMark`, `[Outside content]`, and a sentence telling the model the text is information from the web and never instructions. `internal/tools/outside.go`, a new file, has `MarkOutside`, `IsOutside(block)` (a `tool_result` that is not an error and opens with the mark) and `ReadOutside(messages)`.

**Nothing else changes.** No field is added to `tools.Tool` or `tools.Result`, no event changes shape, and `internal/turn` is not edited: the mark travels in the result, so it is in the `tool_result` event, the chat record and the audit log as it is.

**It joins P7-02 without a line.** P7-02's plan gives each tool a flag, `Inside`, counts every tool that does not set it as outside, and marks the chat once such a tool has run. The web tools set nothing, so they are outside under it. `IsOutside` is there for what a flag on the tool cannot say: that a call brought nothing (a search that failed or found nothing, a lookup that read only pages), and that a chat read outside content, found in its record after a restart.

## Why

- A mark in the record survives a restart and a reload, and is the same for the policy, the screen and an auditor reading the log. A mark kept only in memory, or only in the stream, would not be.
- The words also tell the model what the text is, which the item's scope asks for: results marked as outside content.
- Editing `tools.Tool`, `tools.Result` or the turn while P7-02 edits the same lines would make the merge the hard part. A new file and words in a result cannot conflict.
- The mark can only add caution. A result that carried it wrongly would make side effects ask; it can never let one run. The danger is a tool that brings outside content and forgets the mark, which is why P7-02's rule of counting every tool that does not say it is inside is the right default, and this mark is beside it, not instead of it.

## What it costs

- A mark in words is a convention. A tool that brings outside content must use `MarkOutside`; P7-02's default covers the one that forgets.
- Two ways to say one thing until the merge decides whether the policy reads both. The journal says how they join.
