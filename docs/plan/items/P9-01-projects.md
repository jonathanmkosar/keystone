# P9-01. Projects

Status: doing (chats)
Lane: chats + interface
Depends on: P5-01, P6-01
Owner gate: Approval of the workspace mockups (P1-06), for the screens only.
Built without the gate: The server side and its tests; the screen built from the draft mockup with design tokens, marked provisional until the mockup is approved.
Scope: Instructions, knowledge files, project-scoped chats and memory.
Proof: A project chat receives the project's instructions and files; other chats do not.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Branch `chats/p9-01-projects`, cut from `reboot` at c268a41. The owner gate holds the screens only.

### Plan

`internal/project` is the scripts' helper package, so Keystone's projects get a package of their own, `internal/projects`. What is built, in steps that each end with `verify` green:

1. **The record.** Migration `20261125T214700_projects.sql`: `projects` (id, name, instructions, the knowledge space its chats' facts go to, archived, created, changed), `project_files` (one row for each file of a project: its SHA-256, which is its name in the tenant's `files/` folder and its id, its name, media type, size, the text read from it, and a sensitive mark; the rows go with the project) and `project_chats` (one row for each chat in a project; it goes with either, so deleting a project leaves its chats as ordinary chats). `internal/projects` owns the tables: create, rename, change the instructions, archive and bring back, delete; add, list, mark and remove files; start a chat in a project, move a chat into one or out. It reads no setting and does not import the chat record (which reads settings), so it can be a guard of fixed rules 1 and 3 in `fixed rules`: every function takes one tenant's store, and every row refers to that store's own chats.
2. **Files behind two seams.** `projects.Blobs` keeps the bytes in `files/` under their hash, written whole through the store's `os.Root`, and removes a file only when no project file and no chat (`chat_files`) names it; `chat.Delete` gains one condition so it never removes a file a project holds. `projects.Extract` reads text from text formats (plain text, Markdown, code, CSV, JSON, by their ending, UTF-8 with no zero bytes). The uploads lane (P9-02) builds its own store and extraction at the same time; both are named and shaped like theirs (the file's SHA-256 in lower-case hex, the raw body with `?name=`), so the integrator can make the two share one store. A decision record says so.
3. **The routes** (`internal/turn/projects.go`, listed in `internal/app`): `GET` and `POST /projects`, `GET`, `PATCH` and `DELETE /projects/{id}`, `POST /projects/{id}/files` (the body is the file), `GET`, `PATCH` (sensitive) and `DELETE /projects/{id}/files/{file}`, `POST /projects/{id}/chats` (a new chat in the project), `PUT` and `DELETE /projects/{id}/chats/{chat}` (move a chat in or out), and `GET /chats/{id}/project`. Another tenant's project or chat is `not_found`. The OpenAPI document is regenerated.
4. **Into each call.** `Runner.build` puts a project chat's instructions in the existing `project` slot of the standing start, under a heading, so a provider caches them with the rest of the start, and its files as items of the `attached` part, after the attached chats, each cut to `projects.file_context_max_chars`, within `chats.context_attached_percent`. Other chats carry neither. Fixed rule 3: a file marked sensitive reaches a call only when every model of its route may read sensitive content, and the chat is held first (`recall.Hold`); otherwise it is left out and a pinned line says how many, with no name. As for attached chats, a project with a sensitive file holds a chat's route when the chat's own model may read it (one added line in `Runner.sensitive`).
5. **Project memory.** The knowledge store interface has spaces and no tags, so a project's memory is a space: each project names one (from `projects.memory_space`, `project-{slug}` by default, kept when the project is renamed). The memory pass writes a project chat's facts there instead of `knowledge.memory_space` (one added line in `memoryPass`); a held chat's facts still go to the confirmed sensitive space or nowhere, whatever the project. Recall in a project chat searches the project's space first and then everywhere, the project's hits first (a store wrapper in `internal/projects`, one changed line in `searchKnowledge`), with every label passed through untouched, so rule 3 is recall's as before. A decision record says why.
6. **The proof**, with the fake provider (`internal/turn/projects_test.go`): a project chat's call carries the instructions in the system text and each file's text (checked in the request the audit log holds and in the context record); a chat outside the project carries neither; a chat moved in starts carrying them and moved out stops; another tenant's project is unreachable on every route and its chats cannot be moved into it; a sensitive file is held back from a hosted model and given to a local one, holding the chat; the memory pass writes a project chat's facts to the project's space and recall prefers it. A `projects` check in `verify`, on the real server, adding lines only.
7. **The screens**, provisional on the workspace mockups (P1-06): projects in the sidebar, `/projects` (the list, a new project, archived ones apart), `/projects/{id}` (name, instructions, files with their sensitive mark, the project's chats, and a composer that starts a chat in the project), the project's name in a chat's header, and "Move to project" in the chat's menu. Built from the tokens in `design/README.md`.

Settings, in a new `settings/projects.json`: the most projects, the longest name and instructions, files per project, file size, the text endings, how much of each file goes into a call, the headings and the cut note, the memory space's pattern, and whether recall prefers the project's space.

Left for later: PDF and other documents (P9-02's extraction, once the stores are one); a file larger than `server.max_body_bytes` until P9-02's per-route body limit is merged; project memory for the knowledge lookups the model calls itself; tags in the knowledge store interface.

