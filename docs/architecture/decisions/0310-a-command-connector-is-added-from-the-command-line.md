# 0310. A connector that runs a command is added from the command line, never through the API

November 25, 2026 · decided in P7-04

## The question

A local MCP server is a program Keystone starts and speaks to over its standard input and output. The item asks for connectors "local and remote", and for a Settings screen to add them. Where may a person configure the program a connector runs?

## The decision

**From the command line only.** `go run ./tools/tenant connector add <tenant-id> entry.json` adds a stdio connector, with its command, arguments, plain environment variables and the variable its token is passed in. The API refuses each of those with `403 command_line_only`, on an add and on a change. Through the API a person may change only a stdio connector's label, the name of its token, its switch and whether its results are outside content; they may also remove it. Remote connectors, over HTTP, are added and changed through the API and the screen as the item asks.

**No model, tool result or server can name a command**: the turn reads connectors from the person's record and nothing else, and no tool writes that record.

**The command runs directly**, never through a shell, with an environment of a short list of Keystone's own variables (the path, the system folders, the profile folders), the connector's own variables and its token. Nothing else of Keystone's environment reaches it.

## Why

- A command runs as the account Keystone runs as, which can read every tenant's folder and the vault files beside them. If any signed-in person could set one, any person on the install could read another's data: fixed rule 1 would hold in Keystone's code and fail at the operating system. The test tenant's password is published, and anyone who passes the door can sign in as it.
- Whoever can run `tools/tenant` already has a shell on the machine, so the command line gives them nothing they did not have.
- The token's variable is on the command line's side too: a person who could name it could set `NODE_OPTIONS` or the like to a value of their choosing and run code in the program.
- There is no admin role yet. When there is one, the admin can be given the same power through the browser; until then the command line is the admin.

## What it costs

- The owner adds a local server from a terminal, with a small JSON file, instead of from the screen. The screen says how, with the command.
- An owner question, recorded in the journal (agents 005): should an admin be able to add a command connector from the browser once there is an admin role? Recommended: yes, the admin only.
