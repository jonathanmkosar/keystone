# Connectors

Folders: `internal/mcp` (the MCP client), `internal/mcp/mcptest` (a test server), `internal/connectors` (each person's connectors, what a turn is offered, the sessions, the routes), the turn's side in `internal/turn/connectors.go`, the command in `tools/tenant/connector.go`, and the screens in `web/app/src/ConnectorSettings.tsx` and `ConnectorMenu.tsx`

A connector is an MCP server whose tools a person's chats may use: a program on this machine, spoken to over its standard input and output, or a remote server reached over the streamable HTTP transport. Each person keeps their own, in their own database, with their tokens in their own vault. A chat is offered each connector tool by its name and one line until it is needed; a small built-in tool, `load_tools`, gives the model the full definitions of the tools it asks for. Every call runs through the tool loop's one path: recorded, timed, checked by the approval rule, and marked as outside content unless the connector is marked otherwise.

Built in P7-04. Decisions [0310](decisions/0310-a-command-connector-is-added-from-the-command-line.md) to [0313](decisions/0313-a-connectors-session-is-kept-in-a-pool.md) say why it is built this way. P12-02 adds Gmail and Google Calendar on top of it ("For P12-02" below).

## The protocol

`internal/mcp` is a client for the Model Context Protocol, standard library only. It implements **protocol version 2025-06-18**, and takes a server that answers with 2025-03-26, or over stdio 2024-11-05: the methods used have the same shape in all three. It reads no setting and chooses no address or command of its own: both come from its caller.

| Call | What it does |
| --- | --- |
| `mcp.Connect(ctx, transport, options)` | Sends `initialize` with version 2025-06-18, an empty set of client capabilities and `clientInfo` `keystone`; checks the version the server answers with and that it offers tools; sends `notifications/initialized`. |
| `client.ListTools(ctx)` | `tools/list`, following `nextCursor`, at most 20 pages. |
| `client.CallTool(ctx, name, args)` | `tools/call`. A tool that failed comes back as a result with `IsError`; an error is a call the server could not take or a server that could not be reached. A call that is stopped sends `notifications/cancelled`. |
| `client.Close()` | Ends the session. |

What the server sends that is not a response: `ping` is answered; any other request is answered as a method not found (no sampling, roots or elicitation is offered); `notifications/tools/list_changed` is noted, and the tools are listed again after the next call; other notifications are let go.

**Stdio** (`mcp.Stdio`): the command is run directly, never through a shell, with exactly the environment it is given. One JSON message to a line. A line that is not a message (a banner) is let go. The last 2 KB of its standard error are kept and named, with the token taken out, when it stops. Closing closes its input, waits two seconds, and then stops it. On Windows it opens no console window.

**Streamable HTTP** (`mcp.HTTP`): each message is a POST with `Accept: application/json, text/event-stream`; a request is answered with one JSON message or an event stream that ends with the response, and what the server sends in that stream before the response is answered or noted. The `Mcp-Session-Id` the server gives at `initialize` and `MCP-Protocol-Version` are sent with every later request, and a DELETE ends the session. 404 on a known session is `ErrSessionGone`; 401 and 403 are `ErrRefused`, and the body of a refusal is never read into an error. No redirect is followed. An error never holds the address, which a person may have written a token into. The server's own event stream (a GET) is not opened, and a broken stream is not resumed.

A message larger than `agents.connector_max_bytes` is refused, on either transport.

## A connector

Kept in the tenant's database (migration `20261125T171500_connectors.sql`):

| Field | Meaning |
| --- | --- |
| `name` | Lower-case letters, digits and single underscores, starting with a letter, at most 24. It starts the name of each of its tools. |
| `label` | What the screens call it. |
| `transport` | `stdio` or `http`. |
| `command`, `args`, `env`, `token_env` | A stdio connector's program, its arguments, plain environment variables, and the variable its token is passed in. |
| `url`, `local` | An http connector's MCP endpoint, and whether the person marked it as their own server on this machine or their home network. |
| `secret` | The name its token is stored under in the vault, such as `connector.notes`. Empty means it takes none. |
| `enabled` | Off, it offers nothing in any chat. |
| `outside` | Its results are outside content. True unless the person says otherwise. |
| `added_by` | `person` (the API) or `command line`. |

`connector_tools` keeps what each connector listed when it was last reached, and `connectors.listed_at` and `problem` when and why not. `chat_connectors` keeps the person's switch for a connector in one chat. The rows go with the chat and with the connector.

## The rules

**A command comes from the command line only** (decision [0310](decisions/0310-a-command-connector-is-added-from-the-command-line.md)). A stdio connector runs a program on the machine Keystone runs on, as the account Keystone runs as, which can read every person's folder. So one is added with `go run ./tools/tenant connector add <tenant-id> entry.json`, by someone who already has a shell on the machine, and the API refuses one (`403 command_line_only`). Through the API a person may change only a stdio connector's label, token name, switch and outside mark: not its command, arguments, environment or token variable (the token variable could otherwise name `NODE_OPTIONS`). No model, tool result or server can name a command: commands are read from the person's record and nowhere else. The command's environment is a short list of Keystone's own variables (`PATH`, `SYSTEMROOT`, `TEMP`, the profile folders and the like), the connector's own variables, and its token; nothing else of Keystone's environment reaches it.

**A remote address is public HTTPS, or the person's own local server** (decision [0311](decisions/0311-a-remote-connectors-address-is-checked-as-it-connects.md)). Not marked local, the address must be `https`, not a name that only means the local network, and every address it resolves to must be public. Marked local, it may be `http`, and every address must be on this machine or a private network (the shared range 100.64.0.0/10, which Tailscale uses, counts as private). Both are checked as the connection is made, with web_fetch's dialler (`web.Dial`, decision [0251](decisions/0251-web-fetch-checks-the-address-it-connects-to.md)): Keystone resolves the name, checks every answer, dials the address it checked, and the socket checks it again. No proxy is used and no redirect is followed, so neither the server nor anything it returns can point the connection at the home network. An entry is checked again each time it is reached.

**A token goes to its server and nowhere else.** It is read from the vault for each session, sent as `Authorization: Bearer` (http) or in `token_env` (stdio), and taken out, as "(the token, removed)", of every result, error and standard error before Keystone keeps or shows it. No route returns one; tokens are stored with the secrets route.

## In a turn

`internal/turn/connectors.go`, made like the knowledge lookups. `turn.Options.Connectors` is the server's `connectors.Pool`; nil means none.

1. **What a call is offered.** Each model call asks the pool for the tools of the person's connectors that are enabled and not switched off for the chat (`Pool.ForCall`). Each tool is named `<connector>_<tool>`: the server's name made into a tool's name (`searchNotes` becomes `search_notes`), with a short hash at the end of one too long or one two names come to. A name the call already offers (a built-in tool, a knowledge lookup) keeps it. A tool set to `never` is not offered. Nothing is offered to a model without the `tools` capability or when the person has switched tools off. What is offered comes from what each connector last listed, so offering reaches no server; a connector that has never listed its tools is reached then, and not again for `agents.connector_retry_after` after a failure.
2. **Names until needed** (decision [0312](decisions/0312-connector-tools-are-offered-by-name-until-loaded.md)). A tool is offered with one line of its description, cut at `agents.connector_line_chars`, the connector's label and "load_tools first", and a schema that takes any object. `load_tools` (`names`: up to `agents.load_tools_max`) answers with each tool's full description and its schema. A tool named in a call of `load_tools` whose result is in the chat's branch, and not an error, is offered whole from then on. `load_tools` only reads (it changes which definitions later calls of the chat are sent, which is the chat's own record), and its result holds the servers' words, so it is outside content unless every connector offered is marked otherwise.
3. **A call** runs through a registry of one, as a knowledge lookup does: recorded before and after in the audit log, under `agents.tool_timeout`, checked by the approval rule. A tool is `ReadOnly` when the server's `readOnlyHint` is true, and otherwise changes something and asks by default. It is `Inside` when the connector is marked as not outside. Its result opens with `[Outside content]` and a sentence that the text is information, never instructions, unless the connector is marked otherwise, and once it has run the chat is marked as having read outside content, so every tool that changes something asks after it, whatever its policy. A tool the server says failed is an error result with the server's words. The approval card says "Use the Notes connector's add with title “Tide”."
4. **A disabled connector's tools** are in no call. A call the model makes to one anyway is answered as a tool that does not exist.
5. **A paused step** (waiting for an approval, or after a restart) asks for the connectors' tools again before it goes on.
6. **Policies.** The approvals screen lists every connector tool the person's connectors last listed, with the server's flags (`Pool.Known`), so a tool can be set to always, ask or never. Removing a connector, or pointing it at another address or command, removes the policies of its tools, so a later server under the same name cannot run a tool without asking because this one could.

