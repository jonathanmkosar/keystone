# Uploads

Folders: `internal/uploads`, `internal/turn/uploads.go`, and on the screen `web/app/src` (`Attachments.tsx`, `uploads.ts`, `uploads.css`). Item P9-02.

The files and images a person adds to a chat: kept in the tenant's `files/` folder under their SHA-256, with a row in the tenant's database and the text read from text files; readied for each model call as the models its route can reach can take them; served back to their own tenant only. The rule: **an image never reaches a model whose registry entry does not say it can see.**

## The store

`internal/uploads`, decision [0380](decisions/0380-an-upload-is-kept-by-its-hash-and-tied-to-its-chat.md).

- **A file is named by its hash.** `Add` writes the bytes to `Store.Files()`, the tenant's `files/` folder as an `os.Root`, under their SHA-256 in lower-case hex, under another name first and then renamed. The hash is the upload's id, and the `ref` a message's image or file block names it by. The name the person's browser gave is cleaned to one line (`cleanName`) and kept to show and to tell a model; it is never a path.
- **It is tied to its chat** in `chat_files`, the chat record's table ([chat.md](chat.md)), so deleting the chat removes the file when no other chat holds it.
- **A row in `uploads`** (migration `20261125T213700_uploads.sql`), keyed by the chat and the hash: the name, the kind, the media type, the size, the text read, and when. The same file added to one chat twice is one row.
- **`Remove`** takes an upload off a chat, and its file when no other chat holds it. It is refused (`ErrInUse`) while a message of the chat names it: it goes with the chat.
- **`Blocks`** checks a message as it is sent (`checkSend` in the turn calls it): a block that names a file must name an upload of the same chat, and is written as the upload says. An image upload is always an image block, anything else a file block with the upload's name and type, whatever the client sent.

The package reads no setting; `Options` and `Words` are passed in. It imports `net/http` for its routes and for `http.DetectContentType`, and opens no connection.

## What is taken

| Kind | Known by | Reaches a model |
| --- | --- | --- |
| `image` | its bytes: a type in `uploads.image_types` | as an image, only when every model the call can reach can see |
| `text` | its ending in `uploads.text_extensions`, and being UTF-8 with no zero bytes | its text, as an attachment, to any model |
| `document` | its bytes: a type in `uploads.document_types` | whole, as a file, only when every model the call can reach takes files |

Anything else is refused with a reason a person can act on, and so is an empty file or one larger than `uploads.max_bytes`. An SVG is text to the sniffer, so it is never an image.

## The text read

`Extract`, decision [0381](decisions/0381-text-is-read-with-the-standard-library-and-a-pdf-is-sent-whole.md). Plain text, Markdown, code, CSV and JSON as they are, a byte order mark dropped and line ends made `\n`. HTML by a small reader on the standard library: tags out; scripts, styles, comments, `noscript`, `template`, `svg` and a few more dropped with their content; block elements and `br` start lines; a list item starts with a dash; table cells are parted by a bar; character references decoded. No text is read from a PDF: no pure-Go reader is a dependency, so a PDF is sent whole to a model that takes files (provisional).

## Into a call

`uploads.Prepare`, called by `Runner.withUploads` in `internal/turn/uploads.go` from `Runner.build` for every model call, before the context builder.

- **The route decides.** `Route.Sees` is true only when every model the call can reach has `vision`, and `Route.Files` only when every one has `files`: a call is built once and sent to whichever model of its route answers, so it is built for the least of them, as for sensitive content (decision [0323](decisions/0323-past-chats-reach-a-call-for-the-least-of-its-route.md)).
- **An image** goes as its bytes when the route sees, and otherwise `uploads.image_withheld_note` stands in its place. So does an image in a tool's result, and one sent in the message with its bytes.
- **A text file's block** becomes `uploads.file_marker`, and its text, cut to `uploads.text_max_chars` with `uploads.text_cut_note`, goes under `uploads.file_heading` into `contextbuild.Input.Attached`, after the attached chats (P5-06): one item each, the newest file first, each file once, with the label `file: <name>` and the ref `upload:<id>`. The context builder packs attachments within `chats.context_attached_percent` and records each with its tokens ([context.md](context.md)).
- **A document** goes as its bytes, as a file block, when the route takes files, and otherwise `uploads.document_withheld_note` stands in its place.
- **What is no longer stored** is withheld with the same notes and recorded as missing. `Prepare` never fails, and never sends what it could not check.

