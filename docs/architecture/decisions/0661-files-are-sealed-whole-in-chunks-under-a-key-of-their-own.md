# 0661. A person's files are sealed whole, in chunks, under a key of their own, at the folder they are kept in

November 27, 2026 · decided in P15-06

## The question

Uploads, project files and artifact versions are files in a person's folder, written and read by the chats, projects and agents lanes. How are they sealed, and where does the sealing go so that those lanes change as little as possible?

## The decision

- **The format.** A sealed file is the magic `KSFILE01`, the chunk size (64 KiB), a random 16-byte salt, and then the chunks. The file's key is HKDF-SHA-256 of the person's files key and the salt, so every file has a key of its own and a chunk's nonce can be its number, with a flag on the last chunk. The header and the tenant id are each chunk's additional data. An empty file is one empty last chunk. A cut at a chunk's edge, one changed bit or another person's file is refused (`atrest.ErrSealed`). It is the same shape as a backup's chunks (decision 0260).
- **Where.** `Store.FilesFolder()` and `Store.ArtifactsFolder()` return a `tenant.Folder` with the methods of `os.Root` the lanes already call (`WriteFile`, `ReadFile`, `Stat`, `Remove`, `RemoveAll`, `Rename`, `MkdirAll`). It seals and names for a sealed person and passes straight through for a plain one. Each lane's change is `s.Files()` to `s.FilesFolder()` (or `s.Artifacts()` to `s.ArtifactsFolder()`) on the lines that touch a file: 8 in `internal/uploads`, 6 in `internal/projects/blobs.go`, 8 in `internal/artifacts`, 2 in `internal/chat`.
- **Whole files.** Every caller writes and reads a file whole, so `Folder` seals and opens whole files; nothing streams.

## Why

- A key per file makes a counter nonce safe and lets two equal files seal differently.
- Keeping `os.Root`'s method names keeps the lanes' code as it was: a reviewer sees one word change per line, and the `.part` file then rename that each lane uses to keep a reader from half a file still works, under keyed names.
- The raw `Files()` and `Artifacts()` stay for the code that needs the folder itself: Operations sums its size, the drills walk it, and the browser keeps its profile beside it.

## What it costs

- `Folder.Stat` describes the file on disk, which is 28 bytes and 16 per chunk larger than what it holds. The lanes use `Stat` only to ask whether a file is there. Operations' sizes are the sizes on disk; the load test counts its own the same way (`atrest.SealedSize`).
- A file's name on disk is not in its additional data, so two of a person's sealed files swapped on disk open as each other. Artifacts check each version's digest against its row; uploads and project files are named by their hash and are not checked again on reading.
- Code that opens a person's files through the raw folders sees sealed bytes. Any new code that keeps a person's files must go through `FilesFolder` or `ArtifactsFolder`; the standing check `encryption at rest` scans the folder after the real packages wrote to it.
