# P7-04. Connectors

Status: doing (agents)
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
