# Managing many chats

Files: `internal/turn/manage.go` (the two routes), `internal/chat/bychat.go` (the search by chat), `web/app/src/ManageChats.tsx`, `manageCalls.ts` and `manage-chats.css` (the Chats screen), `tools/verify/managechats.go` (the standing check).

The Chats screen is a table of every chat a person has: its title, a line of its summary, how many messages it holds, when it was last active, and the small marks it wears. A box at the top searches titles and message text by keyword. Chats are selected with a checkbox, one, some, all shown or all that match, and the selected are deleted in one step, after a confirmation. It was asked for by the owner on November 26, 2026 (P4-06), who mostly uses Keystone on a phone and had just had 33 chats imported.

Two routes serve it, in the shell like every chat route ([turns.md](turns.md)): `GET /chats/table` and `POST /chats/delete`. Nothing new is stored: there is no migration.

Decisions: [0550](decisions/0550-the-table-searches-by-chat-with-a-route-of-its-own.md) for a table route beside the list and the search, [0551](decisions/0551-the-table-reads-every-chat-and-its-marks-in-one-query.md) for reading every chat's marks in one query, [0552](decisions/0552-a-batch-delete-skips-a-chat-that-is-at-work.md) for skipping a chat that is at work, [0553](decisions/0553-each-chat-of-a-batch-is-its-own-audit-entry.md) for the audit log, and [0554](decisions/0554-a-deleted-chat-is-gone-and-the-screen-says-so.md) for no undo.

## The table

`GET /chats/table` answers with a `TableReply`.

| Query | Meaning |
| --- | --- |
| `q` | The words to look for. See "The search". |
| `sort` | `active` (the last message; the default), `title`, `messages`, or `match` (the best match first; the default while searching, and read as `active` when there is no search). |
| `order` | `asc` or `desc`. The default is `desc` for `active` and `messages`, `asc` for `title` and `match`. |
| `filter` | Marks a chat must wear, separated by commas: `starred`, `imported`, `project`, `task`, `subagent`. A chat must wear every one named. |
| `limit`, `offset` | A page. `chats.table_page_rows` is the most rows and the default. |
| `ids` | `true` also returns the ids of every chat that matches, in the table's order, at most `chats.table_select_max`, with `ids_cut` when there were more. It is what "select all that match" reads. |

| Field of the reply | Holds |
| --- | --- |
| `rows` | One page, each a `TableRow`. |
| `total` | How many chats match the search and the filters. |
| `more` | There are rows after these. |
| `query`, `sort`, `order`, `filters` | What the table was made with, as the server read it. |
| `page_rows`, `delete_max` | The two settings, so the screen needs no second call. |
| `ids`, `ids_cut` | When asked for. |

| Field of a row | Holds |
| --- | --- |
| `id`, `title`, `created`, `active` | The chat's own. A title is empty until the chat has one. |
| `summary` | The summary of its recap ([chat-summaries.md](chat-summaries.md)), absent until it has one. |
| `messages` | How many messages it holds, on every branch, tool results included. It is what "size" sorts by. |
| `busy` | A turn is queued or running in it. |
| `working` | A background task or a sub-agent that has not ended works in it or was started from it. |
| `starred` | Its star. |
| `imported` | The mark of a chat brought in from another app ([import.md](import.md)). |
| `project` | The `id` and `name` of the project it is in ([projects.md](projects.md)). |
| `task` | A background task or a scheduled run worked in it ([jobs.md](jobs.md), [schedules.md](schedules.md)). |
| `subagent` | A sub-agent worked in it ([sub-agents.md](sub-agents.md)). |
| `held` | It was sent content from a sensitive space, so only a local or trusted model answers in it ([knowledge-recall.md](knowledge-recall.md)). |
| `shared` | How many share links to it are open ([share.md](share.md)). |
| `incognito` | No memory pass reads it ([memory-screens.md](memory-screens.md)). |
| `match` | While searching: what was found in it. |

A chat with no title sorts after every chat with one, whichever way titles run. Rows that are equal by the order asked for stay newest first.

