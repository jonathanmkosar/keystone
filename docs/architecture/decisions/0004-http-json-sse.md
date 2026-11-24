# 0004. The API is HTTP with JSON, and server-sent events for streams

November 24, 2026 · decided in P0-03

## The question

How do the web interface, the phone and the command line talk to Keystone, and how does a streaming answer reach them?

## The decision

One HTTP API. Requests and responses are JSON. Anything that streams (a model's answer, tool steps, sub-agent lanes, job progress) is sent as server-sent events on a response that stays open.

The API is the only thing that touches the data folder. The web interface and the command line are clients of it, with no private path around it. Every route resolves the tenant from the session before it does anything else.

The routes and the stream events are written down in an OpenAPI document, which is the contract the server and the interface both build against.

The server is Go's `net/http`. No web framework is used.

## Why

- One door means one place to check the session, resolve the tenant and write the audit entry.
- Server-sent events are plain HTTP. They pass through proxies and tunnels, the browser reconnects by itself, and a stream can be read with `curl`.
- Streams go one way, from server to client. What the client sends (a message, an approval, a stop) is an ordinary request, so it is checked and recorded like any other.

## What it costs

- No channel from client to server inside a stream. Voice mode may need one, and its item (P10-02) decides that.
- A browser allows about six open connections to one address over HTTP/1.1. The interface keeps one event stream per open chat, not one per component.
- A long stream must survive a dropped connection. Events carry an id so a client can ask for what it missed.
