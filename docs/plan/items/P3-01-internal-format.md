# P3-01. Internal request and stream format

Status: done
Lane: models
Depends on: P0-02
Owner gate: none
Scope: Content blocks (text, image, file, tool call, tool result, thinking), stream events, usage shape.
Proof: Round-trip tests for every block and event type.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

- A new package, `internal/seam`, with plain data types and functions. Standard library only.
- One `Block` type for the six kinds of content: text, image, file, tool call, tool result, thinking. A `Message` is a role and its blocks. A `Request` is the system text, the messages, the tools on offer and the output limits.
- One `Event` type for the stream: start, block start, block delta, block stop, stop, error. A `Usage` shape for token counts, carried by the stop event.
- `Validate` functions that refuse a block, message, request or event with a field that does not belong to its type.
- A `Collector` that folds a stream into a `Response`, and `Events`, which turns a `Response` back into a stream. The fake provider and the adapters build on both.
- Proof: tests that send every block type and every event type through JSON and back and get the same value, and that send every block type through the stream and back.
- Docs: `docs/architecture/seam.md`, a row in the overview, and a line in `verify`'s list of parts.

### Result

Done on November 24, 2026.

- `internal/seam` holds the format: `Block`, `Message`, `Tool`, `Request`, `Event`, `Usage`, `Error` and `Response`, with a `Validate` function for each and `Encode` and `Decode` functions for JSON.
- `Collector` and `Collect` fold a stream into a `Response`. `Events` turns a `Response` back into a stream.
- Proof: `go test ./internal/seam` sends every block type and every event type through JSON and back unchanged, and a reply with every block type a model can write through a stream and back unchanged. `verify` is 8 of 8.
- Decision 0010: a block is one struct with a type field, and so is an event.
- Left for later items: what a call needs (P3-08), cache markers (P3-04 and P5-03), embedding calls (P6-08). No setting was needed.
- Doc: `docs/architecture/seam.md`. Journal: `docs/journal/2026-11-24-models-001.md`.
