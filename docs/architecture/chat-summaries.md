# Chat summaries and past-chat search

Folder: `internal/recap`, joined to a turn by `internal/turn/recap.go` and `internal/turn/pastchats.go`. The tools are `search_chats` and `read_chat` in `internal/tools/builtin/pastchats.go`; the any-of-the-words search of titles and messages is `internal/chat/match.go`.

Each chat has a **recap**: a short summary of it, its topics, and the decisions made in it. A job after a chat's turns asks the model routing gives the summary task for one, once the chat has changed enough and has rested, and keeps it with the chat. The model finds earlier chats with **`search_chats`**, the past-chat search: it matches any of the words given in titles, recaps and messages, and gives each chat it finds with its recap and the passages that match best. `read_chat` reads one chat in full, with its recap. Both obey fixed rule 3: a chat that was sent content from a sensitive space reaches a call only when every model the call may reach may read such content.

The package reads no setting, does not import the chat record and opens no connection: the turn reads the settings and the chat, makes the model call through its route, and passes plain values in. It holds fixed rule 3's one answer for past chats, so it is a guard of the rule.

Decisions: [0320](decisions/0320-a-recap-is-made-when-the-chat-has-changed-enough-and-has-rested.md) for when a recap is made and by what, [0321](decisions/0321-a-recap-is-one-row-a-newer-one-replaces.md) for its shape and how it is kept, [0322](decisions/0322-search-chats-is-extended-not-joined-by-a-second-tool.md) for extending `search_chats` rather than adding `search_past_chats`, [0323](decisions/0323-past-chats-reach-a-call-for-the-least-of-its-route.md) for fixed rule 3 and holding the chat that asked, and [0324](decisions/0324-the-past-chat-search-matches-any-word-and-ranks-by-where.md) for how the search matches and ranks.

## A recap

| Field | Holds |
| --- | --- |
| `summary` | What the chat is about, what was found or settled, and where it ended, in at most `chats.recap_summary_max_words` words. |
| `topics` | Short phrases naming what the chat covers, at most `chats.recap_max_topics`. |
| `decisions` | What the person decided, chose or agreed to in it, each a short sentence, at most `chats.recap_max_decisions`. Often none. |
| `through_id` | The last message of the branch it was made from. |
| `turns` | How many of the chat's turns had ended with a whole reply when it was made. |
| `model`, `request_id`, `made` | The model that wrote it, the call it was made under in the audit log, when. |
| `sensitive` | The chat had been sent content from a sensitive space when it, or a recap before it, was made. Never taken back. |

It is a different thing from compaction's summaries ([compaction.md](compaction.md)): those stand for the oldest turns of one branch inside a call, are made for the window, and form a chain. A recap is one short card for the whole chat, made for finding it later.

## When a chat is given one

1. **A turn ends.** `runTurn` closes the turn, then calls `recapLater` (one line). Nothing happens for a turn that did not end as `done`, for a person with `chats.recap` off, or for a chat that is not due.
2. **Due.** `recap.Due`: a chat with no recap is due once one turn has ended with a whole reply; a chat with one, once `chats.recap_after_turns` more have since it was made. So it is not made after every turn.
3. **Rested.** A goroutine waits until `chats.recap_after_idle` has passed since the turn ended. A turn that ends in the chat while it waits moves the wait on, so the recap is made once the person has stopped writing, not in the middle of an exchange. One waits for a chat at a time, in this process.
4. **Looked at again.** When the wait ends, the chat must still be due, have no turn waiting or running, and exist. Otherwise nothing is made: the next turn's end looks again.
5. **Made.** `makeRecap`, with a store of its own, inside `chats.recap_timeout`, under the request id of the turn that started it, so the turn's audit trail and a background task's budget ([jobs.md](jobs.md)) both see it.

When anything fails it writes one line in the log, keeps nothing, and a turn's end does not try the chat again for `chats.recap_retry_after`. A stop forgets the wait; the chat is still due, and its next turn's end starts it again.

### Making one

`makeRecap` is the same for the job and for the route below:

