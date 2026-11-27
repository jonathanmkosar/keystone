# Load test

Folders: `internal/loadtest`, `tools/loadtest`, `tools/verify/loadtest.go`, `settings/loadtest.json`

The load test (P13-03) answers three questions with numbers: how long a person waits once they have years of chats, whether what Operations reports is what is really there, and how much room a person's folder takes. It writes years of invented chats for one person and a smaller load for several more, in a throwaway data folder, runs the real server on it in memory with the fake provider on the model seam, and measures. It calls no model and opens no connection.

There are two sizes. `go run ./tools/loadtest` is the full run: minutes, several gigabytes of disk while it runs, and not part of `verify`. It prints the table below and writes it into this doc and the item. The small profile runs in about three seconds as the standing check `load test`, with the same generator, the same measurements and the same comparison with the Monitor.

## What a run does

`loadtest.Run(profile, options)`:

1. **The rig** (`rig.go`). A throwaway data folder, the profile's people ("Load Person 1" and on), and the real server as `internal/app` puts it together (`app.Open`), in memory. Each person has two models: `load-hosted`, a hosted, priced entry whose address can never resolve (`models.invalid`), and `load-local`, the fake adapter with a 32,768-token window. Both are answered by the fake provider, with no delay, so what is timed is Keystone's own time. The hosted entry is there so the load spends against the monthly cap and a measured turn reads the cap before its call. `chats.recap` is off for the load people: a real recap is made two idle minutes after a turn, by a model call no run of this length can count on, and the load's recaps are generated.
2. **The probe.** One real turn of the first person's, whose audit entry gives the generator what a plain turn's model call carries beside the chat's messages: the tools offered.
3. **The generator** (`generate.go`, `plan.go`, `words.go`, `fast.go`). For the first person, `loadtest.chats` chats spread over `loadtest.years`, each of 1 to twice `loadtest.turns_per_chat` less one turns of about `loadtest.words_per_turn` words, seven in ten on the hosted model, and one long chat of `loadtest.long_chat_turns` on the local model, whose window it outgrows. For each other person, `loadtest.tenant_chats` chats of the same shape. Every chat gets a recap and the model call that wrote it; one chat in `loadtest.upload_every` gets an uploaded file and one in `loadtest.artifact_every` an artifact in two versions.
4. **The Monitor, first time** (`monitor.go`): see below.
5. **The measurements** (`measure.go`), each made `loadtest.runs` times.
6. **The Monitor, second time**, then the sizes on disk (`run.go`).

A run returns a `Result`: the measurements, the sizes, the Monitor's lines and `Problems`. A measurement over its budget, a figure of the Monitor that differs, a long chat that fits its window, or a call the server answered with 500 is a problem, and a run with a problem did not pass.

## The words are invented

