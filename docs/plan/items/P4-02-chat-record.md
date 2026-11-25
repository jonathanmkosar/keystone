# P4-02. Chat record

Status: done
Lane: chats
Depends on: P2-01
Owner gate: none
Scope: Chats, messages and branches; titles; star, rename, delete; search over titles and text.
Proof: Edit and retry create branches that can be walked back and forth; deletion removes a chat and its files.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

- A new package, `internal/chat`: plain functions over one tenant's store. There is no API yet (P2-05), so there are no routes. The API and the turn runner (P4-03) will call these functions.
- One migration, `20261124T213000_chat_record.sql`, with three tables and two search tables: `chats`, `messages`, `chat_files`, `chat_title_search` and `message_search`.
- Chats: create, get, list with the newest activity first and a starred filter, rename, star and unstar, delete.
- Messages are a tree. Each message names its parent, and each parent remembers its current child. An edit or a retry is a new message under the same parent, so nothing is overwritten. The current branch is the path that follows the current children. Selecting a message puts it on that path, and the choices below it are kept, so a branch is walked back to as it was left. This is decision 0020.
- A message's content is a `seam.Message`, written and read by the seam's own `EncodeMessage` and `DecodeMessage`. A message is passed in as a struct, and every query names its columns, so P4-03 can add the model and the usage with a new column and a new field.
- Search is SQLite's FTS5 over titles and the words of text blocks, written in the same transaction as the row it indexes. A result names the chat and, for a message, the message.
- A file is tied to a chat by a row in `chat_files`, added when a message refers to it. Deleting a chat removes its rows, its search rows, and each of its files that no other chat holds.
- Settings in a new `settings/chats.json`: the longest title, the most search results, and the length of a search snippet.
- Proof: tests that edit and retry, then walk the branches back and forth; that delete a chat and find no row, search row or file left, with another chat's files untouched; that FTS5 is compiled in; and that two tenants' chats and search results never mix.
- Docs: `docs/architecture/chat.md`, a row in the overview, a line in `verify`'s list of parts, and decision 0020.

### Result

Done on November 24, 2026.

- `internal/chat` holds the record. Chats: `Create`, `Get`, `List`, `Rename`, `Star`, `Delete`. Messages: `Append`, `GetMessage`, `Path`, `Select`. Search: `Search`. Files: `AddFile`, `Files`. Settings: `Limits` and `DefaultLimits`.
- The migration `20261124T213000_chat_record.sql` adds `chats`, `messages`, `chat_files`, `chat_title_search` and `message_search`.
- Proof: `go test ./internal/chat`. Edit and retry make branches, and the branches are walked back and forth with each side as it was left. A deleted chat leaves no row, no search row and no file, and the other chat's files are untouched. FTS5 is compiled in. Two tenants' chats and search results never mix. `verify` is 8 of 8 and `rebuild` is green.
- Decision 0020: messages are a tree, and each parent remembers its current child.
- Settings in `settings/chats.json`: `chats.title_max_length`, `chats.search_max_results`, `chats.search_snippet_words`.
- Left for later items: routes (P2-05), the model, the usage and automatic titles (P4-03), stored setting values (P2-04), summaries and the past-chat search tool (P5-05), files a project also holds (P9-01), a chat's artifacts (P9-03).
- Doc: `docs/architecture/chat.md`. Journal: `docs/journal/2026-11-24-chats-001.md`.

