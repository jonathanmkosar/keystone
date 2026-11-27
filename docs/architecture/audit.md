# Audit log

Folder: `internal/audit`

Each tenant's database holds an audit log: a list of entries about the calls made for that person. Entries are added and read. Nothing changes or removes one. Decision [0009](decisions/0009-audit-log-append-only.md) says why it is built this way.

This part enforces the fourth fixed rule: every model call and every tool call is recorded. It reads no setting, and the `fixed rules` check in `verify` fails if it ever imports one.

## An entry

| Field | Meaning |
| --- | --- |
| `id`, `at` | Set by the log when the entry is written. A caller cannot choose them, so an entry cannot be placed in the past. |
| `kind` | `api`, `model` or `tool`. |
| `request_id` | Ties together everything one API call set off: the call itself and each model call and tool call made for it. |
| `actor` | Who made the call: a short name such as `person` or `admin`, or the job or schedule that ran it. Since P15-01, `admin` is a call to an admin route by a person with the admin mark, in their own log, and `admin:<tenant id>` is an admin's read of this person through `adminview`, in this person's log ([admin.md](admin.md)). |
| `action` | What was called: the method and path of an API call, the registry name of a model, the name of a tool. |
| `outcome` | `started`, `ok`, `refused` or `error`. |
| `status` | The HTTP status of an API call. |
| `sent`, `received` | What went out and what came back. For a model call, the seam's request and response as JSON. For a tool call, its input and its result. Empty for an API call. |
| `usage` | The token counts of a model call. The same fields as the seam's `Usage`, so `audit.Usage(u)` converts one. |
| `cost_micros` | What the call cost, in millionths of a US dollar. |
| `duration_ms` | How long the call took. |

A `started` entry is written before a call runs. The entry that ends the call follows, with the same request id and action. A `started` entry with nothing after it is a call that never finished, for example because Keystone stopped.

## Two functions

- `Append(ctx, store, entry, secrets...)` checks an entry, removes any key from it and writes it. It returns the entry as written.
- `List(ctx, store, filter)` returns entries oldest first. A filter narrows by kind and by request id, and reads a page at a time with `AfterID` and `Limit`.

There is no function that changes or removes an entry.

## Append-only in the database

The package's own restraint is not enough, because any part of Keystone can run SQL on a tenant's database. So the table refuses for itself:

- A trigger refuses every `UPDATE`.
- A trigger refuses every `DELETE`.
- A trigger refuses an insert that names an id already in the log. `INSERT OR REPLACE` removes the old row without running the delete trigger, and this closes that way round.
- The table refuses an id below one, which would otherwise make the third trigger refuse every later entry.

What this does not stop: someone who opens the database file with a tool of their own can drop the triggers, or the table. The promise is about Keystone's code. Nothing in Keystone, in any package, can change or lose an entry by mistake or because an agent was talked into it.

Nothing removes old entries, and there is no setting for how long they are kept, because removing one is refused. If the log grows too large, that is a decision of its own.

## Keeping keys out

The plan says no key or token appears in the audit log. Three things keep that true, each covering what the one before can miss.

1. **Writers hand over nothing that carries a credential.** The API wrapper records the method and the path. It records no header, no query string and no body, which is where passwords, keys and session tokens travel.
2. **A caller names the secrets it holds.** Whoever makes a call with a key passes the key to `Append`. That exact value is removed wherever it appears, as plain text and as JSON writes it. This covers tokens with no shape of their own, such as a Waymarks token or the Mac mini gateway's.
3. **Everything is searched by shape.** `Scrub` runs on every text field of every entry. It removes anything shaped like a known key, the token after the word `Bearer`, the value of a header that carries a credential, and the value of a JSON string field named like a secret (`password`, `token`, `api_key` and the like).

What is removed is replaced by `[removed]`, which leaves JSON text valid. `Scrub` is exported, so logs and API responses can use the same function.

The scrub prefers removing too much to too little. A harmless value in a field called `token` is removed too. A number is left alone, so token counts are kept.

The key shapes are a second copy of the list in `tools/verify`, with a few more. `verify` searches the repo and this searches what is written at run time. When a provider is added, its key shape goes in both.

## The API wrapper

`audit.Wrap(who, next)` returns a handler that records every call to `next`. The API shell (P2-05) puts it around the whole API at once, not around each route, so a route cannot be added outside it.

`who` is a function from the request to an `audit.Caller`: the tenant's store and the actor. It is where the session is read.

For each request:

1. A request id is made, placed in the request's context and returned in the `X-Request-Id` header. An id already in the context is kept, so the shell may choose its own. An id sent by the client is not used.
2. `who` is asked whose call it is. If it cannot say, the call belongs to no tenant. It is refused with 401 and `next` does not run.
3. A `started` entry is written. If it cannot be written, the call is refused with 500 and `next` does not run. This is what makes "no call is served without an entry" true.
4. `next` runs. A stream of server-sent events passes straight through.
5. A second entry is written with the status, the outcome and the time taken. Below 400 is `ok`, 400 to 499 is `refused`, 500 and above is `error`. A handler that panics is recorded as an error. A client that leaves early does not stop the entry from being written.

A call with no session is in no tenant's log, because it has no tenant. Recording those belongs to the server's own log, which the API shell brings.

The two refusals in steps 2 and 3 answer with a small JSON body. The shape of an API error belongs to the API shell, which may change them to match.

## Model calls and tool calls

The code that makes a model call or runs a tool appends the entry itself: the kind, the action, what was sent and what came back, the usage, the cost and the outcome. It takes the request id from `audit.RequestID(ctx)` and names the key it used in `secrets`. A call whose entry cannot be written counts as a failed call.

Those callers arrive with usage records (P3-08), streaming turns (P4-03) and the tool loop (P7-01). The plan's standing check "every call recorded" is added with them.

A tenant's database has one connection. `Append` uses it, so it is called outside any transaction the caller has open on the same store.

## Checked by

`go test ./internal/audit`.

- Entries read back as written, by kind, by request id and by page. One tenant's entries are not in another's log.
- Changing, deleting and replacing an entry are each refused by the database, alone and inside a transaction, and the log is unchanged afterwards.
- Made-up keys of twelve shapes are written into every text field and are not in the stored rows. A shapeless token is removed when named. Ordinary text is left as it was.
- A test server with several routes: every call, whatever its status, leaves a `started` entry and an end entry with the right outcome, in its own tenant's log only. A call with no session is not served. A call that cannot be recorded is not served. No header, query string or body reaches the log. A stream arrives as it is written. A panic and a client that leaves are both recorded.

## Not here yet

- A route to read the log, and a screen for it. They come with the API shell (P2-05) and Operations (P11-03).
  - Since P11-03 ([operations.md](operations.md)), Operations reads a few columns of the log, never changing it: each trace's tool calls and a model's last failed call, whose words are passed through `Scrub` again on the way out. The whole log still has no screen.
- A limit on the size of `sent` and `received`. They are stored whole. An image sent inline would be stored inline, so callers should send files by `ref`.
- Proof that the file was not edited from outside, such as a chain of hashes. It can be added without changing an entry's shape.
