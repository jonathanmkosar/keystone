# Uploads

Folder: `internal/uploads`. Item P9-02.

The files and images a person adds to a chat: kept in the tenant's `files/` folder under their SHA-256, with a row in the tenant's database, their text read when they are text, and served back to their own tenant only.

This first version describes the store and the routes. The rest of the item, how an upload reaches a model call, is added as it is built.

## The store

- **A file is named by its hash.** `Add` writes the bytes to `Store.Files()`, the tenant's `files/` folder as an `os.Root`, under their SHA-256 in lower-case hex, written under another name first and renamed. The hash is the upload's id, and the `ref` a message's image or file block carries. No name a person gives is ever a path.
- **It is tied to its chat** in `chat_files`, the chat record's table ([chat.md](chat.md)), so deleting the chat removes the file when no other chat holds it.
- **A row in `uploads`** (migration `20261125T213700_uploads.sql`): the chat, the hash, the name, the kind, the media type, the size, the text read and when. The same file added to one chat twice is one row.
- **Remove** takes an upload off a chat, and its file when no other chat holds it. It is refused while a message of the chat names it: it goes with the chat.

## What is taken

| Kind | Known by | Reaches a model |
| --- | --- | --- |
| `image` | its bytes: a type in `uploads.image_types` | as an image, only to a model that can see |
| `text` | its ending in `uploads.text_extensions`, and being UTF-8 with no zero bytes | its text, as an attachment |
| `document` | its bytes: a type in `uploads.document_types` | whole, as a file, only to a model that takes files |

Anything else is refused with a reason a person can act on, and so is a file larger than `uploads.max_bytes`.

## The routes

| Route | What it does |
| --- | --- |
| `POST /api/v1/chats/{id}/uploads?name=` | Upload a file. The body is its bytes. 201 with the upload, or 200 when the chat had the same file |
| `GET /api/v1/chats/{id}/uploads` | The chat's uploads, and the notices about its replies |
| `GET /api/v1/chats/{id}/uploads/{upload}` | One upload, with the text read from it |
| `GET /api/v1/chats/{id}/uploads/{upload}/content` | Its bytes: an image as itself, anything else as a download |
| `DELETE /api/v1/chats/{id}/uploads/{upload}` | Take it off the chat. 409 while a message holds it |
| `GET /api/v1/uploads/limits` | What may be uploaded, for the composer |

The upload route takes a body as large as `uploads.max_bytes`, which may be more than `server.max_body_bytes`: `api.Route.MaxBodyBytes`.
