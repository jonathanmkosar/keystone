# P0-03. Stack decision records and the Windows check

Status: doing (core)
Lane: core
Depends on: P0-02
Owner gate: none
Scope: Decision records for Go, modernc.org/sqlite per tenant, HTTP with server-sent events, React and Vite with committed build output, secrets wrapped by the OS. The binary serves an embedded page on Windows.
Proof: Each decision record exists and is linked from the overview; the binary serves the embedded page on Windows.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Plan:

- Five decision records, 0002 to 0006: Go and one binary, SQLite per tenant through modernc.org/sqlite, HTTP with JSON and server-sent events, React and Vite with the built files committed and embedded, and secrets wrapped by the operating system. Each is linked from the overview.
- `web/dist` holds a placeholder page, and a small Go file in `web/` embeds it. `keystone serve` serves it at the address in `server.address`.
- Proof: a test fetches the page from the handler; `rebuild` gains a step that starts the built binary and fetches the page over HTTP; `verify` gains a check that every decision record is linked from the overview. All of it is run on Windows.

