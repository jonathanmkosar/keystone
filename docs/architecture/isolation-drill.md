# Tenant isolation drill

Folders: `internal/isolationdrill`, `tools/verify/isolationdrill.go`

The drill for fixed rule 1, a tenant never reads another tenant's data (P13-02). A second person, signed in as themselves, holds the first person's real ids and tries every way this build has to reach the first person's chats, files, knowledge, keys and records. Each try must be refused, or answered with nothing of the first person's; the first person's folder, database and audit log must not move; and each try must be in the second person's own audit log.

The standing check `tenant isolation` (`tools/verify/isolation.go`, `routes.go`, `share.go`) already proves that every route refuses no session, a session re-addressed to another tenant and a signed-out one, and that a call is written in its own tenant's log. The drill adds what that check lacks: a valid session of the wrong person, with the right ids.

## How it runs

`isolationdrill.Run()` takes about six seconds:

1. A throwaway data folder with two invented people, "Drill Person One" and "Drill Person Two", each with a local model (`lantern-local` and `drill-local`), the built-in knowledge store, the memory pass on, and the invented Google OAuth client.
2. The real server, as `internal/app` builds it (`app.Open`), in memory. Its Chat Completions adapter is the drill's own model on the seam (`model.go`), which plays steps the drill sets once the ids exist: it answers the memory pass by the shipped prompt, works as the built-in store's compile job, and otherwise plays the directive (`drill:<name>`) in the person's last message. A test MCP server that answers only the first person's token, an open one, and the stand-in Google are served on this machine.
3. The first person is filled through the API, as they would fill it (`first.go`): a profile, two secrets, a connector, a Google sign-in and a second one left unfinished, a chat with an upload, a message naming it, a title, a star, a pin and a share link, its memory pass, a chat with an artifact in two versions attached to the first, a handoff, a style, a project with a file, a paused schedule, a background task to its end, a page and a source in the store, an incognito chat, a Gmail search, a tool policy, and a turn left waiting for an approval. Every piece of content carries a marker word made for the run.
4. The second person gets a few things of their own: a chat with another attached, an upload, a project, an artifact, a connector to an open server, a connector to the first person's server that names the first person's token, and a model whose entry names the first person's key.
5. The three parts run, each between two looks at the first person's folder, database and audit log: tools, jobs and agents, then the session and the API.
6. Last checks: the second person's whole folder, database and vault are searched for marks; both audit logs for tokens and keys; and the sign-in door is tried.

How a leak and a change are found is decision [0491](decisions/0491-the-drill-finds-a-leak-by-marker-words-and-a-move-by-hashes.md): marker words anywhere the second person can see, the first person's long ids in an API answer that did not carry them, and hashes of every file and every row of the first person's folder and database before and after each part. `Report` holds every attempt with its result, every way in (`Reached`), and notes, such as a route that failed where it could have refused (none today).

## What was tried, and how it ended

The run of November 26, 2026, on Windows 11 with Go 1.26: 342 tries, nothing reached, nothing unrecorded. `go test -v ./internal/isolationdrill` prints each try and its result.

### The session and the API (226 tries)

