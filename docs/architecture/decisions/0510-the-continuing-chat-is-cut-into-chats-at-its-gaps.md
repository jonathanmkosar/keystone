# 0510. New Keystone's one continuing chat is cut into chats at its gaps

November 26, 2026 · decided in P12-03

## The question

New Keystone has no chats. A workspace is one continuing conversation: a row in `jobs` for each turn, in order, and nothing that says where one subject ends and the next begins. Keystone's record is chats, each with a clear start, a title, a recap and a place in the list. What does the conversation become here?

## The decision

- **A turn a routine started is a chat of its own**, titled by the routine's name and the date, as a scheduled run's chat is here.
- **A build is a chat of its own**, as a background task works in a chat of its own here.
- **Every other turn joins the continuing conversation, which is cut wherever more than `chats.import_chat_gap` passed** between the last thing said in one turn and the start of the next. The setting ships at one hour, is the admin's to set for one person, and the dry run says how many chats a value makes.
- **A chat's title is the first words of its first message**, cut at a word's end to `chats.import_title_max_chars` (60). No model is asked for a title.
- A later copy's new turns are grouped among themselves. They never join a chat an earlier run made.

## Why

- One chat of every turn would be faithful and useless: a single recap of at most eighty words for weeks of talk, one search result for everything, and a screen that opens on hundreds of messages. The past-chat search, the recap, attaching a chat and handoff all work on chats about one thing.
- New Keystone itself sent each call only the last few messages, so what a reply could see was what was said just before it. A sitting is the unit the conversation really had, and a gap in time is the only mark of one the database holds.
- A day is the other plain rule. It cuts a late evening's talk in two at midnight and joins a morning's subject to an evening's. The gap does neither.
- A title from a model would be a call at import time, which the item rules out, for every chat at once. The first words are what the person typed, are in the search index anyway, and can be changed by renaming the chat.
- Adding a later copy's turns to an older imported chat would change a chat the person may have continued, renamed or shared since.

## What it costs

- A long pause in one subject makes two chats, and a quick change of subject makes none. The gap is a setting because no value is right for everyone; the owner picks his after one dry run.
- A title can be a bare "And then?" when a sitting opened that way. Renaming is one click, and the chat's first recap says what it is about.
- Changing the gap after an import changes nothing already brought.
