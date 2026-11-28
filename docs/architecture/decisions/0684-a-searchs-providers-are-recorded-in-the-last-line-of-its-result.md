# 0684. A search's providers are recorded in the last line of its result

November 27, 2026 · decided in P7-06

## The question

"Each search is in the audit log with the provider that answered, and every provider tried before it with why it was passed over." The audit log has three kinds (api, model, tool), its package enforces fixed rule 4 and reads nothing else, and `verify` counts a web search's entries under its request id: exactly one before and one after. Where does the record go?

## The decision

In the tool's own result, which the audit log already keeps whole: its last block is one line,

    Answered by bravo, after alpha (out of quota), charlie (unavailable).

or `Answered by bravo.` when the first tried answered. A search no provider answered has an error result with one line for each provider and why, which the log keeps the same way. `web.Answered` reads the line back, for the step row that says which provider answered when it was not the first tried (P7-08). The reasons are a few fixed words: out of quota, rate limited, unavailable, key refused.

## Why

- **No change to the audit package**, which guards a fixed rule, and none to the checks that count a search's entries.
- **The record cannot drift from the call**: it is written in the same entry as the result, by the same code.
- **The step row needs it anyway**, and reads it from the chat record, which holds the result too.

## What it costs

- The model reads the line. It is short, says nothing private, and comes after the numbered sources, so citations and `web.Sources` are unchanged (the reader stops at the first block that is not a source).
