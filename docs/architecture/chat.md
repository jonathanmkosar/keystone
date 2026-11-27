# The chat record

Folder: `internal/chat`

The chat record keeps every chat, every message and every branch for one tenant. It lives in the tenant's own database and never moves into the knowledge store.

The package is plain functions over a `*tenant.Store`. A store is one tenant's, so a function can only reach that tenant's chats. There are no routes here. The API (P2-05) and the turn runner (P4-03) call these functions.

## Chats

A chat has an id, a title, a star, the time it was made and the time of its last message.

- `Create` makes a chat with no title. The id is sixteen random hex characters.
- `Get` returns one chat. `List` returns them with the newest message first. It can keep starred chats only, and takes a limit and an offset.
- `Rename` gives a chat its title. Runs of spaces and line breaks become one space. An empty title, or one longer than `chats.title_max_length`, is refused.
- `Star` sets or clears the star.
- `Delete` removes the chat. See "Deleting a chat" below.

A new message moves a chat to the top of the list. A new name or a star does not.

An id that is not in the record gives `ErrNotFound`. Input that is refused gives `ErrInvalid`, with the reason. Check for either with `errors.Is`.

## Messages are a tree

Decision [0020](decisions/0020-messages-are-a-tree.md) says why.

Each message names its parent: the message it answers or follows. A first message has no parent. Each parent remembers its **current child**, and the chat remembers its current first message.

- **The current branch** is the path that starts at the chat's current first message and follows each current child down to a message with no children. `Path` returns it, first message first.
- **Append** adds a message under a parent and makes it that parent's current child. It never changes a message that is there.
- **Edit** is an append under the edited message's parent. **Retry** is an append under the retried reply's parent. Either way the parent now has two children, and that is a branch. Editing the first message gives the chat two first messages.
- **Siblings** are the messages with the same parent. Every `Message` carries the ids of its siblings, itself included, oldest first. More than one means there are branches at that point, and the list is what a "2 of 3" switcher steps through.
- **Select** puts a message on the current branch: it becomes its parent's current child, and so does each message above it. Below it, earlier choices are kept. So stepping to a sibling and back shows each side as it was last left. Select also opens a message deep in a branch that is not current, such as a search result, in one step.

Appending under a message that is not on the current branch does not move the current branch. A reply that arrives after the person stepped away is there when they step back.

`Append` does not check the order of roles. What may follow what is for the turn runner to decide.

## A message's content

