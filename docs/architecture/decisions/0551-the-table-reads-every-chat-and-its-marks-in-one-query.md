# 0551. The table reads every chat and its marks in one query

November 26, 2026 · decided in P4-06

## The question

A row of the table wears marks that seven other parts keep: the project (`internal/projects`), a task's chat (`internal/jobs`), a sub-agent's (`internal/subagents`), open share links (`internal/share`), the import's mark (`internal/imported`), the hold for sensitive content (`internal/knowledge/recall`) and the incognito flag (`internal/knowledge/memory`). The table is filtered by some of them and sorted by the count of messages, and it must say how many chats match in all. Where is that worked out, and who reads those tables?

## The decision

- **One query** in `internal/turn/manage.go` (`tableLines`) reads every chat of the tenant with a count of its messages and one `EXISTS` for each mark, straight from those parts' tables. It reads and never writes them, and the file names each table with the part that owns it.
- **The filters, the search's narrowing, the order, the total and the page are worked out in Go**, on that list.
- **Only the rows of the page** are then given what costs a read each: the recap's summary (`recap.Summaries`), the import's own mark with its details (`imported.Marks`), and the search's excerpt (`chat.Excerpts`), through those parts' own functions.

## Why

- A filter and an order need the mark and the count for every chat, not for the fifty on the page, so they cannot be asked of each part's own function one chat at a time: that is seven calls for each of several hundred chats, on a store with one connection.
- Each part's functions answer for one chat or for a list of ids. None answers "which chats", and adding seven such functions would touch seven parts that other lanes are working in, for one caller.
- The turn package already reads the `jobs` table this way, for the same reason, where an incognito chat waits for its task (`taskChats`, `taskWorking`).
- Sorting by how well a chat matches the search is an order the full-text index gives, not a column: it is known in Go, so the order is worked out there for every kind of sort, in one place that a test can call.
- The owner has tens of chats and will have hundreds. One indexed read for each mark of each chat is a few milliseconds at that size.

## What it costs

- `manage.go` knows the names of seven tables and a column or two of each. A part that renames one breaks the table's query, and the table's test, which makes every mark, fails. The file's comment lists them.
- Every page reads every chat's marks again. Past a few thousand chats the filters and the order belong in the query, with a count of messages kept on the chat's row. `docs/architecture/manage-chats.md` says so under "Not here yet".
- What "open" means for a share link (not turned off, not expired) is written a second time here. A link refused for another reason, such as a chat that became held, still counts as shared until it is turned off.
