# 0380. An upload is kept by its hash, tied to its chat, and sent when the message is

November 25, 2026 · decided in P9-02

## The question

The plan says the tenant's `files/` folder holds "uploads and attachments, named by hash", and the chat record already ties the files a message names to its chat (`chat_files`). How is an upload stored, named, removed and sent, and when does the browser upload it?

## The decision

**A file is kept under its SHA-256** in lower-case hex, written through the store's `os.Root`, under another name first and then renamed. The hash is the upload's id in the API and the `ref` a message's image or file block names it by. The name the person's browser gave it is kept in the row, cleaned to one line, and is never a path.

**A row in `uploads`, keyed by the chat and the hash**, keeps the name, the kind, the media type, the size and the text read. The same file added to one chat twice is one row. The file is tied to the chat in `chat_files`, so `chat.Delete` removes it when no other chat holds it.

**A message names only an upload of its own chat.** When a message is sent, each block that names a file is checked against the chat's uploads and written as the upload says: an image upload is always an image block, anything else a file block with the upload's name and type, whatever the client sent.

**An upload a message names cannot be removed**; it goes with the chat. One no message names can be, and its file goes when no other chat holds it.

**The browser uploads at send.** The composer holds the files, checks them against `GET /uploads/limits`, and uploads them when the message is sent, then sends the message with their blocks. A file the server refuses stops the send and says why.

## Why

- Named by hash, the same bytes are stored once however often they are sent, the name says nothing a person typed, and `os.Root` refuses any name that leaves the folder.
- Keying the row by chat and hash needs no id of its own, and the existing `chat_files` gives deletion with the chat for nothing.
- Writing the block from the upload means the server, not the client, decides whether something is an image, which is what decides where it may go.
- Uploading at send means no upload is left behind by a message never sent, and the new chat screen, which has no chat until the first message, works the same as a chat.

## What it costs

- A chip shows a file's name and size, not a thumbnail, until the message is sent: the content security policy refuses `blob:` images, and the file is not on the server yet.
- A large file is uploaded while the message waits, with the send button busy and no progress bar.
- A file removed from a chat and uploaded again to it is a new row with a new time.
