# 0221. Outside content marks the chat, a tool brings it unless it says otherwise, and a knowledge source counts while a page does not

November 25, 2026 · decided in P7-02

## The question

The plan: "Once a turn has read outside content (web pages, fetched files, email, connector results), every side-effect tool in that turn asks." What counts as outside content, how does Keystone know a tool brought some, and does the mark end with the turn?

## The decision

**A tool brings outside content unless it says it does not.** `tools.Tool` gains `Inside`: true when the tool's result holds only what Keystone keeps for the person, such as their chats or the clock. A tool that leaves it false is taken to bring outside content once it has run. The three built-in tools say they are inside.

**The knowledge store: a source counts, a page does not.** That is the recommended answer to the owner's open question (knowledge 005). `knowledge_read_source` brings outside content; `knowledge_read_page` does not; `knowledge_search` does when its result names a source; recall does when it kept a source for a call.

**The mark is on the chat.** Once a call of a chat has read outside content, every tool that changes something asks for the rest of that turn and in every later turn of the chat. A new chat starts unmarked. The mark is a row in `tool_outside`, and a mark that cannot be read counts as read.

**Calls of one step are decided together**, so a call after one that may bring outside content is decided as if it had.

## Why

- A forgotten flag must fail safe. With `Inside` false by default, the worst a tool author's slip does is make side effects ask.
- What a turn read stays in the chat's history, and every later model call is sent it again. An instruction hidden in a web page is as able to steer the next turn as this one. Ending the mark with the turn would let "always" run a side effect one message after the page was read. The plan's sentence is the least the rule must do; this does more and never less.
- A source is something brought in and kept unchanged: an imported document, the facts from an email. That is outside content by the plan's own examples. A page is written by the person and by Keystone's upkeep, and treating it as outside would make every chat with recall ask for everything.

## What it costs

- After one web search in a chat, every side effect in that chat asks, for good. A new chat is the way back to "always". If that proves tiresome, a per-chat "trust what this chat read" is a later choice for the owner, and it would be the person's answer, not content's.
- The knowledge search is told apart by the words recall writes for each hit. A change to those words is caught by a test in `internal/turn`.
- If the owner answers that pages count too, every lookup and every recalled page marks the chat. That is one function, `lookupInside`, and one line in `readOutside`.