1. The chat's current branch is read. When it has a recap whose `through_id` is still on that branch, the recap so far stands for the messages up to it and only the messages after it are sent; with nothing said since, the recap stands as it is. Otherwise the whole branch is sent.
2. **The route** is the summary task's: `routing.summary_model`, then the chat's own model, then the fallback chain, each behind the budget's guard and the monthly cap ([routing.md](routing.md)). The test tenant has no route and gets its recorded turns. For a chat that was sent sensitive content, routing keeps only models that may read it.
3. **The request** (`recap.Request`): the system text is `chats.recap_prompt`, with `{words}`, `{topics}` and `{decisions}` replaced by the three limits. The one message holds the chat's title, the recap so far or a line saying there is none, and the messages, oldest first, as plain text, written as compaction writes them: who said what, each tool call and what it answered, thinking left out, an image or a file named. Each message is cut to `chats.recap_message_max_chars`. When they are more than `chats.recap_input_max_chars`, or more than the summary model's window holds, the newest are kept, with the chat's first message when there is no recap so far, and a line says how many were left out between. A message too large on its own is cut.
4. **The call** is made through `Runner.call` with the step `stepSummary`: two audit entries and a usage record of the kind `summary`.
5. **The answer** (`recap.Answer`) is one JSON object, `{"summary": "...", "topics": [...], "decisions": [...]}`, read from the first `{` to the last `}`, so a fenced block does no harm. A missing or blank summary is `ErrAnswer`; a summary past its words is cut at a word's end; a topic or decision that is blank, too long, said twice or past its limit is left out and counted; missing lists are empty.
6. **Kept** (`recap.Save`) in place of the chat's recap, sensitive when the chat was held before the call or after it. A chat deleted while its recap was made keeps nothing.

## The past-chat search

`search_chats`, input `query` and, if wanted, `limit` (1 or more, at most `chats.past_chats_max_results`, which is also the number when none is asked for).

1. **Matching.** Any of the words, each also as the start of a longer one. `chat.MatchAny` finds the titles and the messages, on every branch, that hold one, and `recap.Search` the recaps whose summary, topics or decisions do, each the best first by the full-text rank, at most `chats.search_max_results` rows of each.
2. **Ranking.** Each chat's score is twice its title's rank, plus twice its recap's, plus its best message's (lower is better; nothing found counts nothing). A chat that matches in its title or its recap, or in more than one place, comes first.
3. **Leaving out.** The chat the turn is in is never given. Each other chat, best first, is asked `recap.Sensitive` and `recap.MayShow` for the call (below); one that may not be given is counted and passed over.
4. **What the model reads**, for each chat given: its title, id and the date of its last message, "The title matched." when it did, its recap as `Summary:`, `Topics:` and `Decisions:` lines, and up to `chats.past_chats_passages` of its best passages, "The person wrote:" or "The reply said:". The result opens with "N matches for …", which the chat screen's step row reads, or "No other chat matches …". Chats left out are a closing line with their number and no name.

`read_chat`, input `chat_id`, gives the chat's title and dates, its recap, and the messages of its current branch, oldest first, at most `agents.read_chat_max_messages` of the last.

Both only read, and both are Keystone's own content, not outside content ([approvals.md](approvals.md)): reading a past chat never makes a later side effect ask.

A result of `search_chats` used to be one line per matching title or message, with every word required in the same one (P7-01). The tool keeps its name, its `query`, its "N matches for" opening and "No other chat matches", so recorded turns, the chat screen's step words, a person's stored policy and every chat whose history calls it still fit (decision [0322](decisions/0322-search-chats-is-extended-not-joined-by-a-second-tool.md)).

## Fixed rule 3

Content from a sensitive space reaches only models the registry marks local or trusted. A chat that was sent some (`recall.Held`) may repeat it anywhere: in its title, its messages, its recap. So:

