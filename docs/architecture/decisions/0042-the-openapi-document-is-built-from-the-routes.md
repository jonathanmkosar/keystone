# 0042. The OpenAPI document is built from the routes, and a copy is committed

November 24, 2026 · decided in P2-05

## The question

Decision 0004 makes the OpenAPI document the contract between the server and the interface, and the parallel plan says an item with a server side and a screen commits its contract first. Is the document written by hand and the server made to match, or the other way round?

## The decision

The document is built from the list of routes by `api.Document`. Each route names the Go types it reads and writes, and the events a stream sends, and the document's shapes are read from those types through their `json` tags.

A copy is committed at `docs/api/openapi.json`. A test fails when the copy is not what the routes say, and `go run ./cmd/keystone openapi --out docs/api/openapi.json` brings it up to date.

A lane that writes its contract first adds its routes with their types and a handler that answers 501, and commits the document that produces. The other lane builds against the committed file.

The events of a stream are listed under `x-keystone-events`, a name of Keystone's own, because OpenAPI has no words for them.

No outside module is used. The reader of Go types is about sixty lines.

## Why

- A document built from the routes cannot describe a route that is not served, or miss one that is. A document written by hand drifts, and the lane that finds out is the one building a screen against it.
- The committed copy is what makes it a contract. A change to a route's shape shows up as a change to that file in the same commit, where the interface lane will see it.
- The Go types are already the truth about what a handler reads and writes. Writing the shapes a second time in YAML would be a second truth.

## What it costs

- The reader knows only what `encoding/json` tags say: names, types, which fields may be left out. It has no words for a limit on a number or a list of allowed values. A route that needs to say more says it in its summary until that is worth building.
- The shapes are written out in full wherever they are used, with no shared definitions, so the file is longer than a hand-written one would be.
- A type that changes its own JSON with a `MarshalJSON` method is described by its fields, which may be wrong. `time.Time` and `json.RawMessage` are handled. Another such type needs a line in `schemaOf`.
- The document says nothing about which errors a route can answer with, beyond the one error shape.
