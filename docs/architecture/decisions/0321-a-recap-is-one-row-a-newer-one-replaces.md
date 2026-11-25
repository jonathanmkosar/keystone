# 0321. A recap is one JSON answer and one row for each chat, which a newer recap replaces and extends

November 25, 2026 · decided in P5-05

## The question

A chat's summary, topics and decisions: how is the model asked for them, and how are they kept? Compaction keeps a chain of summaries that extend one another; the memory pass asks for a JSON object of facts.

## The decision

**One request, one JSON object.** The prompt (`chats.recap_prompt`) asks for `{"summary": "...", "topics": [...], "decisions": [...]}` and nothing else, with the most words, topics and decisions written into it. The answer is read as the memory pass reads its facts: from the first `{` to the last `}`, checked on the way in, items that are blank, too long, repeated or past the limit left out and counted, a summary past its words cut at a word's end. A missing summary fails the recap.

**One row for each chat.** `chat_recaps` is keyed by the chat. A newer recap replaces the row; the sensitive mark is the larger of the two and never goes back. A full-text index of the summary, topics and decisions is kept in step by triggers, and goes with the chat.

**Extended, not made again.** When the chat's recap was made from a message that is still on its current branch, the request holds the recap so far and only the messages after it, and the model is told to keep what it says unless a later message changes it. When the person has edited back before it, the whole branch is sent. A long chat sends its first message and its newest, with a line saying how many were left out between.

## Why

- Three fields from one call cost one call. The seam has no response format yet, and the memory pass's way of asking for JSON has held up.
- Search and the chat list want the chat's one current card, not its history. A chain would make every reader pick the right link, as compaction's callers must; nothing here needs the old cards.
- Extending keeps the cost of a recap near the size of what was said since, as compaction's summaries do, and keeps decisions made early in a long chat that the window would no longer show.

## What it costs

- A recap's history is not kept: a summary that lost a detail cannot be compared with the one before.
- Extending can carry a mistake forward until a later message contradicts it.
- After an edit back, the recap is made from the whole branch, cut to its first and newest messages when long, so it can miss the middle of a long branch.