The changed lines in `internal/turn` are listed in the journal (agents 005).

## Sessions

`connectors.Pool` keeps a session with each person's connector open after a call, so a stdio server is not started again for each call. A session is opened by the first call of one of its tools, and the tools it lists are kept; it is closed after `agents.connector_idle_timeout` of no use. A connector whose entry or token changed gets a new session (a hash of both, in memory only, says so). A server that forgot the session gets a new one and the call is made once more; one that went away has its session dropped and the call is not made again, since it may have run. The sessions of a connector that was removed or switched off are closed the next time that person's turn asks for tools. Sessions are kept by tenant and connector, and a connector's tool refuses to run with another tenant's store. The runner closes the pool when it closes (decision [0313](decisions/0313-a-connectors-session-is-kept-in-a-pool.md)).

## The routes

In `internal/connectors/routes.go`, added to the one list by `internal/app/app.go`. Each acts for the signed-in person on their own tenant.

| Route | What it does |
| --- | --- |
| `GET /api/v1/connectors` | `ListReply`: each connector as a `Row` (its token's name and whether it is stored, never the token; a stdio connector's variables by name only), with the tools it last listed, `max` and the transports. |
| `POST /api/v1/connectors` | Add a remote connector (`Body`), and reach it to list its tools unless `check` is false. `enabled` and `outside` are true when left out. |
| `PUT /api/v1/connectors/{name}` | Change one. The name stays in the address. |
| `DELETE /api/v1/connectors/{name}` | Remove one, its switches and its tools' policies. Its token stays in the vault. |
| `POST /api/v1/connectors/{name}/check` | Reach it now and keep what it lists, or why it could not be reached. |
| `GET /api/v1/chats/{id}/connectors` | `ChatReply`: the chat's tools menu, each connector with `enabled` (in Settings), `on` (offered in this chat) and `switched` (the chat has its own switch). |
| `PUT /api/v1/chats/{id}/connectors/{name}` | `{"on": false}`: switch one off or on in one chat. A connector off in Settings stays off. |