**How it is read.** One query reads every chat of the tenant with its marks (`tableLines`): the chat's row, a count of its messages, and one `EXISTS` for each mark, against the tables of the parts that keep them: `project_chats` and `projects`, `jobs`, `subagents`, `share_links`, `imported_chats`, `turn_recall` and `memory_chats`. It changes nothing in them. The filters, the search's narrowing, the order and the page are then worked out in Go (`narrowTable`, `sortTable`), and only the rows of the page are given what costs a read each: the summary, the import's own mark, and the search's excerpt (`fillTable`). A summary or a mark that cannot be read is left out and logged; the table is not failed for it. Decision 0551 says why, and what it costs past a few thousand chats.

The table shows a person their own chats, so a held chat is listed like any other, with its summary: fixed rule 3 is about what reaches a model, and no model is called here.

## The search

The search is the chat record's own ([chat.md](chat.md)): the same two full-text tables and the same reading of a query as `GET /chats/search`.

- Every word must be in the chat's title, or in one of its messages on any branch. A word also matches as the start of a longer one. Capitals and accents do not matter.
- Each word is quoted before it reaches the index, so nothing typed is read as search syntax. Words with no letter or digit match nothing.
- Thinking, tool calls, tool results and file names are not indexed, and neither is a chat's summary.

`chat.Search` returns hits, at most `chats.search_max_results` of them: several may be in one chat, and a chat past the limit is not found. A table needs the chats, so `internal/chat/bychat.go` adds two functions and changes none (decision 0550):

| Function | Does |
| --- | --- |
| `SearchByChat(store, query)` | Every chat that matches, once each, as a `Found`: whether its title matched, how many of its messages did, and the best of them. The chats whose title matched come first, by the title's rank and then the newest; then the rest, by their best message's rank. That order is `sort=match`. |
| `Excerpts(store, limits, query, found)` | For the chats of one page: the title cut into `Part`s with the matching words marked, and about `chats.search_snippet_words` words around the match in the best message, marked the same way, its line breaks read as spaces. |

A row's `match` is `title` and `title_parts`, `messages` (how many matched), and the best message's `message_id`, `role` and `snippet`. A part is `{"text": "...", "match": true}`; the screen draws a marked part as a highlight and never reads markup from the server.

## Deleting many

`POST /chats/delete`, body `{"ids": ["...", "..."]}`, answers 200 with a `DeleteManyReply`:

    {"deleted": ["<id>", ...], "skipped": [{"id": "<id>", "title": "...", "reason": "turn_running", "why": "A reply is still being written in it, ..."}]}

Each chat is deleted by `chat.Delete`, in its own transaction, exactly as `DELETE /chats/{id}` deletes it. Nothing new goes and nothing new is kept:

| Goes with a chat | Stays |
| --- | --- |
| Its messages on every branch, its turns and their events, its pins, its recap, its search rows | A file a project holds, even when the chat held it too |
| Its uploads' rows, and each file in `files/` that no other chat and no project holds | A file another chat holds |
| Its artifacts: the rows and its folder in `artifacts/` | The facts the memory pass kept from it, until they are forgotten on the Memory screen ([memory-screens.md](memory-screens.md)) |
| Its share links, which stop opening at once | Its calls in the audit log and its usage records |
| Its place in a project, its attachments to other chats, its style, the tasks and sub-agents started from it | The chats those tasks and sub-agents worked in, which are chats of their own and can be found with the `task` and `subagent` filters |

**A chat that is at work is skipped, not stopped** (decision 0552). `removeIdle` takes the chat's queue, as the goroutine that works its turns does, and leaves the chat when:

| `reason` | Why | Status in the audit log |
| --- | --- | --- |
| `turn_running` | A turn is queued or running in it, a turn waiting for an approval included | 409 |
| `task_running` | A background task or a sub-agent that has not ended (`queued` or `running`) works in it or was started from it | 409 |
| `not_found` | No chat of this person's has the id. Another person's id is this: a store holds one tenant's chats, so theirs cannot be named | 404 |
| `failed` | The delete failed, or could not be recorded first; the reason is in the server's log under the call's request id | 500 |

