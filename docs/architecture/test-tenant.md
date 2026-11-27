# The test tenant

Folders: `internal/testtenant`, `fixtures/test-tenant`

The test tenant is an invented person, Test Person One, with a known invented password, a few chats, and recorded model turns that the [fake provider](fake-provider.md) plays. `rebuild` loads it, so a fresh checkout ends with someone who can sign in and a model that answers with no key and no network. It is everyone's back end: a screen or a route can be built against it before any real model or real person exists.

Nothing in it is real. Every fixture file says so itself with `"test_data": true`, and a file that does not is refused.

## Signing in as the test tenant

1. `go run ./tools/rebuild`. Its `the test tenant` line prints the tenant's id.
2. The password is the `password` field of `fixtures/test-tenant/tenant.json`.
3. Sign in as Test Person One with that password. Until the sign-in screen exists (P4-01), that is `signin.SignIn(store, password, options)` on the test tenant's store.

The password is published in a public repo and guards nothing. Decision [0080](decisions/0080-the-test-tenant-is-marked-in-its-own-database.md) says what that costs and how to keep the test tenant off an install.

## How it is told from a real tenant

By a mark in its own database: the row `test_tenant` = `true` in `tenant_meta`. The loader writes it into a tenant it has just made, before anything else, and nothing else writes it.

- `testtenant.IsTest(store)` reads the mark on an open store.
- `testtenant.Find(install)` returns the install's test tenant, if there is one. It opens each tenant's store to look, so a server calls it once when it starts.

The name marks nothing. A person named Test Person One is not the test tenant and is never touched by the loader, and the test tenant stays the test tenant whatever its name. The mark is in the tenant's database, so it goes with the database into a backup and out of one.

What reads the mark:

- The loader, to find the tenant on a second run.
- The API and the turn runner (P2-05, P4-03), to give the test tenant the fake provider and nothing else. `testtenant.Provider(fixtures, options)` is that provider.
- The interface, which should show that the person is test data wherever it shows who is signed in.

## The loader

`testtenant.Load(install, fixtures, options)` puts the test tenant into an install. `testtenant.Shipped()` returns the fixtures compiled into the binary, and `testtenant.Read(fsys)` reads them from a folder. The options are the sign-in settings and the chat limits, which the caller reads and passes in. `testtenant.DefaultOptions()` reads the registry's defaults.

It is safe to run twice.

| Step | First run | Later runs |
| --- | --- | --- |
| The tenant | Made, with the fixture's name, and marked. | Found by its mark. |
| The password | Set. | The fixture's password is tried. If it signs in, the session is ended and nothing changes, so nobody is signed out. If it does not, it is set again. |
| Each chat | Made, and remembered in `tenant_meta` as `test_tenant.chat.<file>`: the chat's id and a hash of its fixture and of every recorded turn. | Kept when the chat is there and the hash is the same. Made again when it was deleted. Deleted and made again when its fixture or a recorded turn has changed. |

Chats the person made by hand are never touched. Nothing is written to any other tenant. A chat that fails part of the way through is removed, so a failed load leaves no half chat.

No secret is stored. The fake provider needs no key, so loading the test tenant never touches the operating system's key store.

`Load` returns a `Result`: the tenant, whether it was made, whether the password was set, and how many chats were made and kept.

## The fixtures

In `fixtures/test-tenant/`. `fixture.go` compiles the folder into the binary, so the server finds the fixtures without the repo on disk.

| File | Holds |
| --- | --- |
| `tenant.json` | `name` and `password`. |
| `turns/*.json` | Recorded model turns: script files in the [fake provider's format](fake-provider.md). One of them is a catch-all, so any message gets an answer. |
| `chats/*.json` | One chat each. Loaded in order of file name, so the last file is the newest chat. |
| `cases/*.json` | Use cases and their expected results. |

`turns/tool-steps.json` (P7-01) holds two turns that use tools: a task of several steps, in which the model searches the person's chats, then reads the time, then answers, and a call to `send_postcard`, a tool that would change something. They are typed into a new chat as they are written there. See [tool-loop.md](tool-loop.md).

`turns/compaction.json` (P5-02) holds one recorded summary. It answers any request whose system text holds "running summary of a long chat", words of the shipped `chats.compact_prompt`, so the fake provider summarises a long chat's oldest turns for the test tenant and for any model that names the fake adapter. See [compaction.md](compaction.md).

`turns/memory.json` (P6-06) holds the memory pass's recorded turns: replies to two messages with durable facts in them (two beehives at the allotment, a sister's visit), the facts each gives when the memory pass reads it, the facts of the question about the sensitive space, and an empty list for any other exchange. An extraction answers a request whose system text holds "long-term memory of a personal assistant", words of the shipped `knowledge.memory_prompt`. `knowledge/memory.json` holds the messages, the facts each must give, a message with nothing durable, and the held chat's question, space and facts, for the `memory pass` check. See [memory-pass.md](memory-pass.md).