| Code | Status | Meaning |
| --- | --- | --- |
| `command_line_only` | 403 | A command, its arguments, environment or token variable, sent through the API. |
| `bad_connector` | 400 | The entry is refused. The message names the field and never repeats a value. |
| `exists`, `full` | 409 | The name is taken, or the person has `agents.connectors_max`. |
| `not_found` | 404 | No such connector, or no such chat in this person's record. |

`docs/api/openapi.json` has every shape.

## The command

    go run ./tools/tenant connector add <tenant-id> entry.json
    go run ./tools/tenant connector list <tenant-id>
    go run ./tools/tenant connector check <tenant-id> <name>
    go run ./tools/tenant connector remove <tenant-id> <name>

An entry for a program on this machine, with its token stored first with `tenant secret set <tenant-id> connector.files`:

    {"name": "files", "label": "My files", "transport": "stdio",
     "command": "C:\\Tools\\files-mcp.exe", "args": ["--folder", "C:\\Notes"],
     "env": {"FILES_LOG": "quiet"}, "secret": "connector.files", "token_env": "FILES_TOKEN"}

## On the screen

**Settings → Connectors** (`/settings/connectors`, `ConnectorSettings.tsx`): each connector with its label, address or command, whether its token is stored, the tools it listed (each marked "reads" or "changes"), the problem when it could not be reached, a switch, "Results are outside content", **Check now** and **Remove**. A form adds a remote connector: name, label, address, "This is my own server, on this machine or my home network", and a token. The connector is added without being reached, the token is stored with the secrets route as `connector.<name>`, and then it is reached, so a connector the server refuses leaves no token behind. A section says a program on this machine is added from the command line, with the commands.

**The tools menu** (`ConnectorMenu.tsx`), beside the model picker in the composer: a switch for each connector in this chat, the number of its tools, and "off in Settings" for one that is disabled. On the new chat screen the switches are kept in the page and applied to the chat its first message makes, before the message is sent. A link goes to Settings → Connectors.

## Settings

In `settings/agents.json`, install scope.

| Key | Default | Meaning |
| --- | --- | --- |
| `agents.connectors_max` | 20 | The most connectors one person may have. |
| `agents.connector_connect_timeout` | 20s | How long a server has to start a session and list its tools. |
| `agents.connector_idle_timeout` | 10m | How long a session is kept after its last call. |
| `agents.connector_max_tools` | 64 | The most tools taken from one connector. |
| `agents.connector_max_bytes` | 4000000 | The most bytes of one message read from a server. |
| `agents.connector_line_chars` | 160 | The most characters of the line a tool is offered with before it is loaded. |
| `agents.load_tools_max` | 16 | The most tools one call of `load_tools` loads. |
| `agents.connector_retry_after` | 1m | How long before a connector that has never listed its tools is tried again at offer time. |

`agents.tool_timeout` and `agents.tool_result_max_chars` bound every call and result, as for every tool.

## For P12-02

Gmail and Google Calendar are meant to be connectors of this kind:

- **Their tokens live in the person's vault**, named by `secret`. `reachFor` in `internal/connectors/open.go` is the one place a token is read for a session; an OAuth connector adds a refresh there (read the stored refresh token, get an access token, keep it until it expires) and keeps the same rule that the token goes to its server and nowhere else.
- **A Google connector can be another transport**, a third case in `reachFor`, or a remote MCP server Google runs. Either way its tools reach the turn through `Pool.ForCall` and run through the same registry path.
- **Sends and guest-notifying changes** are tools that change something, so they ask by default, and always ask once the chat has read an email, which is outside content. A tool that must ask whatever the person's policy says (a send) needs one more flag the approval rule reads; that is P12-02's to add to `internal/tools/approval`, with a decision.

