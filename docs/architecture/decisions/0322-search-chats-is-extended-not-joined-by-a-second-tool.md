# 0322. search_chats is extended into the past-chat search, and no search_past_chats is added

November 25, 2026 · decided in P5-05

## The question

The item asks for a past-chat search tool the model can call, named in its brief `search_past_chats`, with a query and a limit. The tool loop (P7-01) already has `search_chats`, a search of titles and messages, and `read_chat`. The brief says to extend or replace them rather than add a duplicate, and to say which.

## The decision

**Extend `search_chats`, under its own name.** It now matches any of the words given in titles, chats' recaps and messages, ranks chats, and gives each with its title, id, date, summary, topics, decisions and best passages; it takes a `limit`. `read_chat` gives the chat's recap too. The name `search_past_chats` is not used.

What stays: the name, the `query` input and its shape, the opening "N matches for …" and "No other chat matches …", "The title matched.", and the passages' "The person wrote:" and "The reply said:".

## Why

- Two search tools over the same chats would be the duplicate the brief rules out, and would leave the model to choose between them.
- Replacing the name would break what already names it: the test tenant's recorded task of several steps and the `calls recorded` check in `verify`, which asserts the calls `search_chats current_time` (shared check files may only gain lines), the chat screen's words for the step (`web/app/src/steps.ts`, which reads the result's opening), a person's stored policy for `search_chats`, and every chat whose history holds a call to it, which a model may make again.
- `search_chats` says what it does as plainly as `search_past_chats`, and its description now says it searches earlier chats' summaries and decisions.

## What it costs

- The tool's name is not the one in the item's brief. Anyone looking for `search_past_chats` finds this record.
- The result is now per chat, not per matching message, so "N matches" counts chats. The screen's row reads the same words and shows the new number.
