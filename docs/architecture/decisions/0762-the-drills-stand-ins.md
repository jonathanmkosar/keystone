# 0762. The drill's stand-ins: where each plant sits, and the browser's host rule

November 28, 2026 · decided in P13-01

## The question

The tricked-agent drill must plant instructions in web pages, fetched files, email and connector results, with everything on this machine and nothing changed in the parts it tests. Some parts have no way to serve a planted page on this machine: `web_fetch` reads only public addresses, by a rule the server cannot change, and the browser's shipped host rule refuses this machine and every name that does not resolve. Where does each plant sit?

## The decision

- **A web search result**: the server's `Web.Searcher`, which the web tools already take from a caller. It gives the two planted results for every query and keeps every query.
- **A web page and a fetched file**: the fake browser (`Browser.Driver`), which puts every request a page makes through the launch's policy as the real one does. Its host rule (`Browser.HostAllowed`, which a caller may give) lets through the fake's invented sites, names under `.example`, which never resolve, and holds every other host to the shipped rule's own tests: `web.IsPublic` for an address and `web.IsLocalName` for a name; any other name is refused, since the drill resolves none.
- **`web_fetch` keeps the shipped rule.** It is tried only where it must refuse: Keystone's own address by `127.0.0.1` and by `localhost`, and, in a held chat, addresses carrying a sensitive word. Its pages with planted words are covered by the browser's, since both are tools that are not Keystone's own and mark the chat the same way.
- **An email and a calendar event**: the stand-in Google's `Echo`, which it repeats in every message and every event description.
- **A connector's result and a tool's description**: a test MCP server of the drill's own, made from `mcptest.Server`.
- **A knowledge source**: the built-in store, written directly, as an import would.
- **Keystone's own address**: a stand-in server on this machine that counts every request. The drill's server is in memory, so a request that got past a guard to this machine would land on the stand-in.

## Why

- Every stand-in is a seam the server already offers to its own checks. Nothing in the parts under test was changed to make the drill possible.
- Keeping `web_fetch` on its shipped rule means the drill tries the real guard where it matters: a planted address on this machine.

## What it costs

- The browser's host rule in the drill is the drill's, written from the shipped rule's parts. The shipped rule itself is proved by the browser's own tests and the `browser use` check.
- One `Echo` text is in both the email and the event, so they share their planted words.