| Way in | Result |
| --- | --- |
| Every route that takes an id, with the first person's: chats and their model, pins, skips, recap, memory, connectors, project, style, attached chats, handoff, select, uploads (list, one, content, delete, add), turns (one, events, stop, inspect), approvals of the waiting turn (list, approve, decline), tasks (list, one, events, stop, start), incognito (flag on, off, close); artifacts (one, compare, a version, restore, download, sandbox page); projects (one, change, delete, files: add, one, mark, delete; chats: new, move in, move out); styles (one, change, delete, default); schedules (one, change, delete, pause, resume, run); share links (the chat's, turning one off) | Refused, 404 `not_found`, each one |
| The first person's id in a body: memory forget and correct with their source, Google finish with their pending state | Refused: 404, and 400 `bad_state` |
| Routes that act on the caller's own records, given the first person's names: a secret, a setting, a tool policy, a model and a connector of the same name, a project, a style, a schedule, a profile, Google start and disconnect, the embeddings refresh | Answered with the second person's own records; the first person's unchanged |
| Lists and reports with the first person's tenant id in the query (`?tenant=`) and their request id: chats, search, artifacts, projects, styles, schedules, connectors, models, secrets, settings, memory, usage, operations, watch, agents, wiki jobs, tools, the audit log, embeddings, the session | Answered with the second person's own; none holds a mark or the first person's id |
| The second person's own chat or project with the first person's turn, waiting turn and approval, task, upload, attached chat, pinned message, page, connector name, project file and chat under it | Refused, 404; two removals of something the chat never had answered 200 and removed nothing |
| A turn in the second person's own chat that edits or retries the first person's message, names their upload or project file (also by a path that climbs out), or asks for their model; choosing their message or style | Refused, 404 or 400; the turn on their model ended failed, "no model named lantern-local" |
| Path tricks: `..` raw (redirected, 307, nothing served) and encoded (`%2E%2E%2F`, `%2F`, `%5C`), the first person's tenant id as a chat id, their upload's hash in the second person's chat and in a route under the first person's tenant id, its hash upper-cased, their project file's hash in the second person's project, an artifact id and a version that climb into the first person's artifacts folder | Refused, 404, each one |
| Uploads and project files named to climb into the first person's folder (`../../<id>/files/<hash>.txt`, `..\..\<id>\secrets.bin.txt`, `/<id>/keystone.db.txt`) | Stored by hash in the second person's own folder, the name kept as words only; the first person's folder unchanged |
| Forged and guessed chat ids: upper case, a trailing space, a NUL, `x' OR '1'='1`, a random id, `%`, `*`; searches with the first person's words and with the index's own operators | Refused, 404; searches found nothing of theirs |
| Cookie and session swaps: the second person's token re-addressed to the first person; two session cookies in both orders; the token in an `Authorization` header and under another cookie name; the first person named in `X-Tenant`, the query and `X-Request-Id` | 401 for every token that is not the second person's own; with theirs first, 404 as the second person; the session route says the caller is the second person |
| A model whose `ca_cert` names a file in the first person's folder (absolute and relative), their `secrets.bin`, or a share on another host | Refused, 400 `bad_model`, since decision [0490](decisions/0490-a-ca-cert-path-is-set-from-the-command-line-only.md); before it, taken (finding 1) |
| The share doors (P10-04), with no session and with the second person's: their own link; their token re-addressed to the first person; a guessed token for the first person; a token with a path; their link and a re-addressed one asked for the first person's upload as an image, also by a path | Their own link shows their own chat, recorded in their log under the link; every other try 404, in no person's log (the server's log has it); no cookie set |
| Signing in as the first person with the second person's password and session cookie, and with a guessed password | Refused, 401, no cookie; recorded as two refused sign-ins in the first person's log and their wrong-password count, and nothing else, as decision [0492](decisions/0492-a-sign-in-for-a-person-is-in-their-log-whoever-sent-it.md) says |

Not tried with ids, because they take none and act only on the caller's own: signing out, the pause and resume switches for agents and schedules. The wiki jobs' routes take a job's name, which is the install's; the jobs part runs one.

### Tools (37 tries)

A turn of the second person's whose model calls, in its first step, `search_chats` with the first person's title and words, `read_chat` with four of their chats and a path, `open_attached_chat` with two of their chats, `knowledge_search` (also in their space by name), `knowledge_read_page` and `knowledge_read_source` with their page, source and memory source, `read_artifact` by their artifact's id, title and version, `update_artifact` by id and by title, `load_tools` with their connector's tool names, and Gmail and Calendar; and in its second, their connector's five tools by name and the second person's own connector's.

