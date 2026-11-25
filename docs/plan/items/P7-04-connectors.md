# P7-04. Connectors

Status: provisional
Lane: agents
Depends on: P7-02
Owner gate: none
Scope: MCP servers local and remote; per-tenant connector tokens in secrets; tools menu per chat; most tool definitions sent as names until needed.
Proof: A test MCP server's tools are listed, loaded on demand and called; a disabled connector's tools are absent.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Plan, November 25, 2026 (agents, branch `agents/p7-04-connectors`):

- `internal/mcp`, standard library only: an MCP client speaking JSON-RPC 2.0, protocol version 2025-06-18 (2025-03-26 accepted, and 2024-11-05 over stdio), with `initialize`, `notifications/initialized`, `tools/list` (with its cursor) and `tools/call`, over two transports: a local command over stdio (newline-delimited JSON) and the streamable HTTP transport (POST, a JSON or an event-stream answer, `Mcp-Session-Id`, `MCP-Protocol-Version`). `internal/mcp/mcptest` is a test MCP server in Go over both transports, for tests and `verify` only.
- `internal/connectors`: each person's connectors in their own database (migration `20261125T171500_connectors.sql`): a name, a label, a transport (`stdio` with a command, arguments and plain environment; or `http` with an address), an optional secret name for its token (default `connector.<name>`), enabled or not, and whether its results are outside content (yes by default). The tools each connector last listed are kept beside it, and a switch per chat. A remote connector's address must be public HTTPS, or one the person marks as their own local server, which must then resolve to this machine or a private network; it is checked as it connects (web_fetch's dialler), no proxy, no redirect followed. A stdio connector runs only the command a person configured, with no shell and a small environment; it is added from the command line only (`tools/tenant connector add`), never through the API, so a person signed in through the browser cannot run a command on the machine (fixed rule 1; an owner question).
- A pool of sessions per person and connector, opened when a tool is first called and closed after `agents.connector_idle_timeout`; a changed connector or token opens a new one.
- In a turn: a connector's tools join the call's tools as `<connector>_<tool>`, run through a registry of one as the knowledge lookups do (audit, timeout, approval check, outside mark), are `ask` unless the MCP tool says it only reads, and mark the chat as having read outside content unless the connector is marked otherwise. Names until needed: each tool is offered by name and one line with an empty schema, and a built-in `load_tools` gives the full definitions of the tools named; once loaded in the chat's branch, the full definition is offered. A disabled connector, or one switched off for the chat, offers nothing. The turn's side is one new file, `internal/turn/connectors.go`, and a few lines in `context.go`, `approvals.go`, `policies.go`, `store.go`, `runner.go` and `config.go`.
- Routes: `GET/POST /api/v1/connectors`, `PUT/DELETE /api/v1/connectors/{name}`, `POST /api/v1/connectors/{name}/check`, `GET /api/v1/chats/{id}/connectors`, `PUT /api/v1/chats/{id}/connectors/{name}`. Tokens are stored with the existing secrets route. `docs/api/openapi.json` regenerated.
- The screens: Settings → Connectors (add a remote connector, enable, disable, check, remove, store a token; a stdio connector is shown with how to add one), and a tools menu beside the model picker with a switch per connector for the chat.
- Proof: tests with the test MCP server over stdio and HTTP: tools listed, offered by name, loaded through `load_tools`, called; a disabled connector's tools absent; a changing tool asks. A step in `calls recorded` runs the test tenant's recorded connector turn on the real server against the test server, and `approval` checks its changing tool asks.

Left out: OAuth sign-in for remote servers (P12-02 brings Google's), resources, prompts, sampling and elicitation, and the server's own notifications outside a call.

### Result

Provisional on November 25, 2026. Everything in the scope is built and the proof passes. It waits on two things: the agent mockups (P1-05), for how Settings → Connectors and the tools menu look, which have no mockup; and the owner's answer on who may add a connector that runs a command, built as the recommended answer, the command line only (decision 0310, journal agents 005). No real MCP server has been reached, only the test server on this machine.

- **MCP servers, local and remote.** `internal/mcp` speaks MCP 2025-06-18 (2025-03-26 accepted, and 2024-11-05 over stdio) over stdio and the streamable HTTP transport, standard library only. A stdio connector runs the command a person configured, directly, with a short environment, and is added with `go run ./tools/tenant connector add` only; the API refuses a command (`command_line_only`). A remote connector's address is public HTTPS, or the person's own local server on this machine or a private network, checked as it connects, with no proxy and no redirect (decisions 0310, 0311).
- **Per-tenant connector tokens in secrets.** Each token is in the person's vault, named by the connector (`connector.<name>` from the screen), read for each session, sent to its server only, and taken out of everything the server sends back. The `no keys in output` check has a surface for it.
- **Tools menu per chat.** A switch per connector beside the model picker; the new chat screen's switches go to the chat its first message makes. A connector off in Settings is off in every chat.
- **Names until needed.** Each connector tool is offered as `<connector>_<tool>` with one line and an empty schema; `load_tools` gives full definitions, and a tool loaded in the chat's branch is offered whole from then on (decision 0312). Calls run through a registry of one: recorded, timed, approved by policy (ask unless the server says the tool only reads), and marked as outside content unless the connector is marked otherwise. Sessions are kept in a pool (decision 0313).
- **The proof**, in `go test ./internal/turn` over HTTP and over stdio, and in a step of `verify`'s `calls recorded` on the real server: the test server's tools are listed, offered by name, loaded through `load_tools` and called; a disabled connector's tools are absent from every call; a connector's tool that changes something asks, even set to always, after the connector's outside content. `verify` is 20 of 20.
- Seven routes, `docs/api/openapi.json` regenerated; one migration, `20261125T171500_connectors.sql`; eight settings. The doc is `docs/architecture/connectors.md`; decisions 0310 to 0313; the journal is agents 005. Seen in a browser on port 7981 as the test tenant.