Every word is two to four syllables put together by a generator seeded with `loadtest.seed` (`words.go`): "rebain", "lukloudem". The same seed makes the same vocabulary, the same chats and the same ids, so a run is repeatable, and nothing a person wrote is in it. Common words are drawn far more often than rare ones, as in any language (a word's share falls with its place in the vocabulary), which is what makes a search for a common word a different test from a search for a rare one. Each chat has a topic, three rare words it keeps coming back to, so a search for a topic finds few chats.

## The fast path, and the proof that it leaves what a real turn leaves

The real paths take no date: a chat, a turn, an audit entry and a usage record are stamped with the moment they are written. Years of chats written through them would sit in one minute. So a turn's rows are written directly, with dates of their own, in the order a real turn writes them (decision [0531](decisions/0531-the-load-is-written-by-a-fast-path-proven-against-the-real-handler.md)):

| Table | As left by |
| --- | --- |
| `chats`, `messages`, `message_search`, `chat_title_search` | `chat.Create`, `chat.Append`, `chat.Rename` |
| `turns`, `turn_events`, `turn_messages`, `turn_contexts`, `chat_styles` | `internal/turn`: every event of the stream, each streamed piece of `fake.delta_size` characters |
| `usage_records` | one for each model call: the chat call, a hosted chat's title call, the recap's call |
| `audit_log` | two entries for each model call, the first with the whole request, and two for each API call |

The request in the audit log and the record in `turn_contexts` are not written by hand: they come from the real context builder, `contextbuild.Build`, given the person's settings, the model, the chat so far and the probe's tools. Events are the turn package's own types, marshalled.

**The proof** is `TestTheFastPathLeavesWhatARealTurnLeaves` (`agree.go`, `agree_test.go`). Six chats, 17 turns, on both models, are sent through the real handler for one person, with the fake provider scripted to say each planned reply, and written by the fast path for another. The two databases are then compared table by table and row by row. A random id is replaced by its place in the order it was made, a time by a mark, and three columns are left out (how long a call took, in two tables, and the month it fell in). Counted ids are compared as they are, so the order of writing must match too. The test then changes one word of one event and expects exactly one difference.

Through the real store functions: uploads (`uploads.Add`), artifacts (`artifacts.Create`, `artifacts.Update`), recaps (`recap.Save`). Through the API: the measured project, its file, its chat's upload and its attached chats.

Written and not covered by the comparison:

- **The recap's model call**, written as a summary call under the chat's last request, with the recap package's own request (`recap.Request`).
- **The long chat's context.** The builder packs it by leaving its oldest turns out. The real server would by then have written a summary of them; the measured turns in the long chat do set that off, for real.

## The measurements

Each is against the real handler. A turn is read through a response writer that notes when each event of the stream is written (`timedWriter`).

| Measurement | What is timed |
| --- | --- |
| Context packing, the long chat | A message sent in the long chat, on the local model, to its model call opening (the `start` event): the turn is queued, the whole chat read, packed under the window with the oldest turns left out, and the call recorded. |
| Context packing, a project's chat | The same, in a chat that is in a project with instructions and a knowledge file, has an upload its first message names, and has two generated chats attached. |
| Past-chat search, `search_chats` | A real turn whose scripted model calls the tool, from the `tool_call` event to the `tool_result`. Five searches in turn: two common words, a chat's three topic words, a common word and a topic word, one word of middling use, three of the commonest words. |
| Past-chat search, the route | `GET /api/v1/chats/search?q=`, with the same five. |
| The chat list | `GET /api/v1/chats`, the first page of `server.page_limit`. |
| The long chat's page | `GET /api/v1/chats/{id}`: the whole current branch. |
| First streamed words | A message sent in a new chat on the hosted model to the first `text` event. For one person; and with every person sending at once, each in a new chat of their own, started together. |
| Streaming a reply | The first `text` event to the last, for a reply of `loadtest.reply_chars` characters in pieces of `fake.delta_size`: 200 events for 2,400 characters. |
| The usage report | `GET /api/v1/usage?period=this_month`. |
| The Operations report | `GET /api/v1/operations`. |
| The home screen's notice | `GET /api/v1/operations/watch`. |

The median and the 95th percentile are over the runs; the 95th percentile is the nearest rank, the 29th of 30 when sorted.

## The budgets

One for each measurement, in `settings/loadtest.json`, chosen from what a person feels and set before the first full run (decision [0532](decisions/0532-the-load-tests-budgets-are-what-a-person-feels.md)). The full run holds each 95th percentile to its budget. The small profile holds each median to `loadtest.small_budget_percent` of it.

| Setting | Budget | Why this number |
| --- | --- | --- |
| `loadtest.budget_context_long`, `loadtest.budget_context_project` | 250ms | Keystone's own share of the wait for a reply to begin. A model takes a second or so to start; a quarter of that is not felt. |
| `loadtest.budget_first_words_one` | 300ms | The same wait, to the first words on the screen, with a model that answers at once. |
| `loadtest.budget_first_words_several` | 500ms | With every person sending at the same instant. |
| `loadtest.budget_stream_one`, `loadtest.budget_stream_several` | 1s, 1.5s | 2,400 characters in a second is faster than any model writes, so Keystone never holds a reply back. |
| `loadtest.budget_search_tool` | 500ms | A step inside a turn that is already waiting on a model. |
| `loadtest.budget_search_route` | 200ms | A search a person types and watches. |
| `loadtest.budget_chat_list` | 100ms | Drawn with the page; under a tenth of a second reads as at once. |
| `loadtest.budget_chat_page_long` | 400ms | Opening a chat: a click, then the page. |
| `loadtest.budget_usage_report`, `loadtest.budget_operations_report` | 300ms, 500ms | Pages opened now and then. |
| `loadtest.budget_operations_watch` | 150ms | Read by the home screen each time it is shown. |

A measurement over its budget is a finding. Its cause is found and fixed where it is, with a test; a budget is not raised to pass.

## The Monitor's figures against the test's own

Twice in a run, the first person's `GET /api/v1/operations` is read and each figure set beside a count the test kept itself: from what the generator wrote and from what the rig's own turns streamed, never from the database the report reads.

| Figure | The test's own count |
| --- | --- |
| The database's bytes | `keystone.db` and its write-ahead log, read from the folder just before and just after the call; the report's figure must lie between, since the call adds its own two audit entries. |
| The files folder's bytes | The bytes of every upload the generator added, and of the project file and the upload the measurements added. |
| The artifacts folder's bytes | The bytes of every artifact version the generator wrote. |
| The cap: spent this month, and the cap | The cost of every generated call to the hosted model dated this month, and of every hosted call the rig's turns made, each chat's title call among them; the cap's default. |
| Each model's calls and cost over the health window | Generated calls dated inside the report's own window, and the rig's own calls. |
| Each model's median latency | The median of the generated calls' times, while none of the model's calls in the window is one the server timed: after the load is written, for the hosted model. |
| The traces | Each of the newest twenty: its calls, input and output tokens and cost against the test's count for its request id; and, after the measurements, that the newest is the first person's last turn. |

One figure is "at least": after the measurements, the local model's calls. A turn in the long chat sets off a summary of its oldest turns, beside the turn, which no stream tells; the traces of those turns are held the same way.

## The last full run

<!-- load test: the last full run, written by go run ./tools/loadtest -->

No full run has been written here yet.

<!-- load test: end of the last full run -->

## Settings

In `settings/loadtest.json`, all for the whole install. The budgets are in the table above.

| Key | Default | Meaning |
| --- | --- | --- |
| `loadtest.seed` | 20261126 | The seed of the words and the chats. |
| `loadtest.years` | 3 | How far back the first person's chats reach. |
| `loadtest.chats` | 5000 | The first person's chats. |
| `loadtest.turns_per_chat` | 20 | The mean turns in a chat: from 1 to twice the mean less one. |
| `loadtest.words_per_turn` | 220 | About how many words a turn holds; a fifth are the person's. |
| `loadtest.long_chat_turns` | 600 | The long chat's turns. |
| `loadtest.tenants` | 5 | How many more people. |
| `loadtest.tenant_chats` | 200 | Each other person's chats. |
| `loadtest.vocabulary` | 8000 | How many different invented words. |
| `loadtest.upload_every`, `loadtest.upload_kb` | 25, 200 | One chat in so many has an upload, of this mean size. |
| `loadtest.artifact_every`, `loadtest.artifact_kb` | 40, 12 | One chat in so many has an artifact in two versions, each of this mean size. |
| `loadtest.runs` | 30 | How many times each measurement is made. |
| `loadtest.reply_chars` | 2400 | The length of a measured reply. |
| `loadtest.small_days`, `loadtest.small_chats`, `loadtest.small_turns_per_chat`, `loadtest.small_long_chat_turns`, `loadtest.small_tenants`, `loadtest.small_tenant_chats`, `loadtest.small_runs` | 10, 40, 5, 120, 2, 6, 5 | The small profile. Its chats reach back days, so some calls fall in Operations' last day. |
| `loadtest.small_budget_percent` | 100 | The share of each budget the small profile's median is held to. |

`go run ./tools/loadtest` takes `-small`, sizes of its own to try (`-chats`, `-turns`, `-long`, `-tenants`, `-tenant-chats`, `-runs`), `-dir` for where the data folder is made, and `-no-write`. Only the full run, as the settings give it, is written into the docs.

## Checked by

`go test ./internal/loadtest`:

- `TestTheFastPathLeavesWhatARealTurnLeaves`: the proof above.
- `TestTheSmallLoadStaysInsideItsBudgets`: the small profile passes, every measurement was made its number of times, the sizes hold the load, and the Monitor was compared.

In `verify`: the standing check `load test` (`tools/verify/loadtest.go`) runs the small profile and fails on any problem.

Fixes the load test led to have tests of their own: `TestASearchRanksAtMostSoManyMatches` in `internal/chat`, and `TestListFindsAMonthAndARequestByTheirIndex` in `internal/usage`.

## Adding to it

A part that makes a plain turn write a new row fails the comparison until `fast.go` writes it too. A new thing a person waits on is a new name in `Measures`, a budget `loadtest.budget_<name>` in the settings file, and its runs in `measure.go`.