1. **Who writes a held chat's recap.** The summary route of such a chat is planned as sensitive, as every route of the chat is, so a model that may not read it is left out, first or as a fallback, even the one `routing.summary_model` names.
2. **The recap's mark.** It is kept as sensitive when the chat was held before the call or after it, and the mark is never taken back.
3. **What a call is given.** `recap.Sensitive(chat)` is true when the chat is held or its recap is marked; a mark that cannot be read counts as sensitive. `recap.MayShow(sensitive, mayReadSensitive)` lets anything of such a chat, its title, its recap or its messages, reach a call only when every model of the call's route may read sensitive content: the least of the route, `readsSensitive` in `internal/turn/recall.go`, as the knowledge lookups use.
4. **The tools are made for each call.** The server's registry holds `search_chats` and `read_chat` made for a call that may not read sensitive content (`builtin.Add`). That is what the model is offered by name. Each call that offers tools also makes the two for the least of its route (`builtin.ChatTools`, called by `Runner.chatTools` from `lookupTools`), keeps them on the turn, and the calls the model makes in reply run those, as the knowledge lookups are run (`internal/turn/lookups.go`). A tool the registry does not hold is not made, so the registry still decides which tools exist.
5. **The chat that asked.** What a tool gives stays in the chat it was given in and is sent with every later call of it. So when a tool gives a call content from a held chat, it first holds the chat it is in with `recall.Hold`, and gives nothing when the hold cannot be kept. From then on that chat goes only to models that may read sensitive content.

`internal/recap` imports neither `settings` nor `internal/tenantsettings`, itself or through another package, and is in `fixedRuleGuards` (`tools/verify/pastchats.go`). It imports the tenant handle, the seam and `internal/knowledge/recall` for `Held`.

The person's own chat list shows a held chat's summary like any other: the rule is about what reaches a model.

## Stored with the chat

`chat_recaps` and `chat_recap_search`, from the migration `20261125T171000_chat_recaps.sql`.

- `chat_recaps`: one row for each chat that has a recap, keyed by the chat, with the fields above; `topics` and `decisions` are JSON lists. A newer recap takes the row's place with `sensitive = max(old, new)`. The row goes with its chat.
- `chat_recap_search`: a full-text index of the summary, topics and decisions, with the chat record's tokenizer. Three triggers keep it in step with `chat_recaps`, a row that goes with its chat included.

| Function | Does |
| --- | --- |
| `Request(ask)` | The request that asks for a recap. |
| `Answer(response, options)` | The recap from the model's answer, or `ErrAnswer`. |
| `Due(recap, ok, done, after)` | Whether a chat is due a new recap. |
| `Save`, `Get`, `Summaries` | Keep a chat's recap; read one; read the summaries of a list of chats. |
| `Search(query, most)` | The chats whose recap holds any of the words, the best first. |
| `MatchAny(query)` | The full-text query for any of the words. |
| `Text(recap)` | A recap as the `Summary:`, `Topics:` and `Decisions:` lines a model reads. |
| `Sensitive(chat)`, `MayShow(sensitive, mayRead)` | Fixed rule 3, above. |
| `CheckOptions(options)` | Refuses settings no recap can be made with, with `ErrOptions` and the setting's name. |

## The routes

In the shell like every chat route ([turns.md](turns.md)), in `internal/turn/recap.go`, listed in `internal/app`:

| Route | Reply |
| --- | --- |
| `GET /chats/{id}/recap` | `RecapReply`: `chat_id`, `made`, and `recap`, empty until the chat has one. |
| `POST /chats/{id}/recap` | `RecapReply`, after making the recap now, as above, under this call's request id. No body. It waits for the model, up to `chats.recap_timeout`. A chat with no message is `bad_request`; one whose recap is being made is `busy` (409); a route with no model is 409 with the route's code, such as `sensitive` or `no_model`; a model that fails or answers with something that is not a recap is `recap_failed` (502), and one that runs out of time 504. |

`GET /chats` gives each chat its recap's `summary`, absent until it has one, for the screen to show later. A chat that is not the person's is `not_found`.

## The test tenant

The test tenant has no route: its recaps are made by its recorded turns.

- `fixtures/test-tenant/turns/recaps.json` answers a request whose system text holds "summary, topics and decisions of one chat", the shipped prompt's words: with a recorded recap for each of the three chats (the lighthouse walk, the north ferry, the bakery), for the questions below, for the chat held for sensitive content (it holds "Melatonin", one of the knowledge fixture's markers), and for anything else. A model of another tenant that names the fake adapter gets the same answers.
- `fixtures/test-tenant/turns/past-chats.json` holds three recorded turns that call `search_chats` and then answer: "What did I decide about the lighthouse walk?", "What times did you find for the north ferry?" and "Have we talked about sleep before?".
- `fixtures/test-tenant/cases/past-chats.json`: the first two questions ask for `search_chats`.
- `fixtures/test-tenant/past-chats.json` holds what the `past chats` check expects: each chat's recap, each question's chat, result and reply, and the held chat's question, the question about it and its marker. The loader does not read it.