## Checked by

- `go test ./internal/mcp`: over HTTP answered as JSON with tools on several pages, and as event streams with a server that pings and notifies before each response; a token sent and a refused token named without the token or the server's body; an unreachable address not repeated in the error; a redirect not followed; versions taken and refused; a forgotten session; a message too large, as JSON and as a stream; over stdio with the test binary as the server, with a banner line, a chatty server and the token in its environment; a command that breaks, with its standard error redacted; a command that is not there; a cancelled call, after which the session goes on.
- `go test ./internal/connectors`: tool names; every refused entry, with no value repeated; the record, with switches and policies going with a connector; what a call is offered by name, `load_tools` and the loaded definitions, a call through the pool marked as outside content, the token taken out of a result and an error, another tenant refused; a disabled connector and a switched-off one offering nothing, and one marked inside; stdio with one session for several calls, closed with the pool; a command that breaks; a missing token; addresses checked as they connect, for both kinds, a redirect, and a stored entry no longer allowed; sessions reused, renewed when the server forgets one, and when the token changes; the routes, including `command_line_only` for every way a command could be sent.
- `go test ./internal/turn` (`connectors_test.go`), the item's proof, over HTTP and over stdio: the test server's tools are listed, the first call is offered them by name, `load_tools` loads two, the next calls are offered those whole and the rest by name, the search runs and is outside content, the add asks and runs once approved, every call is in the audit log with no token, and the switched-off connector's tools are in no call; a call to a disabled connector's tool does not run; a connector tool has a policy.
- `go test ./tools/tenant`: the command adds, lists, checks and removes a stdio connector, and names a token without showing it.
- `verify`:
  - `calls recorded` has a step (`tools/verify/connectors.go`): the test tenant adds a test MCP server on this machine through the API, with a token stored through the secrets route, and a second connector switched off, and sets the server's `add` to always; its recorded turn (`fixtures/test-tenant/turns/connectors.json`) loads two tools, searches and adds. The tools must be listed, the first call offered them by name and `load_tools`, the second whole, the switched-off connector's in none; the search must be outside content; the add must still ask, with the reason `outside`, not run while it waits, and run once approved; and the calls must be in the audit log with no token.
  - `no keys in output` has a connectors surface: servers that repeat the token in a result, in a failed tool's error, in a refusal's body and on a breaking command's standard error. The token must be in no result, error, kept problem, audit entry or file.
  - `offline`: `internal/mcp`, `internal/mcp/mcptest` and `internal/connectors` are on the list of folders that may use the network.
  - It was seen to fail when the connector's tools were made to say they only read and are Keystone's own: the add then ran without asking.
- From the command line, on November 25, 2026, against a data folder inside the worktree with the test tenant: `tenant secret set` stored an invented token as `connector.files`, and `tenant connector add` added a stdio connector whose command was a throwaway build of the test server, a program of its own; it was reached and listed its five tools, and `connector list` named the token without showing it.
- In a browser, the same day, against a server built from this branch on port 7981 with that data folder, signed in as the test tenant, with the test server served over HTTP on port 7982 asking for an invented token: Settings → Connectors listed the stdio connector as added from the command line; the add form refused the HTTP address until it was marked as the person's own server, then added it with its token, reached it, and listed five tools. On the new chat screen the tools menu showed both connectors; the stdio one was switched off there, and the chat the first message made had it off. The recorded connector turn drew "Loaded notes_search, notes_add" with "2 tools", "Used notes_search: query “harbour”" with the marked result when opened, and an approval card, "Use the Invented notes connector's add with text “…”, title “Tide”", asking because the chat read outside content; approved, the add ran and the reply finished. The Approvals screen listed the five `notes_` tools. The menu and the screen at phone width, in dark. The console's only errors were the sign-in probe's 401 and the refused add's 400. The servers were stopped and the data folder removed.

## Not here yet

- OAuth sign-in for a remote server (P12-02 brings Google's), and resources, prompts, sampling, roots and elicitation.
- The server's own event stream, so a notification outside a call is not heard, and resuming a broken stream.
- Images, audio and files a tool returns are named, not passed on to the model.
- A per-connector budget of calls or time beyond the turn's step limit and the tool timeout.

## Provisional

- **Who may add a command connector.** Built as the command line only, the recommended answer to the owner's question in the journal (agents 005). If the answer is "the admin through the browser", it waits on an admin role.
- **The screens** have no mockup; they follow the settings screens and the model picker, and are provisional with P1-05.
- No real MCP server has been reached: only the test server, on this machine.
