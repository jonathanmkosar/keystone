# 0009. The audit log is append-only in the database, and an API call is recorded before it runs

November 24, 2026 · decided in P2-04

## The question

The plan asks for an append-only audit log for each tenant, with every call in it and no key in it. Three things had to be chosen. What stops an entry from being changed? When is a call recorded, so that none is served without an entry? And what keeps a key out?

## The decision

**The table refuses changes for itself.** The log is one table, `audit_log`, in the tenant's database. The `internal/audit` package can append and list, and has no other function. Triggers on the table refuse an update, a delete, and an insert that would replace a row. So the log is append-only for every part of Keystone, not only for the code that remembers to be careful.

**An API call is recorded twice: before it runs and when it ends.** The wrapper the API puts around its routes writes an entry with the outcome `started`, and refuses the call if that entry cannot be written. When the handler returns it writes a second entry with the status and the time taken. Entries are never updated, so the end of a call is a new row and not a change to the first.

**Keys are kept out three ways.** The API wrapper records the method and the path, and no header, query string or body. A caller that holds a key names it when it appends, and that exact value is removed. Every text field is then searched for the shapes of keys, for bearer tokens and for JSON fields named like a secret, and what is found is removed.

**Tokens and cost are columns.** An entry carries the same token counts as the seam's `Usage`, each in a column of its own, and the cost as a whole number of millionths of a US dollar.

## Why

- A rule that lives only in one package is broken by the first SQL statement written somewhere else. The fourth fixed rule should not rest on that.
- Writing the entry after the call leaves a gap. If the write fails, the call has already been served, and a call that stops Keystone half way leaves nothing. Writing first closes both. A `started` entry with nothing after it is itself evidence.
- Holding the answer back until the entry is written was the other way to close the gap. It cannot work for a stream, and the handler's side effects would already have happened.
- Shapes alone miss tokens that have no shape, such as a Waymarks token. Naming alone misses a key that arrives inside a web page or a tool result. Recording less in the first place is the only one of the three that cannot be fooled, so it comes first.
- Columns let spend be totalled with a plain query. Whole numbers avoid the drift that comes from adding many small fractions.

## What it costs

- Two rows for each API call. A reader that wants one row for each call skips the `started` ones.
- Nothing can remove an old entry, so there is no setting for how long entries are kept. If the log grows too large, a later decision has to say how it is cut, and it will need a migration that changes the triggers.
- Someone who opens the database file with their own tool can drop the triggers. The log is not proof against the machine's owner. A chain of hashes would show such an edit, and can be added later without changing an entry.
- The scrub removes too much sometimes: a harmless value in a field called `token` is lost. That is the side to err on.
- The key shapes are listed twice, here and in `verify`, which searches the repo. A new provider's shape goes in both.
- The audit package declares its own `Usage` with the seam's fields, where it could have imported the seam's. It is the guard of a fixed rule and may not import anything that reads settings (decision [0014](0014-fixed-rules-and-tenant-settings.md)), so it imports as little as it can. A test fails if the two shapes ever differ.
- A cost below a millionth of a dollar rounds to nothing. The caller rounds, once for each call.
