# 0294. A chat's marks go with its task, both ways

November 25, 2026 · decided in P8-01

## The question

Two fixed rules rest on marks a chat carries: a chat that read outside content asks before every tool that changes something (rule 2, decision 0221), and a chat that was sent content from a sensitive space is held to models that may read it (rule 3, decision 0211). A task's work chat starts empty, and its words come from another chat; its result goes back there. Without care, a task would launder both marks: words written after reading a web page could drive a changing tool set to always in a clean work chat, and words from a sensitive page could reach a hosted model there.

## The decision

**A work chat starts with the marks of the chat its task was started from**: marked as having read outside content when that chat had, with the reason "the chat this task was started from had read outside content", and held when that chat was held.

**The chat a task's note lands in takes the work chat's marks**: marked when the work chat read outside content, and held when it was held.

**The marks are written before they could be needed**: before the first round can run, and before the receiving chat can run another turn. `turn.Runner.Send` and `turn.Runner.Note` hold the chat's queue while they write them. A mark that cannot be read counts as set.

## Why

- It is the rule as it stands, carried with the content: what was read stays in what is written from it. It can only make a task ask more or route more narrowly, never less.
- Marking a work chat from a clean chat would make every task ask for every side effect, and a person who set a tool to always would be asked anyway. Taking the marks as they are keeps the rule exactly as strict as it is in a chat.
- Holding the queue while the marks are written closes the moment in which a turn could run before its chat was marked.

## What it costs

- A task started from a chat that once read a web page asks for every side effect, for good, as that chat does.
- A chat that receives the result of a task held for sensitive content is held from then on.
