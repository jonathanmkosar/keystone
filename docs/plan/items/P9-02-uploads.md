# P9-02. Uploads

Status: provisional
Lane: chats + interface
Depends on: P4-03
Owner gate: Approval of the workspace mockups (P1-06), for the screens only.
Built without the gate: The server side and its tests; the screen built from the draft mockup with design tokens, marked provisional until the mockup is approved.
Scope: Files and images with text extraction; routed only to models with the needed capability.
Proof: An image never reaches a model without vision; extracted text appears in the inspector.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan (interface lane, November 25, 2026)

Branch `interface/p9-02-uploads`, cut from `reboot` at 798e627. New code goes in new files; the lines changed in shared files are listed in the journal.

1. **Upload and store**, a new package `internal/uploads`. A file is written to the tenant's `files/` folder under its SHA-256, through `Store.Files()` (an `os.Root`, so no name can leave the folder), and tied to its chat in `chat_files`, so deleting the chat removes it. A row in a new table `uploads` (migration `20261125T213700_uploads.sql`) keeps its name, kind, media type, size and extracted text; the chat and the hash name it. Routes under `/api/v1/chats/{id}/uploads`: add (the body is the file, `?name=` its name), list, one, its bytes, remove (refused while a message holds it), and `GET /api/v1/uploads/limits` for the composer. An image or a PDF is known by its bytes, a text file by its ending and by being UTF-8. The largest size, the kinds taken and the text limits are settings in a new `settings/uploads.json`. A route may take a larger body than `server.max_body_bytes`: a field on `api.Route`.
2. **Text extraction**: plain text, Markdown, code, CSV and JSON as they are; HTML to text with a small reader on the standard library (the repo has no HTML parser). No pure-Go PDF reader is a dependency, so a PDF is stored and sent whole to a model whose entry takes files, and its text is not read: provisional, with a decision record.
3. **Into a call** (`internal/turn/uploads.go`): an image or file block names an upload of its chat by its hash, checked when the message is sent. For each model call, an image goes as its bytes only when every model of the route can see; otherwise a note stands in its place. A text file's text goes into `contextbuild.Input.Attached`, newest first, one item each, so the inspector shows it with its tokens. What each call was given and what was withheld is kept in `upload_calls` and returned by the turn inspector.
4. **Routing by capability**: a turn whose message holds an image asks the route for vision (`routing.Ask.Needs`), so it goes to the chat's model if it can see and to the first fallback that can if not. When none can, it goes to the chat's model without the image, and the person is told. The chat keeps the person's model for later turns.
5. **The adapters**: each refuses an image to an entry without vision before anything is sent, and sends an image in its provider's form to one with it, proven against the stand-in servers.
6. **The screen**, provisional on the workspace mockups (P1-06): an attach button and dropping files on the composer, chips with remove, the files uploaded when the message is sent, images as thumbnails in the person's message, the notice on a reply that did not see an image, the attached files in the inspector.
7. **The proof**: tests in each package, and a `verify` check, `uploads`: an image never reaches a model without vision, by any route, and a file's extracted text is in the inspector's record.

Left for later: reading text out of a PDF; files and images in the test tenant's fixture; thumbnails before sending (the content security policy refuses `blob:` images, so the chips show names).

### Result (interface lane, November 25, 2026)

Provisional on the workspace mockups (P1-06) for the screens; the server side is complete and proven.

- Built as planned: `internal/uploads` (store, extraction, routes, `Prepare`, the record and the notices), `internal/turn/uploads.go` (the route asks for vision, each call readied for the least of its route, the chat keeps its model), the adapters' refusal, `api.Route.MaxBodyBytes`, `settings/uploads.json`, migration `20261125T213700_uploads.sql`, and the screen (`Attachments.tsx`, `uploads.ts`, `uploads.css`). Decisions 0380 to 0384.
- The proof: an image never reaches a model without vision (`TestAnImageNeverReachesAModelWithoutVision` and `TestACallIsReadiedForTheLeastOfItsRoute` in `internal/turn`, a `vision_test.go` for each real adapter checking the request it builds against a stand-in, and the `uploads` check in `verify`); the extracted text is in the inspector's record (the attachments part with its tokens, in the same tests and check). `verify` is 24 of 24.
- Seen in the browser on the test tenant: chips, drop, remove, a refusal, thumbnails, the notice, and the inspector's attachments and Images and files, at desktop width in light and phone width in dark.
- Provisional: the screens (no mockup), and PDF text, which is not read (decision 0381): a PDF goes whole to a model that takes files.
- Not done: files and images in the test tenant's fixture; a thumbnail before sending; pasting an image.
- `reboot` at c268a41 (P5-06, P12-02) is merged into the branch, with four conflicts resolved; `verify` is 25 of 25 on the merge. The files' text follows the attached chats in the attachments part.
- See `docs/journal/2026-11-25-interface-005.md` and `docs/architecture/uploads.md`.
