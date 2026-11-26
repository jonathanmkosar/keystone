# Projects

Folders: `internal/projects` (the record, the files, the rules), `internal/turn/projects.go` (the routes, and what each call carries), `web/app/src` (the screens). Built in P9-01.

A project is a name, instructions, knowledge files and the chats in it. Every call of a chat in a project carries the project's instructions at the start, where a provider caches them, and its files with the attachments; a chat outside carries neither. A project's chats keep their facts in a space of the knowledge store named for it, and recall in them searches that space first.

Not to be confused with `internal/project`, the scripts' helpers ([project.md](project.md)).

Decisions: [0400](decisions/0400-a-projects-memory-is-a-space-of-the-knowledge-store.md) for project memory, [0401](decisions/0401-project-files-are-kept-by-their-hash-behind-a-seam-the-uploads-can-share.md) for the files and the seam the uploads can share, [0402](decisions/0402-deleting-a-project-keeps-its-chats.md) for what deleting a project does, and [0403](decisions/0403-a-sensitive-project-file-is-left-out-or-holds-the-route-as-an-attached-chat-does.md) for files marked sensitive.

## The record

Migration `20261125T214700_projects.sql`, three tables `internal/projects` owns:

| Table | One row for | Columns |
| --- | --- | --- |
| `projects` | each project | `id`, `name`, `instructions`, `memory_space`, `archived_at` (set while archived), `created_at`, `updated_at` |
| `project_files` | each knowledge file of a project | `project_id` (goes with the project), `hash` (the file's SHA-256 in lower-case hex: its id and its name in `files/`), `name`, `media_type`, `size`, `text` (read once, when it is added), `sensitive`, `added_at`. The same file twice in one project is one row. |
| `project_chats` | each chat in a project | `chat_id` (the key: a chat is in one project or none), `project_id`, `added_at`. Both refer to this tenant's own rows with `ON DELETE CASCADE`. |

The package's functions, each on one tenant's store:

| Function | Does |
| --- | --- |
| `Create(ctx, store, options, name, instructions)` | A project. The name is put on one line, 1 to `projects.name_max_chars` characters; the instructions keep their lines, up to `projects.instructions_max_chars`. Its memory space is named from `projects.memory_space` (`SpaceFor`). Past `projects.max_projects`, `ErrFull`. |
| `Get`, `List` | One project, or all of them, the one changed last first, archived ones included. |
| `Update(ctx, store, options, id, Change)` | Rename, change the instructions, archive or bring back, change the memory space. A field that is nil is left as it is. |
| `Delete(ctx, store, options, id)` | The project and its files' rows, and each file nothing else holds. Its chats are kept, as ordinary chats (decision 0402). |
| `AddFile(ctx, store, options, project, name, data)` | A knowledge file: its text read by the `TextReader`, its bytes kept by the `Blobs`. The same file again is returned with `added` false. Refused with `ErrInvalid` (a reason a person can act on) or, past `projects.max_files`, `ErrFull`. |
| `GetFile`, `Files`, `MarkFile`, `RemoveFile`, `Sensitive` | One file with its text, all of a project's in the order they were added, the sensitive mark, taking one off (and its bytes when nothing else holds them), and whether a project holds a sensitive file. |
| `Of(ctx, store, chat)`, `Chats`, `Counts` | The project a chat is in; a project's chats; how many files and chats each project has. |
| `Move(ctx, store, project, chat)`, `Leave` | Put a chat into a project, out of any other; take it out. |
| `Instructions(words, project)`, `FileItem(words, project, file)` | What a call carries. See "Into each call". |
| `MayShow(file, mayReadSensitive)`, `LeftOut(n)` | Fixed rule 3's answer for a file, and the line given in place of the files left out. |
| `Slug`, `SpaceFor`, `Prefer(store, space)` | Project memory. See "Project memory". |

It reads no setting and does not import the chat record, which reads settings: the turn reads both and passes `Options` and `Words` in, as `internal/attach` does. `CheckOptions` and `CheckWords` refuse values nothing can be done with, naming the setting.

## Files

A knowledge file is a text file: its ending is one of `projects.text_extensions` (plain text, Markdown, code, CSV, JSON, YAML and the like), and it is UTF-8 with no zero bytes. Its text is read once, when it is added: the file as it is, with a byte order mark dropped and line ends made `\n`. A PDF, an image, HTML that would need its markup taken out, or any other file is refused for now.

The bytes go to the tenant's `files/` folder under the file's SHA-256, written under another name and renamed, through the store's `os.Root`, so no name can leave the folder and no tenant reaches another's files. The name a browser gave the file is put on one line and shown and told to a model; it is never a path.

A file is removed from `files/` only when nothing holds it: no project file names it and no chat holds it (`chat_files`, which the chat record keeps and the uploads use too). `chat.Delete` gained one condition so it never removes a file a project holds.

### Two seams

The uploads lane (P9-02) is building a store and text extraction for files added to a chat at the same time (decision 0401). Both are named alike: the SHA-256 in lower-case hex is the file's name on disk and its id in the API, the bytes are the request's raw body with `?name=`, and the same endings are taken. So that the integrator can make the two one store:

- `Blobs` (`Keep`, `Has`, `Release`) is where the bytes are kept. `Folder` is the one shipped. `Options.Blobs` takes another.
- `TextReader` reads a file's text and media type, or refuses it. `ReadText` is the one shipped. `Options.Read` takes another, such as the uploads' extraction once it is shared, which would bring PDFs and HTML.

At the merge with P9-02: the uploads' `dropUnheld` must also count `project_files`, as `Folder.Release` counts `chat_files`; and the file route can take the uploads' per-route body limit.

## Into each call

`Runner.build` (`internal/turn/context.go`, three added lines) asks `projectParts` for the project of the turn's chat:

- **The instructions** go in the context builder's `project` part, which was waiting for this item: the standing start, after the rules, the profile and the person map ([context.md](context.md)). They are the heading of `projects.instructions_heading`, with `{name}` the project's name, a blank line and the instructions; nothing at all when the project has none. They change only when the person changes the name or the instructions, so the call's first cache point covers them and a provider caches them with the rest of the start ([caching.md](caching.md)).
- **The files** go in the `attached` part, after the attached chats, one item each, in the order they were added: the heading of `projects.file_heading` and the text, cut to `projects.file_context_max_chars` characters with `projects.file_cut_note` after it. They share `chats.context_attached_percent` with the attached chats, and a file that does not fit is left out and listed in the turn inspector's record. The part is in the system text after the standing start, which changes rarely.

A chat in no project carries neither: the `project` part is empty, and so is its share of the attachments.

## Fixed rule 1

Every function takes one tenant's store. A project, a file and a chat of another tenant are not in that store's tables, so they read as not found, and `project_chats` refers to the store's own `chats` and `projects`, so a chat cannot be put into another tenant's project from either side. Every route resolves the store from the session ([api.md](api.md)). `internal/projects` is listed as a guard of `rules.tenant_isolation` in `fixed rules`.

## Fixed rule 3

A person may mark a file sensitive (`PATCH /projects/{id}/files/{file}`). Then (decision 0403):

1. **A call is built for the least of its route.** A sensitive file reaches a call only when every model the call's route may reach may read sensitive content (`projects.MayShow`). Otherwise it is left out, and the call is given a pinned line saying how many files were left out, with no name, and that only a local or trusted model may read them.
2. **The chat it goes into is held first.** When a call carries one, `recall.Hold` marks the chat before the call is made; if the hold cannot be kept, the file is left out and counted. From then on the chat goes only to models that may read sensitive content.
3. **It holds the route when the chat's own model may read it.** `Runner.sensitive` counts a project with a sensitive file for a chat whose own model may read it (one added line in `internal/turn/recall.go`), so a chat on a local model with a hosted fallback is carried the file and the fallback is left out. A chat on a hosted model is not moved: its calls leave the file out.

The instructions are the person's own words for the project and go to every model. `internal/projects` is a guard of `rules.sensitive_content` in `fixed rules`, so it may never read a setting.

## Project memory

The knowledge store interface has spaces and no tags, so a project's memory is a space (decision 0400). Each project names one when it is made, from `projects.memory_space` (`project-{slug}` by default, `{slug}` being its name in lower case with hyphens, or its id when the name has no letter or digit), and keeps it when it is renamed. The person may change it, or clear it, with `PATCH /projects/{id}` (`memory_space`).

- **The memory pass** writes a project chat's facts to its project's space instead of `knowledge.memory_space` (one added line in `memoryPass`, `internal/turn/memory.go`). A store makes a space it does not hold as a normal space. A held chat's facts still go to `knowledge.memory_sensitive_space` or nowhere, whatever its project ([memory-pass.md](memory-pass.md)): rule 3 is the memory pass's, unchanged.
- **Recall** in a project chat searches through `projects.Prefer`: a search of every space asks the project's space first, then the whole store, and gives the project's hits first, each hit once, no more than the search's limit (one added line in `searchKnowledge`, `internal/turn/recall.go`). Every hit keeps the label the store gave it, so what recall keeps or withholds for sensitive content is as before ([knowledge-recall.md](knowledge-recall.md)). `projects.recall_prefer` turns it off.

Facts kept before a chat joined a project, or before the space was changed, stay where they are.

## The routes

In `internal/turn/projects.go`, listed in `internal/app` from `turn.ProjectRoutes`, in the shell like every route. A project, file or chat that is not the person's is `not_found` (404); a refused name, instructions or file is `bad_request` (400) with the reason; past a limit is `too_many` (409); a body larger than the API takes is `too_large` (413).

| Route | Does | Answers |
| --- | --- | --- |
| `GET /projects` | The projects, archived ones included, the one changed last first | `ProjectsReply`: each project with its counts of files and chats, and the limits |
| `POST /projects` | Make one: `{"name", "instructions"}` | 201 `ProjectReply` |
| `GET /projects/{id}` | One project | `ProjectReply`: the project, its files (no text), its chats (newest message first), the limits |
| `PATCH /projects/{id}` | `name`, `instructions`, `archived`, `memory_space`; absent fields are left | `ProjectReply` |
| `DELETE /projects/{id}` | Delete it; its chats are kept | `{"deleted": true}` |
| `POST /projects/{id}/files?name=` | Add a file: the body is its bytes | 201 `ProjectFileReply` (with its text), or 200 with `added` false for the same file again |
| `GET /projects/{id}/files/{file}` | One file, with its text | `ProjectFileReply` |
| `PATCH /projects/{id}/files/{file}` | `{"sensitive": true}` or false | `ProjectFileReply` |
| `DELETE /projects/{id}/files/{file}` | Take it off | `{"removed": true}` |
| `POST /projects/{id}/chats` | Start a chat in the project | 201 `ChatReply`, as `POST /chats` answers |
| `PUT /projects/{id}/chats/{chat}` | Move a chat in, out of any other | `ChatProjectReply` |
| `DELETE /projects/{id}/chats/{chat}` | Take a chat out; a chat in another project or none is left | `ChatProjectReply` |
| `GET /chats/{id}/project` | The project a chat is in | `ChatProjectReply`: `chat_id`, and `project` when it is in one |

The limits carry `file_max_bytes` as the smaller of `projects.file_max_bytes` and `server.max_body_bytes`, since the API refuses a larger body before the route reads it.

## Settings

In `settings/projects.json`.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `projects.max_projects` | 200 | tenant | The most projects, archived ones included. |
| `projects.name_max_chars` | 100 | install | The longest name. |
| `projects.instructions_max_chars` | 20000 | tenant | The longest instructions. |
| `projects.max_files` | 20 | tenant | The most files one project has. |
| `projects.file_max_bytes` | 1048576 | install | The largest file; `server.max_body_bytes` bounds it too until a route may take a larger body. |
| `projects.text_extensions` | `.txt,.md,…,.sql` | install | The endings taken. |
| `projects.file_context_max_chars` | 20000 | tenant | The most of one file's text a call is given. |
| `projects.instructions_heading` | `This chat is in the person's project "{name}". …` | tenant | The line before the instructions. |
| `projects.file_heading` | `A knowledge file of the person's project "{project}": {name}. Its text:` | tenant | The line before a file's text. |
| `projects.file_cut_note` | `[The rest of {name}, {chars} more characters, is left out.]` | tenant | The line after a cut file. |
| `projects.memory_space` | `project-{slug}` | tenant | The pattern a new project's memory space is named by. Empty keeps new projects' facts with the rest. |
| `projects.recall_prefer` | true | tenant | Whether recall in a project chat searches the project's space first. |

The line given in place of sensitive files left out is fixed words in `projects.LeftOut`, as the attached chats' is: it is part of the rule.

## In shared code

Every existing line of shared code this item changed:

- `internal/turn/context.go`, in `build`: a comment line, `project, files := r.projectParts(ctx, s, t, p, m)` and `attached = append(attached, files...)` after the attached chats, and `Project:    project,` in the `contextbuild.Input` (added lines).
- `internal/turn/recall.go`: in `sensitive`, `held = held || (mayRead && r.projectSensitive(ctx, s, t))`; in `searchKnowledge`, `ks = r.preferProject(ctx, s, t, ks)` before the search (added lines).
- `internal/turn/memory.go`, in `memoryPass`: `o.Space = r.projectMemorySpace(bg, s, t, o.Space)` after the options are read (an added line).
- `internal/chat/chat.go`, in `Delete`: the query that finds the files to remove gained `AND name NOT IN (SELECT hash FROM project_files)` (one line changed, one added).
- `internal/app/app.go`: `all = append(all, turn.ProjectRoutes(runner)...)` after the attach routes.
- `tools/verify/main.go`: a `componentDocs` line for `internal/projects` and the check `projects`.

`contextbuild` itself did not change: the `project` part and the `attached` part's share were there.

## Checked by

`go test ./internal/projects`:

- The settings are checked, each bad one by name; endings are read with their dots in lower case.
- A project is made with its name on one line and its instructions' lines kept, its memory space named; a blank or long name and long instructions are refused; one past the most is `ErrFull`. It is renamed, its instructions and space changed, archived and brought back; the list puts the one changed last first.
- Slugs and spaces: letters and digits with hyphens, cut; a name with no letter takes the id; an empty pattern gives no space.
- A file is kept under its SHA-256 with its name, media type, size and text (a byte order mark and `\r\n` dropped); the same file again is not added twice; an image, no ending, an empty, blank, binary or Latin-1 file and one too large are refused; one past the most is `ErrFull` and leaves nothing on disk. Marking works and a name that is not a hash reads nothing. A file another project or a chat holds stays on disk when one project lets it go; it goes once nothing holds it. `chat.Delete` keeps a file a project holds.
- Chats move in, between projects and out; a deleted chat leaves its project; another tenant's chat and project cannot be named from either side, read, listed, given files or deleted.
- Deleting a project keeps its chats, removes its files' rows and its files.
- What a call carries: the heading and instructions, nothing for no instructions, a file's heading and text, a long file cut with the note, `MayShow`, the left-out line.
- `Prefer` over the built-in store: the project's space first, each hit once, labels kept, the limit kept, a search of one space untouched.

`go test ./internal/turn` (`projects_test.go`), through the routes, with the fake provider:

- **The proof.** A chat started in a project: its call's system text holds the instructions in the standing start (before the first cache point) and the file's text after it, and the record's `project` part is kept and its `attached` part lists the file. A chat outside carries neither and its `project` part is empty; moved in, it carries both; moved out, neither. Changed instructions reach the next call, and a file taken off does not.
- A long file is cut to `projects.file_context_max_chars` with the note; a project with no instructions leaves the `project` part empty.
- Fixed rule 1: every project route, as another tenant, is `not_found`, and so is that tenant's chat's project; the owner cannot move another tenant's chat in; the other tenant lists nothing; the project is unchanged.
- The routes' refusals: no name, a blank name, archived or an unknown field on a new project, no fields, no such project, file or chat, a second file past the most, an image; the same file again; marking, archiving and renaming (the memory space kept); deleting a project keeps its chat.
- Fixed rule 3: a sensitive file is held back from a hosted model, which is told one was left out and still gets the instructions, and the chat is not held; a local model with a hosted fallback is given it, the chat is held, and its next turn on the hosted model is refused as `sensitive`.
- Project memory, on the test tenant with the built-in store: a project chat's facts land in the project's space, a chat outside keeps them in `memory`; recall in the project chat gives a hit in the project's space first, though a page elsewhere matches better; outside it does not; with `projects.recall_prefer` off it does not.

Faults put in by hand and caught: the routing line switched off, and the files chosen as if every model may read sensitive content.

`verify`, `projects` (`tools/verify/projects.go`), on the real server with the shipped settings and a throwaway folder: a chat started in a project is sent the instructions in the standing start and the file with the attachments; a chat outside is sent neither, moved in both, moved out neither; another tenant reaches the project on no route and no chat crosses; on an invented tenant with a hosted and a local model, a sensitive file reaches no request the hosted model is sent (whose call says one was left out, and whose chat stays unheld) and is carried to the local model, whose chat is held ([tools.md](tools.md)).

## Not here yet

- **Other kinds of file**: PDFs, HTML with its markup taken out, images. They come with the uploads' extraction, through the `TextReader` seam, once the two stores are one.
- **A file larger than `server.max_body_bytes`**, until the uploads' per-route body limit is merged and the file route takes it.
- **Project memory for the knowledge lookups** the model calls itself: only the automatic recall prefers the project's space.
- **Tags** in the knowledge store interface, which would let a project's facts live in any space.
- **A handoff, a background task or a scheduled run from a project chat** does not put its new chat in the project.
- **Searching a project's files** with a tool, for files too large to go whole into a call.
- **A project's instructions or files marked for some models only** beyond the sensitive mark.
