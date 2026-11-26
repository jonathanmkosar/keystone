# 0491. The isolation drill finds a leak by marker words, and a change by hashes of the whole folder

November 26, 2026 · decided in P13-02

## The question

The drill (P13-02) has a second person try every way in to the first person's data. "Refused or nothing" has to be judged for hundreds of answers, tool results and job records, many of them ordinary successes that return the second person's own data, and some with ids that collide by design (message numbers, the built-in store's `p1` and `s1`). And "nothing a second tenant does writes a file in the first tenant's folder" has to be proved for writes made anywhere in the server, by any part. How does the drill tell?

## The decision

**Marker words.** Every piece of the first person's content carries an invented word made for the run (`quillmark<what><run>`): their messages and replies, the upload, the artifact's versions, the project's instructions and file, the style, the schedule's prompt, the task and its result, the profile, the memory facts, the page and the source, the secret, the connector token, the Google refresh token and the share token. The second person never types one. So a mark found in any answer, tool result, job record, or anywhere in the second person's folder, database or vault at the end, is a leak, wherever it came from. The names the second person tries with (`Lantern Accounts`, `Lantern Ledger`, `lantern`) are not marks.

**Ids, where they mean something.** In the API part an answer is also searched for the first person's long ids (16 hex characters and more) that the request did not carry, which catches a listing that shows the first person's chat, artifact or project by id alone. The audit route is left out of this, because it lists the second person's own record of what they tried. Short ids are never searched for: both people have a message 5 and a page `p1`.

**Hashes of the whole folder, row by row.** Before and after each part, the drill hashes every file in the first person's folder but the database's own files, every row of every table of their database (sorted, so order does not matter), and lists their audit log. Any difference is a way in. Left out, with the reason written in the code: the audit log, compared entry by entry, and `operations_snapshots`, which the install's sampler writes for each person on a timer.

**One run, then judged.** `isolationdrill.Run` returns every attempt with its result and every way in it found; its test and the standing check `isolation drill` both fail on any way in. The report says, for each attempt, how it was refused and whether it is in the second person's own log.

## Why

- Content leaks travel through many shapes: JSON, server-sent events, a model's request, a tool's text, a search snippet, a file. A word found anywhere needs no parser for each.
- Reads cannot be watched from outside the process on Windows, where the last-access time is off; a read that matters ends somewhere the second person can see, and the marks are looked for there.
- Hashing the whole folder catches a write by any part, including ones added later, without the drill knowing their tables.

## What it costs

- A read that leaves no trace the second person can see (a file opened and dropped) is not caught by the marks. Paths a person can name are closed where they are taken instead, as decision 0490 does.
- A part that adds a table written by a server-wide timer must be added to the left-out list with its reason, or the drill fails on it.
