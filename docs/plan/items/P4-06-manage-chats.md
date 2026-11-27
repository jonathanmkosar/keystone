# P4-06. Manage chats

Status: provisional
Lane: chats + interface
Depends on: P4-03
Owner gate: none
Scope: Asked for by the owner on November 26, 2026. The Chats screen becomes a table of every chat, with keyword search over titles and message text, sorting, select one, some or all, and delete the selected in one step.
Proof: A search finds a chat by a word in a message; the selected chats and nothing else are deleted, with their files, and the deletion asks first.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan (November 26, 2026)

Branch `chats/p4-06-manage-chats`, cut from `reboot` at f433894. No owner gate. Built in steps that each end with a green `verify`, a commit and a push.

**The contract, written first.** Two routes, in a new file `internal/turn/manage.go`, listed in `internal/app` by one added line:

- `GET /chats/table`: every chat as a row of the table. Query: `q` (the words to look for), `sort` (`active`, `title`, `messages`, or `match` while searching), `order` (`asc` or `desc`), `filter` (any of `imported`, `starred`, `project`, `task`, `subagent`, separated by commas; a chat must wear every one named), `limit`, `offset`, and `ids=true` for the ids of every chat that matches, for "select all that match". The reply: `rows`, `total` (how many chats match), `more`, `page_rows` and `delete_max` (the two settings, so the screen needs no second call), and `ids` when asked.
  - A row: `id`, `title`, `summary` (its recap's, when it has one), `created`, `active`, `messages` (every message on every branch), `busy`, and the marks: `starred`, `imported` (the import's own mark), `project` (its id and name), `task`, `subagent`, `held`, `shared` (open links), `incognito`. While searching, `match`: whether the title matched, how many messages did, and the best message's id, role and a snippet cut into parts, the matching words marked.
  - The search is the chat record's own: the same two full-text tables and the same reading of a query as `GET /chats/search` (every word in one title or one message, a word also as the start of a longer one). That route returns at most 50 hits, not chats, so it cannot count the chats that match, page them, or be joined with a filter. A new function in a new file of `internal/chat` returns the matches by chat; `chat.Search` and its route stay as they are.
- `POST /chats/delete`: body `{"ids": [...]}`, at most `chats.delete_batch_max`. Each chat is deleted by `chat.Delete`, in its own transaction, with its queue held so no turn can start in between. The reply names what was deleted and what was skipped and why: `turn_running`, `task_running` (a task or a sub-agent that has not ended works in it or for it), `not_found` (no such chat of this person's, which is what another person's id is), `failed`. Nothing is stopped: a chat that is at work is left. Each chat is one entry in the audit log, under the call's request id, as its own `DELETE /api/v1/chats/<id>` would be.

**Settings**, in `settings/chats.json`, added lines only: `chats.table_page_rows` (rows a page), `chats.table_select_max` (the most ids "select all that match" returns), `chats.delete_batch_max` (the most chats one request deletes).

**The screen**, in new files `web/app/src/ManageChats.tsx`, `manageCalls.ts` and `manage-chats.css`; `pages.tsx` keeps `Soon` and `Missing` and gives `Chats` from the new file. A search box at the top, filter chips, a sort control, the table (a checkbox, the title with its marks, a line of summary or the snippet, messages, last activity), "Load more" and loading as the end of the list comes into view, select all shown, select all that match with the count, and Delete selected with a confirmation that names how many, what goes with them and what stays, and that there is no undo. Under 760 pixels a row is two lines with no sideways scrolling, the bar with the count and Delete sticks to the bottom of what can be seen, no box takes the focus by itself, text boxes are 16px, and the confirmation is placed in the visible part of the screen.

**The proof**, as tests in `internal/chat` and `internal/turn` and a standing check `manage chats` in `verify` on the real server: a search finds a chat by a word in a message, with the word marked; the selected chats and nothing else are deleted, with their files, artifacts and links, while a file a project holds stays; another person's chat ids in the batch are refused and nothing of theirs changes; a chat with a turn running is skipped and still there; one request over the limit is refused. The isolation drill gets the new route's body with the first person's chat ids.

**Order.** 1. This plan. 2. The search by chat and the table route, with tests. 3. The batch delete, with tests, the drill's line and the standing check. 4. The screen, seen in a browser at phone and desktop widths, light and dark. 5. The write-up: `docs/architecture/manage-chats.md`, decisions from 0550, the journal, status.

**Not in this item.** An undo or a trash: a deleted chat is gone, and the confirmation says so. Search by meaning. Bulk star, move to a project or export.

### Result (November 27, 2026)

Provisional on the owner's eye: built and proven, and seen in a browser at a phone's width and a desktop's, light and dark, but the mockups draw no such screen and no real phone has held it.

- **Built as planned**: `GET /chats/table` and `POST /chats/delete` in `internal/turn/manage.go`; the search by chat in `internal/chat/bychat.go`, beside `chat.Search`, which is unchanged; the Chats screen in `web/app/src/ManageChats.tsx`, `manageCalls.ts` and `manage-chats.css`; three settings; no migration. The component doc is `docs/architecture/manage-chats.md`, with decisions 0550 to 0554.
- **The proof passes**, as tests (`go test ./internal/chat ./internal/turn ./internal/isolationdrill`) and as the standing check `manage chats`, on the real server: a search finds a chat by a word in a message, with the word marked; the selected chats and nothing else are deleted, with their files, artifacts and share links, while a file a kept chat or a project holds stays; another person's chat id in the batch is refused and nothing of theirs changes; a chat with a turn, a task or a sub-agent still at work is skipped, named, and still there; and the deletion asks first, saying there is no undo.
- **Changed from the plan**: the confirmation's text and the note afterwards were settled in the browser; a title takes two lines on a phone, so a word found in it shows; a row's checkbox carries its name itself, after the hidden words beside it were found to lengthen the page.
- **Left**: the owner's look on his phone. An undo or a trash, opening a result at its message, other things to do with a selection, and the search of summaries are in the doc's "Not here yet".