The loaded chats are not made by turns, so they have no recap until a turn ends in them or the route is asked.

## Settings

In `settings/chats.json`.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `chats.recap` | true | tenant | Whether chats are given recaps after their turns. Off, the search reads titles and messages alone. |
| `chats.recap_after_turns` | 3 | tenant | A new recap once this many more turns have ended since the last, from 1 to 1000. The first comes after the first turn. |
| `chats.recap_after_idle` | 2m | tenant | How long a chat must rest after a turn before its recap is made, from 0s to 24h. |
| `chats.recap_prompt` | see the file | install | The instruction to the model. `{words}`, `{topics}` and `{decisions}` become the three limits. |
| `chats.recap_summary_max_words` | 80 | install | The most words a summary may have, from 10 to 1000. |
| `chats.recap_max_topics` | 6 | install | The most topics kept, from 0 to 50. |
| `chats.recap_max_decisions` | 8 | install | The most decisions kept, from 0 to 50. |
| `chats.recap_item_max_chars` | 200 | install | The longest topic or decision kept, from 20 to 2000. |
| `chats.recap_message_max_chars` | 2000 | install | The most of one message the model is shown, from 100 to 100000. |
| `chats.recap_input_max_chars` | 24000 | install | The most of a chat the model is shown at once, no less than the line above and at most 1000000. |
| `chats.recap_max_output_tokens` | 800 | install | The longest answer, from 64 to 32000. |
| `chats.recap_timeout` | 60s | install | How long making one recap may take, from 5s to 30m. |
| `chats.recap_retry_after` | 10m | install | How long a chat whose recap failed waits before a turn's end tries again. |
| `chats.past_chats_max_results` | 5 | tenant | The most chats one `search_chats` call returns, from 1 to 50. |
| `chats.past_chats_passages` | 2 | tenant | The most passages given for each chat, from 0 to 20. |