`turns/connectors.json` (P7-04) holds one: "Find my notes about the harbour, and add one about the tide." The model calls `load_tools` for `notes_search` and `notes_add`, searches, adds a note, which asks for approval, and answers. The test tenant has no connectors, so on the shipped server each call is answered as a tool that does not exist; `verify` adds a test MCP server on this machine as the connector `notes`. See [connectors.md](connectors.md).

`turns/google.json` (P12-02) holds three: "Find the lunch email, draft a reply, and send it." (`gmail_search`, `gmail_draft`, `gmail_send`), "Put a swim on my calendar on Thursday morning." (a personal `calendar_create_event`) and "Invite the keeper to tea on Saturday." (one with a guest). The test tenant has no Google sign-in, so on the shipped server each call is answered as a tool that does not exist; `verify` connects it to a stand-in Google on this machine. See [google.md](google.md).

`turns/web-search.json` (P7-03) holds one: "When is the harbour museum open this week?" The model calls `web_search` and answers citing [1] and [2]. The test tenant has no search key, so on the shipped server the step says "not set up" and the numbers stay text; `verify` stores an invented key and points the search at a stand-in on this machine. See [web-search.md](web-search.md).

`turns/background-task.json` (P8-01) holds five. "Find out in the background when the lighthouse walk should start." makes the model call `start_background_task`, which asks first; the task it starts searches the person's chats, reads the time and answers, in a chat of its own, and its result comes back as a note. "Wait for the tide table, then say when the tide turns." calls `wait_for_tide`, a tool only the `background tasks` check registers. Two more answer the messages Keystone sends a task's next round, by words of `agents.task_resume_text` and `agents.task_continue_text`. See [jobs.md](jobs.md).

`turns/recaps.json` (P5-05) holds the chats' recorded recaps. It answers a request whose system text holds "summary, topics and decisions of one chat", words of the shipped `chats.recap_prompt`, with a recap for each of the three chats, for the questions below, for the chat held for sensitive content, and for anything else. `turns/past-chats.json` holds three questions about earlier chats, each answered by calling `search_chats`: the lighthouse walk, the ferry times, and sleep. `cases/past-chats.json` checks that the first two ask for the search, and `past-chats.json`, beside `tenant.json` and not loaded or embedded, holds what the `past chats` check expects. See [chat-summaries.md](chat-summaries.md).

`turns/scheduled-task.json` (P8-03) holds one: "Write the morning harbour brief: today's date, and what my chats say about the lighthouse walk." reads the time with `current_time` and answers with the brief. Make a schedule with that prompt to see a run in the browser; the `scheduled tasks` check runs it after a simulated outage. See [schedules.md](schedules.md).

`turns/inline-visuals.json` (P9-04) holds three, with invented numbers about kayak hires at a boathouse: "Chart the kayak hires at the boathouse for the season." answers with a bar chart as a fenced `chart` block; "Draw how a kayak hire goes, from arriving to handing the paddle back." with a flowchart as a fenced `mermaid` block; and "Show me one of each kind of chart, and a second diagram." with a line, a pie, an area and a scatter chart, a sequence diagram, and a block that is not a chart. The chat `chats/04-kayak-hires.json`, "Kayak hires in charts", holds the first two, so a fresh checkout has a chart and a diagram to look at. See [inline-visuals.md](inline-visuals.md).

### A chat file

```json
{
  "test_data": true,
  "title": "Planning the lighthouse walk",
  "starred": true,
  "messages": [
    { "role": "user", "blocks": [ { "type": "text", "text": "I want to walk to the old lighthouse on Saturday. What should I bring?" } ] },
    { "role": "user", "blocks": [ { "type": "text", "text": "How long is the walk there and back?" } ] },
    { "edits": 2, "role": "user", "blocks": [ { "type": "text", "text": "How long is the walk if I take the cliff path instead?" } ] }
  ]
}
```

A chat file holds the person's messages only, each in the [seam's format](seam.md). The replies are not written down. The loader plays each message through the recorded turns and stores the reply the turn gives, so a chat in the test tenant always holds what the fake provider would say. This is the plan's "replays recorded turns".

