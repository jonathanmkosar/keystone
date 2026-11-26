# 0350. Gmail and Calendar are built-in connectors with a third transport, run in process, not an MCP server Keystone runs

November 25, 2026 · decided in P12-02

## The question

P7-04 built connectors as MCP servers, local over stdio or remote over HTTP, and left Gmail and Google Calendar to P12-02: "a third case in `reachFor`, or a remote MCP server Google runs". Three ways were open: a small MCP server Keystone runs itself (a stdio child or an HTTP endpoint on this machine) that speaks Google's REST APIs; Google's own remote MCP servers; or tools written in Go, run in process, behind the connector registry. Which fits P7-04's design with the least new surface?

## The decision

**Gmail and Calendar are connectors with the transport `google`**, two rows in the person's `connectors` table named `gmail` and `calendar`, sharing one Google sign-in. Their tools are `internal/google`'s functions, plain data and a `Run` each, offered and run by the same `Pool.ForCall`, `load_tools`, chat switches, policies, outside mark and audit path as an MCP connector's. `reachFor` gets a third case: it reads the refresh token and the OAuth client and gives the call a function that hands it an access token. No session is opened; there is no child process and no protocol between Keystone and its own tools.

A migration makes the three connector tables again with `google` among the transports (SQLite cannot change a check in place), keeping every row.

## Why

- **The approval card and the rule need what MCP cannot carry.** A send must say who it goes to, the subject and the words; a calendar change must say who is told; and the rule needs to know, call by call, whether a call reaches other people (decision 0351). MCP's tool annotations have `readOnlyHint` and nothing like either. In process, each tool's `Describe` and `Reaches` are Go functions of the call's input.
- **Least new surface.** A server of Keystone's own would add a process or an endpoint, its startup and its failure modes, a token passed across a boundary, and a second copy of the address and redaction rules, all to call REST APIs Keystone can call itself. The registry already offers, switches, loads and records connector tools; a third transport reuses all of it.
- **Google's remote MCP servers** would put Google's own tool shapes and approvals model in front of the person, with no card Keystone controls, and are not something the owner has set up. They can still be added as an ordinary remote connector.
- One sign-in for two connectors keeps the tools menu honest: Gmail can be switched off in a chat while Calendar stays on.

## What it costs

- The migration rewrites three tables. It is tested to keep connectors, tools and switches and their cascades.
- Gmail and Calendar's tools are Keystone's code to keep in step with Google's APIs, not a server someone else maintains.
- `Check now` on a Google connector asks Google for an access token instead of listing tools: its tools are fixed in the binary.
