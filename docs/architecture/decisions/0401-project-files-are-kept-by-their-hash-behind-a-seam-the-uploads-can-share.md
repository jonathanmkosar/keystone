# 0401. Project files are kept by their hash, behind a seam the uploads can share

November 25, 2026 · decided in P9-01

## The question

A project's knowledge files need their bytes kept and their text read. The uploads lane (P9-02) is building the same for files added to a chat, in another worktree, at the same time, and is not merged. Should projects wait for it, borrow its code, or build its own?

## The decision

**Projects build their own, small, and shaped like the uploads'**, so the integrator can make the two one:

- The bytes go to the tenant's `files/` folder named by their SHA-256 in lower-case hex, as the plan's data folder says and as the uploads name theirs, written under another name and renamed. So one file added to a chat and to a project is one file on disk.
- The SHA-256 is also the file's id in the API, and a file is added as the request's raw body with `?name=`, as an upload is.
- Text is read from text formats only (UTF-8 with no zero bytes, by ending), from the same list of endings, less HTML, whose markup the uploads take out and projects do not yet.

**Both sit behind a seam in `internal/projects`:** `Blobs` (`Keep`, `Has`, `Release`) for the bytes and `TextReader` for the text, each with one implementation shipped (`Folder`, `ReadText`) and a field in `Options` that takes another.

**A file goes only when nothing holds it.** `Folder.Release` removes a file only when no project file names it and no chat holds it (`chat_files`). `chat.Delete`, which removes a deleted chat's files that no other chat holds, gained one condition so it keeps a file a project holds.

## Why

- Waiting would leave the item without files; copying another branch's code would make two copies to merge.
- Naming files alike means the two stores already agree on disk: merging them is a matter of code, not of moving files.
- A shared folder needs both sides to know the other's holders before a file is removed. With the chat record's `chat_files` counted here and `project_files` counted in `chat.Delete`, this branch never removes a file the other side holds.

## What it costs

- At the merge, the uploads' `dropUnheld` must count `project_files` too, or removing an upload could remove a project's file of the same bytes. It is one condition, named in the journal for the integrator.
- Until the uploads' extraction is shared, a project takes no PDF, image or HTML.
- Until the uploads' per-route body limit is merged, a project file is bounded by `server.max_body_bytes` as well as `projects.file_max_bytes`.