Each message keeps as many blocks as before, so an index into the branch, such as a pin's, still holds. The result is the same bytes for the same branch and route, so the start of the call caches as before ([caching.md](caching.md)).

**The record.** What each call was given, and what was withheld and why, is an `uploads.Call`, kept in `upload_calls` by turn and step. `GET /chats/{id}/turns/{turn}/inspect` returns it as `uploads` ([inspector.md](inspector.md)), and the chat's notices are made from it.

## Routing by capability

Decision [0382](decisions/0382-a-turns-image-asks-its-route-for-vision.md). Before a chat turn is routed, `mediaOf` reads whether the turn's own message holds an image, and whether an earlier message of its branch does. `planRoute`, which `Runner.routed` calls in place of `routing.Plan`, asks the route for vision (`routing.Ask.Needs`) when the turn's message holds an image, or when an earlier one does and the chat's model can see.

- The chat's model can see: the route is it and the fallbacks that can see.
- It cannot, and a fallback can: the route starts at the first fallback that can. The person is told, and the turn writes the chat's own model on its row, so the next turn goes back to it (`turnMedia.chatModel`).
- None can: the route is planned without vision, every call leaves the images out, and the person is told.

**The last lock** (decision [0383](decisions/0383-the-adapters-refuse-an-image-to-a-model-without-vision.md)): each of the three real adapters refuses, before anything is sent, a request that holds an image for an entry without `vision`, with the seam's `invalid`, which no fallback takes. `seam.Images` counts a request's image blocks.

## Notices

`uploads.Notices` reads, for each turn of a chat, from its first call: each image or document of the person's message that was withheld (`withheld`), and whether a fallback answered to see an image (`moved`), each with a sentence for the person. `GET /chats/{id}/uploads` returns them with the uploads, and the chat screen draws each above the reply of its turn.

## The routes

| Route | What it does |
| --- | --- |
| `POST /api/v1/chats/{id}/uploads?name=` | Upload a file. The body is its bytes. 201 with the upload, or 200 when the chat had the same file. 400 `bad_upload` for a file that is not taken, 413 `too_large` past `uploads.max_bytes` |
| `GET /api/v1/chats/{id}/uploads` | The chat's uploads, each marked when a message holds it, and the notices about its replies |
| `GET /api/v1/chats/{id}/uploads/{upload}` | One upload, with the text read from it |
| `GET /api/v1/chats/{id}/uploads/{upload}/content` | Its bytes. An image as itself, inline; anything else, an HTML page included, as `application/octet-stream` and an attachment. Every answer carries `Content-Security-Policy: default-src 'none'; sandbox`, and may be cached privately, since an id's bytes never change |
| `DELETE /api/v1/chats/{id}/uploads/{upload}` | Take it off the chat. 409 `in_use` while a message holds it |
| `GET /api/v1/uploads/limits` | What may be uploaded, for the composer |

Each acts on the store the API shell resolved from the session, so another tenant's chat and upload are not found (404). The upload route takes a body as large as `uploads.max_bytes`, which may be more than `server.max_body_bytes`: `api.Route.MaxBodyBytes`, decision [0384](decisions/0384-a-route-may-take-a-larger-body.md).

## The screen

Provisional until the workspace mockups (P1-06) are approved: built from the design tokens, after the composer of `design/screens/MainDark.html` (its "Add files or a chat" button) and the attached chat's chip in `Home.html`.