`why` says the same in words, for the screen. While the queue is held no turn can start in the chat, so a message sent in the same moment either goes with the chat or finds it gone. This is unlike `DELETE /chats/{id}`, which stops a running turn first: one chat a person chose to delete from its own menu is one thing, and a chat swept up in "select all" while it is answering is another.

**What is refused whole**, with nothing deleted: a body with no ids, an id that is not a short run of letters, digits, hyphens and underscores (400 `bad_request`), and more ids than `chats.delete_batch_max` (400 `too_many`). An id said twice is taken once.

**The audit log** (decision 0553). The call has its own two entries, as every API call has, and those hold no body. So each chat of the batch is an entry too, under the call's request id, with the action its own delete would have, `DELETE /api/v1/chats/<id>`: `started` before it is deleted and `ok` after, or one `refused` entry with its reason for a chat that was left. A delete that cannot be recorded first is not made.

**There is no undo** (decision 0554). A deleted chat is gone, and the screen's confirmation says so.

## The screen

`/chats`, in `web/app/src/ManageChats.tsx`, provisional on the mockups, which draw no such screen; it is built from the tokens. `pages.tsx` gives `Chats` from it.

| Part | What it does |
| --- | --- |
| The search box | Searches titles and messages a quarter of a second after the last key. A new search is ordered by best match. |
| The filters | Five chips: Starred, Imported, In a project, Tasks, Sub-agents. Any that are on must all hold. |
| The table | A checkbox, the title with its marks, then a line: what the search found ("You:" or "Reply:" and the snippet, the matching words highlighted, and how many more messages matched) or the chat's summary. On a wide screen, columns for messages and last activity, and the three headings sort. |
| Paging | The next page is read as the end of the list comes into view, and by a "Show more" button. |
| Selecting | A checkbox on each row; "select all shown" in the heading; and, once all shown are selected and there are more, "Select all N that match", which reads the ids from the server and says how many it took. A new search or filter starts with nothing selected. |
| The bar | While anything is selected, a bar at the foot of what can be seen: the count, Clear, and Delete. |
| The confirmation | Names how many chats, what goes with them (messages on every branch, uploaded files, artifacts, share links), what stays (a file a project holds, what Keystone remembered), that a chat still at work is left and named afterwards, and that there is no undo and no trash. |
| The delete | One request for up to `delete_max` chats; a larger selection goes as several, one after another, with the count so far. Afterwards a note says how many were deleted and names each chat that was left, with its reason; those stay selected. The table is read again, and so is the sidebar's list. |

**On a phone** (under 760 pixels):

- A row is the checkbox and, beside it, the title, the marks, the line, and "N messages · date". Nothing scrolls sideways; a long title is cut with an ellipsis and a long project name inside its mark.
- The checkbox sits in a square 44 pixels wide, and the rest of the row opens the chat.
- The heading row is "Select all shown", and sorting is a native select with a button that turns the order round.
- No box takes the focus by itself, so the keyboard comes up only when the person taps the search box. The focus of the confirmation goes to its Keep button.
- The search box and the select are 16px, so the browser does not zoom the page in.
- The confirmation covers the visible part of the screen, which the keyboard moves: `useSeen` keeps `--seen-top` and `--seen-height` from `window.visualViewport`, as the attach picker does.

## Settings

In `settings/chats.json`, each a person's own (`tenant`).

| Key | Default | Meaning |
| --- | --- | --- |
| `chats.table_page_rows` | 50 | Rows a page, from 1 to 500. |
| `chats.table_select_max` | 2000 | The most ids "select all that match" is given at once, from 1 to 100000. |
| `chats.delete_batch_max` | 100 | The most chats one request deletes, from 1 to 1000. |

A stored value that does not read as a whole number within its bounds is not used: the shipped default is, with a line in the log. The table also reads `chats.search_snippet_words` for a snippet's length.

## In shared code

