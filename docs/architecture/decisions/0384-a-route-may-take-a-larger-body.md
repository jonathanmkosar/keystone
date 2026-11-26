# 0384. A route may take a larger body than the API's own limit

November 25, 2026 · decided in P9-02

## The question

The API shell limits every request body to `server.max_body_bytes`, 1 MiB by default, before routing. A photo from a phone is several. Should an upload be held to the API's limit, should the limit be raised for everything, or should a route be able to take more?

## The decision

`api.Route` has a field, `MaxBodyBytes`. A route whose limit is larger than the handler's gets its own: the shell finds the request's route with its own mux, and limits the body to that route's number. Every other route keeps `server.max_body_bytes`. The upload route sets it to `uploads.max_bytes`, 5 MiB by default.

## Why

- Raising `server.max_body_bytes` would let every JSON route read five times as much, which no other route needs.
- Holding uploads to 1 MiB refuses most photos.
- The limit stays in the shell, applied before the handler runs and before the audit wrapper, as before: a route cannot read more than it declared.

## What it costs

- Two settings bound a body: `server.max_body_bytes` for every route, and `uploads.max_bytes` for the one that takes files.
- The shell looks a request's route up once more for its limit.
