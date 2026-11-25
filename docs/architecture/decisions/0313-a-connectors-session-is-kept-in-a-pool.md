# 0313. A connector's session is kept in a pool, by person and connector, and what a turn offers comes from what was last listed

November 25, 2026 · decided in P7-04

## The question

An MCP session has a cost to start: a stdio server is a program to launch (a package runner can take seconds), and a remote one takes two round trips to initialize. A turn may call a connector's tools several times, and offer them in every model call. When is a session opened and closed, and does offering tools reach the server?

## The decision

**Offering reaches no server.** What a call is offered comes from what each connector last listed, kept in `connector_tools`. A connector is listed when it is added or checked through the API or the command line, whenever a session is opened, and after its server says its tools changed. A connector that has never listed is reached at offer time, once, and not again for `agents.connector_retry_after` after a failure, so a server that is down does not slow every turn.

**Calls share a session from a pool.** `connectors.Pool` keeps one session for each person and connector, opened by the first call of one of its tools and closed after `agents.connector_idle_timeout` of no use. A hash of the connector's entry and its token, kept in memory only, names the session; when either changes, the next call opens a new one. A server that answers 404 on the session gets a new one and the call is made once more, since the server did not take it; a server that went away has its session dropped and the call is not made again, since it may have run. The sessions of a connector that was removed or switched off are closed the next time that person's turn asks for tools. The runner closes the pool when it closes.

**One person's session is never another's.** Sessions are kept by tenant and connector, and a connector's tool refuses to run with another tenant's store.

**The pool is the turn runner's.** The routes do not share it: a check through the API opens a session of its own and closes it.

## Why

- A stdio server started for every call would make each call as slow as a program's start; started for every turn, a little less so. Kept for a few idle minutes, a conversation that uses a connector pays the start once.
- Offering from what was listed keeps a turn's start fast and independent of the network.
- Changes are found by the hash rather than by telling the pool, so the routes need no handle on the runner and nothing in `internal/app` beyond one line.

## What it costs

- A disabled connector's program can keep running until that person's next turn or the idle timeout.
- A server whose tools changed without saying so is offered its old list until it is next listed; a call to a tool that is gone is answered by the server's error.