Every existing line of shared code this item changed:

- `web/app/src/pages.tsx`: the old `Chats`, `Row` and `when` are gone, with three imports, and `export { Chats } from "./ManageChats";` stands in their place. `Soon` and `Missing` are as they were. `App.tsx` is untouched.

Added lines only:

- `internal/app/app.go`: `all = append(all, turn.ManageRoutes(runner)...)` with its comment, after the incognito routes.
- `internal/isolationdrill/api.go`: in `bodyFor`, the body of `POST /api/v1/chats/delete` with four of the first person's chats; in `queryFor`, the table searched with the first person's words and asked for ids.
- `tools/verify/main.go`: `{"manage chats", checkManageChats},` after the import check.
- `settings/chats.json`: the three settings above, before `chats.context_pinned_heading`.
- `docs/api/openapi.json`: regenerated.

New files: `internal/turn/manage.go` and its test, `internal/chat/bychat.go` and its test, `tools/verify/managechats.go`, and the screen's three files. `chat.Search`, `chat.Delete`, `GET /chats`, `GET /chats/search` and `DELETE /chats/{id}` are unchanged.

## Checked by

- `go test ./internal/chat`: `SearchByChat` finds a chat by a word in a message, once however many of its messages and branches hold the word, with the title and the messages counted; title matches come first; the start of a word matches; two words of two different messages do not; anything typed is safe; a deleted chat is found no more; one tenant's chats only. `Excerpts` marks the word in the title and the snippet and nothing else. `parts` cuts marked text, and a mark with no partner does no harm.
- `go test ./internal/turn`, through the routes:
  - **The table**: every chat with each of its marks (star, project, import, task, sub-agent, hold, share link, incognito); **a search finds a chat by a word in a message, with the word marked**; a title match first; a search with a filter; each filter and two together; each order both ways, with untitled chats last; pages, `more`, `total` and the ids; the two settings; every refusal; and another person's table and search hold none of it.
  - **The delete**: **the selected chats and nothing else are deleted, with their files**: a chat's own file and its artifacts folder go, a file a kept chat holds and a file a project holds stay, the share link and every row go, and the chats not named are as they were. **Another person's chat id in the batch is refused, and nothing of theirs changes**: their chat, its message, its file, and their audit log. Each chat is in the caller's audit log in order. Every refusal of a body, more than the setting allows, a second delete of the same chats, and a delete with no session.
  - **A chat at work is skipped**: with a turn in the middle of its model call, and with a task or a sub-agent not ended in the chat or started from it; the turn still runs afterwards and nothing was stopped; the skip is in the audit log; and each is deleted once its work has ended.
- `go test ./internal/isolationdrill`: the walk tries both routes with the first person's ids and words, and the first person's folder, database and audit log do not move.
- `verify`, the standing check **`manage chats`** ([tools.md](tools.md)), on the real server with two invented people and a model that writes half a reply and waits: the search by a message's word; more than `chats.delete_batch_max` refused whole; one call that deletes two chats with their uploads, leaves a file a kept chat holds, refuses the other person's chat and skips the chat whose turn is running; nothing of the other person's changed; each chat in the audit log. It also reads the screen's source: no box takes the focus by itself, the text boxes are 16px, the confirmation is placed by the visible viewport and says there is no undo; and the three settings are in the registry.

Two faults were put in by hand and each was caught by the tests and by the check, then taken out: a chat at work deleted like any other, and a search that narrows nothing.

## Not here yet

- **An undo or a trash.** A deleted chat is gone (decision 0554).
- **Search by meaning**, and search of summaries. The search matches words in titles and messages.
- **Opening a result at its message.** A row opens the chat on its current branch; `message_id` is given, and `POST /chats/{id}/select` opens a chat there, but the screen does not use it yet.
- **Other things to do with a selection**: star, move to a project, export.
- **Very many chats.** Every chat's marks are read for each page (decision 0551). That is quick for hundreds and for a few thousand; past that the filters and the order belong in the query.
- **A chat's files by size.** "Size" is the number of messages.