- **The composer** (`Composer.tsx` with `useAttachments`, `AttachButton` and `AttachChips` from `Attachments.tsx`): a plus button opens the file picker, limited to what `GET /uploads/limits` takes; files dropped on the composer are taken too, with an accent border while they are over it. Each file is a chip with its ending, name and size, and a remove button. A file the limits refuse is not taken, and the reason is shown under the chips. A message may be files alone.
- **Sending** (`messageWith` in `uploads.ts`, through `send` in `turns.ts`): the files are uploaded when the message is sent, then the message goes with their blocks first and the words last. A refusal from the server is the turn's error, as any send's.
- **The person's message** (`SaidMedia`): images as thumbnails from the content route, opening whole in a new tab; other files by name, as downloads.
- **The notice** (`ReplyNotices`): above a reply, a line with the server's sentence; dashed in the accent for a withheld image or file.
- **The inspector**: the attachments part lists each file's text with its tokens, and a section, "Images and files", says what the first call was given of each and why anything was withheld.

The content security policy (`default-src 'self'`) allows the thumbnails, which come from Keystone's own address, and refuses `blob:` images, so a chip shows no thumbnail before sending.

## Settings

In `settings/uploads.json`, lane `uploads`.

| Setting | Scope | Default | What it does |
| --- | --- | --- | --- |
| `uploads.max_bytes` | install | 5242880 | The largest file one upload may be |
| `uploads.image_types` | install | png, jpeg, gif, webp | The image types taken, known by their bytes |
| `uploads.text_extensions` | install | `.txt`, `.md`, `.csv`, `.json`, `.html`, code and more | The endings of the text files taken |
| `uploads.document_types` | install | `application/pdf` | The document types taken, sent whole to a model that takes files |
| `uploads.text_max_chars` | tenant | 60000 | The most characters of one file's text in a call |
| `uploads.file_heading` | tenant | a sentence with `{name}` | Before a file's text, with the attachments |
| `uploads.file_marker` | tenant | a sentence with `{name}` | In the message, where a text file was |
| `uploads.text_cut_note` | tenant | a sentence with `{name}` and `{chars}` | After a file's text that was cut |
| `uploads.image_withheld_note` | tenant | a sentence with `{name}` | In place of an image, for a call that cannot see |
| `uploads.document_withheld_note` | tenant | a sentence with `{name}` | In place of a document, for a call that takes no files |

## Checked by

- `go test ./internal/uploads`: a file is kept under its hash, tied to its chat and named once; kinds are known by their bytes or ending, and anything else is refused; text is read and HTML read as its words; an upload a message holds cannot be removed, and deleting the chat removes its files; another tenant reads nothing, and a path-like name is refused; `Prepare` sends an image only to a route that sees, anywhere in the branch, puts a file's text in the attachments newest first and cut, sends a document only to a route that takes files, and withholds what is gone; the routes, through the API shell, with a body larger than the shell's own limit.
- `go test ./internal/turn` (`uploads_test.go`): an image never reaches the model without vision, as the model picked, behind a fallback, or as an earlier turn; a turn whose model cannot see goes to the fallback that can, with the image's bytes, and the chat keeps its model; the file's text is in the call and in the inspector's attachments part; a call is readied for the least of its route; a message names only an upload of its own chat.
- `go test ./internal/adapters/...` (`vision_test.go` in each): each real adapter refuses an image for an entry without vision before anything reaches a stand-in server, and sends one in its provider's form for an entry with it.
- `go test ./internal/api` (`bodylimit_test.go`): a route may take a larger body; the others keep the handler's limit.
- `verify`, `uploads` ([tools.md](tools.md)): the same proof through the running API, with the audit log searched, and the three real adapters as the server builds them.

## Not here yet

- Text from a PDF (decision 0381): provisional.
- Files and images in the test tenant's fixture.
- A thumbnail on a chip before sending, and an upload's progress.
- Pasting an image into the composer.
- Removing an upload from the screen once it is sent: the route refuses it while a message holds it, and the chat's deletion removes it.
