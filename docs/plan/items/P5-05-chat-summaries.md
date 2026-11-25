# P5-05. Chat summaries and past-chat search

Status: done
Lane: chats
Depends on: P5-02
Owner gate: none
Scope: Summary, topics and decisions per chat; a past-chat search tool for the agent.
Proof: Test-tenant questions about earlier chats are answered through the tool.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Plan, November 25, 2026 (chats 006), cut from `reboot` at c5a5787:

- **A chat's recap**: a short summary, its topics and the decisions made in it. A new package, `internal/recap`, keeps one for each chat in `chat_recaps` (migration `20261125T171000_chat_recaps.sql`) with a full-text index of its own, writes the request that asks the summary model for one as one JSON object, reads the answer, says when a chat is due, and searches recaps. It reads no setting and does not import the chat record, because it also holds fixed rule 3's one answer for past chats: a chat that was sent sensitive content (`recall.Held`), or whose recap was made while it was, reaches a call only when every model of the call may read sensitive content.
- **The job**, in a new file `internal/turn/recap.go`: when a turn ends as done and the chat has no recap, or one that is `chats.recap_after_turns` finished turns old, a job beside the turn waits until the chat has rested for `chats.recap_after_idle`, then asks the model routing gives the summary task (`routing.summary_model`, then the chat's own, then the fallback chain; for a held chat only models that may read sensitive content) under the turn's request id, and stores the answer, sensitive when the chat was held before or after the call. Usage is of the kind `summary`. Routes: `GET` and `POST /chats/{id}/recap` (read it; make it now), and `GET /chats` gives each chat's summary.
- **The tool**: `search_chats` is extended into the past-chat search rather than replaced by a second tool. It matches any of the words given in titles, recaps and message text, ranks the chats, and returns for each its title, id, date, summary, topics, decisions and best passages, with a `limit`. `read_chat` gives the chat's summary and decisions too. Both leave a held chat out for a model that may not read sensitive content, decided for the least of the call's route as the knowledge lookups are: the registry holds versions made for such a model, and each call that offers tools makes its own for its route and runs those. A held chat given to a model that may read it holds the asking chat.
- **The chat record** gains a new file with an any-of-the-words search of titles and messages, ranked.
- **Fixtures**: recorded recaps for the test tenant's three chats, a held chat and anything else; recorded turns in which the fake model answers questions about earlier chats by calling `search_chats`; use cases.
- **Proof**: tests in `internal/recap`, `internal/chat`, `internal/tools/builtin` and `internal/turn`, and a standing `verify` check, `past chats`: the test tenant's questions about earlier chats are answered through the tool with the chats' recaps, the job runs after a turn, and a held chat's recap is made by no hosted model and never reaches one through the tool.
- **Left for later**: a sweep that gives recaps to chats made before this item or left due by a restart (P8-01's jobs are the place); the chat screen showing summaries (interface lane); what incognito does to recaps (P6-07).

Result (November 25, 2026): done, as planned. `go run ./tools/verify` is 22 of 22 with the new `past chats` check: each named chat of the test tenant is given its recorded recap through the route, by one call of the kind `summary`, and the chat list gives its summary; the test tenant's questions about the lighthouse walk and the ferry times are answered through `search_chats`, whose result names the earlier chat and holds its summary, topics and decisions, with the call in the audit log; a turn's end gives its chat a recap under the turn's request id; and on an invented tenant a chat held by recall is given its sensitive recap by the local model, never by the hosted summary model, and the search leaves it out for the hosted model, which is never sent a word of it, and gives it to the local model, holding the chat that asked. `search_chats` was extended under its own name rather than replaced by `search_past_chats` (decision 0322): the `calls recorded` check, the test tenant's recorded task, the chat screen's step words and stored policies all name it. Decisions 0320 to 0324; the component doc is `docs/architecture/chat-summaries.md`; the journal is `docs/journal/2026-11-25-chats-006.md`, with four owner questions, none of which the proof rests on. Only the fake provider has written a recap; no real model has been called.