| Way in | Result |
| --- | --- |
| Reads of the first person's chats, attached chats, artifacts | Refused: "no chat with the id", "not attached to this one", "no such artifact in this chat" |
| Knowledge lookups | Nothing found; `s1` read the second person's own source (both stores count from 1) |
| `update_artifact` by id and by title | Refused, nothing changed |
| The first person's connector tools, loaded or called by name | Not offered: `load_tools` loaded only the second person's own five; each call "There is no tool named" |
| The second person's connector to the first person's server, naming the first person's token | Added with no tools: "its token, stored as connector.lantern-notes, is missing"; no call to either server carried the first person's token |
| Gmail and Calendar, for a person with no Google sign-in | Not offered; the stand-in Google took no call |
| A turn on a model whose entry names the first person's key, and on the first person's model by name | Failed: "needs a key that is not in this person's vault", "no model named lantern-local"; the model was never opened with the first person's key |

Every call is in the second person's log as a tool call under the turn's request id, with its input.

### Jobs and agents (70 tries)

| Way in | Result |
| --- | --- |
| A background task of the second person's, whose rounds call the same tools | The same refusals, in the task's own chat; each call in the second person's log under the task's request id |
| A scheduled run, run now, doing the same | The same; its search found only the second person's own task chat |
| The built-in store's wiki compile job, reading the first person's sources and page by id, then answering with a page at their path citing their source | Reads found nothing of theirs; Keystone refused the answer ("cites source s2, which this job may not cite") and wrote nothing |
| The memory pass, with the second person's memory space named as the first person's | One fact kept in the second person's own store, in a space of that name; the first person's store unchanged |

Sub-agents (P8-02) were not merged when the drill was written; they add their tries here when they land.

### Files on disk and the audit log

| Way in | Result |
| --- | --- |
| The first person's folder (5 files, hashed) and database (89 tables, row by row) before and after each part | Unchanged, after every part |
| The first person's audit log before and after each part | No entry added |
| The second person's every file, table and secret, searched for the first person's marks | None holds one |
| Both audit logs, searched for both session tokens, the first person's key, connector token and Google refresh token, and both share tokens | None holds one |

## Findings

1. **A model's `ca_cert` path through the API (medium, fixed).** Any person could make Keystone read any path the server's account can reach: another person's files, a device, a share on another host that Windows signs in to as Keystone's account. The content did not come back (the turn says only that the entry is refused), but the read happened. The API now takes PEM text only; a path is set from the command line (decision 0490, `internal/settingsapi/cacert.go`, `TestACACertPathIsRefusedThroughTheAPI`).
2. **A connector or a model marked local may name any address on this machine or the home network (low, not fixed; owner question).** Every person on an install shares that network. A server there that asks for no token, such as one person's notes server on `127.0.0.1`, can be added by another person as their own connector. Keystone sends each person only their own token, which the drill proves, so a server that asks for one is safe.
3. **The audit log records a call's method and path, never its body or query (low, by design).** A try that carries the first person's id in a body or query string is in the second person's log by its route, status and outcome, not by the id. Bodies are left out because passwords and keys travel there (decision 0009).
4. **A sign-in for a person is in their log and counts toward their wait, whoever sent it (low, by design; known).** Decision 0492.

## Settings

None. The drill's waits are constants in the code, as the other checks' are.

## Checked by

- `go test ./internal/isolationdrill`: `TestTheSecondPersonReachesNothing` runs the drill and fails on any way in, or on a part that tried nothing.
- The standing check `isolation drill` in `verify` runs it again.
- `go test ./internal/settingsapi`: `TestACACertPathIsRefusedThroughTheAPI`.

## Adding to it

A route is tried by the walk with no new line, as long as the names of its path's parts are in `pathValue`; a part with a new name fails the drill until it is added there. A body the route reads goes in `bodyFor`. A tool goes in `battery`. A job goes in `jobs.go`. A table that a server-wide timer writes goes in `unwatched`, with its reason.