Content is a `seam.Message`: a role and its blocks, in the [model seam's internal format](seam.md). `Append` writes it with `seam.EncodeMessage` and every read goes through `seam.DecodeMessage`, so what the seam refuses, the record refuses.

A message's id is a number. It is never used twice in one tenant's record, and a later message has a larger one.

`Append` takes a `NewMessage` struct, and every query names its columns. P4-03 adds what a turn records, such as the model and the usage, as a new column and a new field. Nothing here is rewritten.

## Search

`Search` looks through titles and the words of messages' text blocks, on every branch, with SQLite's FTS5.

- Every word of the query must be in the same title or the same message. A word also matches as the start of a longer one, so `plan` finds `planning`.
- Capitals and accents do not matter.
- Each word is quoted before it reaches FTS5, so nothing a person types is read as search syntax.
- Title matches come first, then message matches, each with the best match first. There are at most `chats.search_max_results`.
- A `Hit` names the chat and its title. For a message it also has the message's id, its role, and a snippet of about `chats.search_snippet_words` words around the match. For a title the message id is zero.

Thinking, tool calls, tool results and file names are not indexed.

The two search tables hold their own copy of the text. `Rename`, `Append` and `Delete` write and remove search rows in the same transaction as the row they index, so the index cannot fall out of step.

Since P5-05, `MatchAny(store, query, most, snippetWords)` (`match.go`) searches the same two tables for any of the query's words instead of all of them, and returns the title rows and the message rows apart, each the best first by the full-text rank with the rank as `Score`. The past-chat search, `search_chats`, ranks chats by them with the chats' recaps ([chat-summaries.md](chat-summaries.md)). A chat's recap is kept by `internal/recap` in tables of its own, which go with the chat.

**Over years of chats** (P13-03, `ranked.go`, decision [0530](decisions/0530-a-search-cuts-snippets-only-for-what-it-gives-and-ranks-so-many-matches.md)). A common word is in most of what a person ever wrote, and the load test found a search for one taking seconds over 100,000 messages: every match was joined to its chat and cut to a snippet before the best fifty were kept. Since then:

- The index is asked for its best few in its own order, and only those are cut to a snippet and read with their chats. Equal ranks are put newest first.
- At most `chats.search_rank_max` matching messages are ranked. When more match, the search is too wide for a rank to mean much: the newest matches are given, newest first.
- `MatchAnyWithin(store, query, most, snippetWords, rankMax)` is `MatchAny` with that bound, and the past-chat tool calls it. In a search for any of several words it leaves a word out of the search of messages when more messages than the bound hold it and another of the words is in fewer, so a rare word finds its old chat whatever common words came with it. `MatchAny` itself ranks everything, as a bound below 1 does.

A record with fewer matches than the bound gets the answer it got before.

## Files

A file is tied to a chat by a row in `chat_files`: the chat and the file's name in the tenant's `files/` folder. `Append` ties every file a message's blocks refer to by `ref`, tool results included. `AddFile` ties one by hand, for a file that is uploaded before its message is sent. `Files` lists a chat's files.

Files are named by hash, so two chats can hold the same file. A name that is not a plain path inside the folder is refused.

Writing the file, its media type and its extracted text are uploads, P9-02.

Since P9-02 the files a chat holds are mostly its uploads: kept under their SHA-256, with a row each in `uploads`, and tied to the chat here so deleting it removes them. A message names an upload by its hash in a block's `ref` ([uploads.md](uploads.md)).

## Deleting a chat

`Delete` removes, in one transaction:

1. The chat's rows in both search tables.
2. The chat's row. Its messages and its `chat_files` rows go with it, by `ON DELETE CASCADE`.
3. From the `files/` folder, each of the chat's files that no other chat holds.

The files go before the transaction commits. If one cannot be removed, the transaction is rolled back, the chat stays, and `Delete` can be run again. A file is never left in the folder with no chat that names it. A file that is already gone does not stop the delete.

**A later table that belongs to a chat** names it with `REFERENCES chats(id) ON DELETE CASCADE`. `Delete` then removes its rows with no change here.

**A chat's artifacts** (since P9-03) are kept in a folder named by the chat's id in the tenant's `artifacts/` folder. `Delete` removes that folder after the files, before the transaction commits, once the chat's row is known to have been there; its `artifacts` and `artifact_versions` rows go by `ON DELETE CASCADE` ([artifacts.md](artifacts.md)).

## Pinned messages

Since P5-02 a person can pin a message so it is never left out of a call. `Pin(store, chatID, messageID, pinned)` pins one, on any branch, or with `pinned` false takes the pin off; pinning twice or unpinning what is not pinned changes nothing. `Pins(store, chatID)` returns the pinned ids, oldest first. A chat or message that is not in the record gives `ErrNotFound`. The rows are `chat_pins`, from the migration `20261125T110000_compaction.sql`, and go with their chat. What a pin does to a call is in [compaction.md](compaction.md).

## Tables

In `internal/tenant/migrations/20261124T213000_chat_record.sql`.

| Table | Holds |
| --- | --- |
| `chats` | One row for each chat: `id`, `title`, `starred`, `current_root`, `created_at`, `active_at`, and `seq`, the row number the title index is keyed by. |
| `messages` | One row for each message: `id`, `chat_id`, `parent_id`, `current_child`, `role`, `content` (the seam's JSON), `created_at`. |
| `chat_files` | One row for each file a chat holds: `chat_id`, `name`. |
| `chat_title_search` | FTS5 over titles. A row's `rowid` is `chats.seq`. |
| `message_search` | FTS5 over message text. A row's `rowid` is `messages.id`. |

Times are text in UTC with milliseconds, one fixed width, so they sort as text.

## Settings

In `settings/chats.json`.

| Key | Default | Meaning |
| --- | --- | --- |
| `chats.title_max_length` | 150 | The most characters a title may have. |
| `chats.search_max_results` | 50 | The most results one search returns. |
| `chats.search_snippet_words` | 24 | About how many words of a matching message a result shows, from 1 to 64. |
| `chats.search_rank_max` | 20000 | The most matching messages one search ranks; when more match, the newest are given. For the whole install. Zero ranks every match. |

The functions that use them take a `Limits` value. `DefaultLimits` reads the registry's defaults. Stored values come with P2-04, and the caller then builds `Limits` from the tenant's own.

## Checked by

`go test ./internal/chat`.

- Edit and retry make branches, and the branches are walked back and forth: each side reads as it was left, and no message is changed.
- A chat 600 messages deep is read and selected in one step.
- Every block type goes into the record and comes back the same, and what the seam refuses is refused.
- Search finds titles and text, on every branch, and survives anything typed into it. FTS5 is compiled into the driver.
- A deleted chat leaves no row, no search row and no file. The other chat's rows and files, and a file both chats held, are untouched. A delete that cannot remove a file keeps the chat.
- Two tenants' chats and search results never mix, and one tenant's ids mean nothing in the other's store.

## Not here yet

- Routes, and the shape of a chat in the API: P2-05 and P4-03.
- The model, the usage and automatic titles: P4-03. The automatic title should be set only while the title is empty, so it never replaces one the person chose.
- Summaries, topics and decisions, and the past-chat search tool: P5-05.
- Projects: P9-01. A file a project also holds must be counted before `Delete` removes it.
- Artifacts live in their own folder, and P9-03 decides how a chat's artifacts are removed.
- Deleting one message or one branch. Only a whole chat is deleted.