The model is `routing.summary_model` ([routing.md](routing.md)). `search_chats` also reads `chats.search_max_results` (rows of each kind it reads) and `chats.search_snippet_words` (a passage's length); `read_chat` reads `agents.read_chat_max_messages`.

## In shared code

Every existing line of shared code this item changed:

- `internal/turn/runner.go`: the field `recaps recapJobs` on `Runner`, in a block of its own; in `runTurn`, after `memoryLater`, `r.recapLater(s, t, status)`.
- `internal/turn/recall.go`: the field `chats map[string]tools.Tool` on `turnRecall`, with its comment.
- `internal/turn/lookups.go`: in `lookupTools`, after `tr.lookups = nil`, `tr.chats = r.chatTools(ctx, s, t, p, m)`; in `lookup`, three lines first that return a past-chat tool made for the call.
- `internal/turn/routes.go`: the field `Summary` on `Chat`. `internal/turn/handlers.go`: in `list`, `r.withSummaries(...)` before the reply is written.
- `internal/app/app.go`: `all = append(all, turn.RecapRoutes(runner)...)` after the inspector's routes.
- `internal/tools/builtin/builtin.go`: `search_chats` and `read_chat` moved to `pastchats.go` and made for a call that may not read sensitive content; `Options` gained `PastChats`; the package comment. `builtin_test.go`: the one-match result reads "1 match for".
- `tools/verify/main.go`: one `componentDocs` line and the check `past chats`.
- `settings/chats.json`: the fifteen settings above, after `chats.search_snippet_words`.

New files: `internal/recap/*`, `internal/chat/match.go` and its test, `internal/tools/builtin/pastchats.go` and its test, `internal/turn/recap.go`, `internal/turn/pastchats.go`, `internal/turn/recap_test.go`, the migration, the four fixtures and `tools/verify/pastchats.go`.

## Checked by

- `go test ./internal/recap`: each bad setting is named; when a chat is due; the request holds the title, the recap so far or a line saying there is none, tool calls and their results, and no thinking; a long chat keeps its first message and its newest, a small window takes fewer, a message too large is cut, and no room is `ErrNoRoom`; an answer is read fenced, with objects, with lists missing, with too many, repeated, blank and long items left out and counted, and a long summary cut; an answer with no summary, no object or a wrong item is `ErrAnswer`; recaps are kept, replaced, keep their sensitive mark, are searched by summary, topic and decision and by the start of a word, are not found by a word of a replaced recap, and go with their chat, search row and all; `Sensitive` follows the hold and the mark, and a mark that cannot be read counts as sensitive; `MayShow` follows the rule.
- `go test ./internal/chat`: `MatchAny` finds titles and messages holding any of the words, ranks a message with more of them first, finds the start of a word, keeps to its limit, and reads one tenant's chats only.
- `go test ./internal/tools/builtin`: `search_chats` finds a chat by a word only in its recap and gives the recap, ranks a chat matching in its title, recap and a message before one matching in messages, keeps to the limit and the passages, refuses a bad limit, and leaves out the chat the turn is in and another person's; `read_chat` gives the recap. Made for a call that may not read sensitive content, both leave out a held chat and one whose recap is marked, give no word of either, and say how many were left out; made for one that may, they give them and hold the chat the call is in first, and give nothing sensitive when the hold fails.
- `go test ./internal/turn`, through the routes with the fake provider:
  - a chat is given its recorded recap after its first turn, by the test model, under the turn's request id, with one usage record of the kind `summary`, and the chat list gives its summary; one more turn makes none, and two more make a newer one;
  - the recap waits until the chat has rested, and `chats.recap` off makes none;
  - the routes read and make a recap, a recap made again with nothing said since is the same, a chat with no message is refused, and another tenant's chat is not found;
  - **the item's proof**: the test tenant's two questions about earlier chats are answered through `search_chats`, whose results name the earlier chat and hold its summary, topics and decisions, and are in the audit log;
  - **fixed rule 3**: a held chat's recap is made by the local model and never by the hosted model the summary rule names, and is sensitive; the search for the hosted model leaves it out and counts it, and the hosted model is never sent a word of it; for the local model it is given, and the chat that asked is held and then refused a hosted model; `read_chat` refuses a held chat for the hosted model and reads it for the local one, holding the chat that read it.

Faults were put in by hand and each was caught: the per-call tools always made for a model that may read sensitive content (both rule 3 tests, and the `past chats` check), a held chat's recap kept as not sensitive, the job not started at a turn's end, `MayShow` letting everything through, and the sensitive mark taken back by a newer recap.

`verify`, `past chats` ([tools.md](tools.md)), with the real server on a throwaway folder and the expectations of `fixtures/test-tenant/past-chats.json`: each named test tenant chat is given its recorded recap through the route, by one call of the kind `summary`, and the chat list gives its summary; each question is answered through `search_chats`, whose result names the chat and holds its recap, with the reply's words, and the call before and after in the audit log; a turn's end gives its chat a recap under the turn's request id; on an invented tenant with a hosted and a local model that can use tools and the knowledge fixture in its built-in store, a chat held by recall is given its recap by the local model and never asked of the hosted summary model, the search for the hosted model leaves it out and counts it while nothing the hosted model is sent, and no call in the audit log, holds its marker, and the search for the local model gives it and holds the chat that asked.

## Not here yet

- **A sweep.** Chats made before this item, and chats whose wait a stop cut short, get a recap at their next turn's end, or when the route is asked. A sweep at start belongs with P8-01's background jobs.
- **The screen.** `GET /chats` gives each summary and the routes read and make one; showing them is the interface lane's. The chat screen's step row for `search_chats` still reads its result's opening, "N matches for", as before.
- **Incognito** (P6-07). An incognito chat is given a recap and is found by the search like any other; the flag stops the memory pass only.
- **Attaching a chat and handing off** (P5-06) can start from a recap: it is the short form of a chat the plan asks for.
- **Search by meaning.** The search matches words. Vectors are P6-08.
- **A real summary model.** Only the fake has written a recap. How well a cheap model keeps to the shape, and how many decisions it finds, is unknown.
- **A held chat on another branch.** A chat is held as a whole, so all of it is left out, even a branch from before the sensitive turn.