- When a reply asks for a tool, the next message in the file is the tool's result, a `tool_result` block with the call's `id`.
- `edits` is the number, counted from one, of an earlier message in the file that this one replaces, as editing a message does. The chat then has a branch at that point and the new message is on the current branch.
- Titles are different from each other, because a use case names a chat by its title.

`Read` plays every chat in memory before any database is touched. A chat no recorded turn can answer, a reply written into the file, an edit of a later message, and a chat that ends on a tool call are each refused with the reason.

### Use cases

```json
{
  "test_data": true,
  "cases": [
    { "name": "a recorded turn can ask for a tool", "kind": "turn",
      "ask": "When does the north ferry leave on Tuesday?",
      "expect": { "stop_reason": "tool_call", "tool_calls": ["lookup_timetable"] } },
    { "name": "search finds a chat by a word in a reply", "kind": "search",
      "query": "windproof",
      "expect": { "titles": ["Planning the lighthouse walk"] } },
    { "name": "the lighthouse chat shows its edited branch", "kind": "chat",
      "title": "Planning the lighthouse walk",
      "expect": { "messages": 4, "starred": true, "branches": 2, "text_contains": ["cliff path is shorter"] } }
  ]
}
```

| Kind | Does | Can expect |
| --- | --- | --- |
| `turn` | Sends `ask` as one message to the test tenant's model. | `stop_reason`, `text_contains`, `tool_calls` (the tools' names, in order). |
| `search` | Searches the test tenant's chats for `query`. | `titles`: the chats found, in any order, and no others. |
| `chat` | Reads the current branch of the chat titled `title`. | `messages` (how many), `starred`, `branches` (the most messages sharing one parent), `text_contains` (in any message on the branch). |

An expectation that is not set is not checked. A use case that expects nothing is refused.

**Adding a use case** is adding an entry to a file under `cases/`, with a recorded turn or a chat if it needs one. The `test tenant` check runs it with no change to the code.

**Adding a kind** is for an item that builds something new to prove, such as recall or uploads: a field or two on `Case` and `Expect`, a line in `checkCase` and a case in `runCase`, all in `internal/testtenant/cases.go`.

`testtenant.RunCases(store, fixtures, limits)` runs them and returns one line for each that did not give its expected result.

## rebuild, the command and the setting

- `go run ./tools/rebuild` has the step `the test tenant`, after the data folder. It loads the shipped fixtures into the data folder and prints the tenant's name and id, and whether it was made, found as it was, or brought back to its fixtures. It does not run the use cases there, against a tenant someone may have been using. `verify` runs them in a throwaway folder.
- `go run ./tools/tenant test` does the same load by hand, into the data folder or the one `--data` names.
- `test_tenant.load` in `settings/core.json`, on as shipped, is whether `rebuild` loads it. Switching it off does not remove a test tenant that is already there.

## Checked by

- `go test ./internal/testtenant`. Two loads give one tenant, one set of chats and no ended session. A person with the test tenant's name is not marked, gets no password and no chats. A deleted chat comes back, a changed fixture is loaded again without a second copy, a changed password is put back, and a chat the person made stays. Ten kinds of bad fixture are refused. A use case with a wrong expectation is reported.
- The `test tenant` check in `verify` ([tools.md](tools.md)): every JSON file under `fixtures/` says it is test data; in a throwaway data folder, beside an ordinary tenant and under the shipped settings, the test tenant loads twice with the same result, signs in with the fixture's password and resolves to its own store, is the one tenant marked, and every use case gives its expected result.

## Not here yet

- Files, images and a knowledge store with sensitive and ordinary spaces. Uploads are P9-02 and the built-in store is P6-02. Each adds its fixtures to `fixtures/test-tenant/`, a step to `Load`, and a kind of use case.
- Invented knowledge with a sensitive and two ordinary spaces is in `fixtures/test-tenant/knowledge/knowledge.json` (P6-05). The loader does not read it, and the test tenant ships with the `none` adapter. The `recall` and `sensitive content` checks put it into the test tenant's built-in store and into a fake Waymarks themselves ([knowledge-recall.md](knowledge-recall.md)). Loading it in `Load` would change every call the test tenant makes, which is for the item that wants that.
- The fake provider is not wired to a route. The model registry (P3-02) and the turn runner (P4-03) give a store for which `IsTest` is true the provider from `testtenant.Provider`. Until then the recorded turns are played by the loader and by the use cases.
- Chats loaded together all carry the time of the load. The chat record has no way to write an earlier time.
- No command removes the test tenant, because no command removes any tenant.
- A fixture that is not JSON, such as an image, has no way to say it is test data. The item that adds the first one decides how.
