# P7-03. Web search and fetch

Status: doing (agents)
Lane: agents
Depends on: P7-01
Owner gate: none
Scope: Search provider behind an interface (Tavily first, as in New Keystone); fetch; numbered citations; results marked as outside content.
Proof: Answers cite their sources; fetched pages mark the turn as having read outside content.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

**The contract.** No route, request, reply or event changes shape. Two tools are added to the server's registry, and their results have a fixed form the screen reads.

**Server side**, in a new package `internal/tools/web` (standard library HTTP only):

1. `Searcher`, an interface with one method, and `Tavily`, its first implementation, with New Keystone's request: `POST` to the endpoint, a bearer key, `search_depth`, `max_results`, no answer and no raw content. Results with a URL that is not `http` or `https`, or that carries a user name, are dropped; titles and snippets are cut. Status 401 and 403 say the key was refused, 429, 432 and 433 say a limit was reached, anything else says the service is unavailable. An answer larger than the cap is refused. No error, result or log line carries the key, and a key the service echoes back is removed from what the model reads.
2. `web_search` (`query`, optional `count`) and `web_fetch` (`url`), both read-only. The key is read on each call from the person's vault as `provider.tavily`. With none, the tool answers "Web search is not set up: store a Tavily key as provider.tavily." as an error result, so the turn goes on and the person reads it on the step. A query that looks like it holds a credential is not sent.
3. `web_fetch` refuses any address that is not public: loopback, private, link-local, shared (100.64.0.0/10), multicast, unspecified and reserved ranges, IPv4 and IPv6. The check is in the dialer, on the address really connected to, so a name that resolves to a home address is refused, and so is every redirect, each of which is dialled again. It ignores the environment's proxy, takes `http` and `https` only, follows a few redirects, reads at most the size cap, and returns the page's readable text (HTML tags, scripts and styles dropped) cut at a setting.
4. **Numbered citations.** Each result is a block `[n] title`, its URL on the next line, and its text. Numbers run on through one turn (a second search, or a fetched page, continues from the last number), so `[n]` names one source in a reply. The tool finds the turn's start from the chat record and keeps the count in memory.
5. **Outside content.** Every successful result opens with a mark line, `[Outside content]`, and a sentence telling the model the text is from the web and is information, never instructions. A new file in `internal/tools`, `outside.go`, exports the mark and `IsOutside(block)`, so the tool loop, the record and the approval policy can tell a result that brought outside content into the turn. P7-02 (in its own worktree) plans a `Tool.Inside` flag, with every tool that does not set it counted as outside; the web tools set nothing, so they are outside under it too. The join is in the journal.
6. Settings, added to `settings/agents.json`, install scope, read by `app.ReadConfig` and passed in: the search endpoint, depth, default and most results, snippet length, timeout and size cap; the fetch timeout, size cap, most characters and most redirects. Registered by added lines in `internal/app/tools.go`. `internal/tools/web` goes on the `offline` check's list.

**The screen.** `steps.ts` gets rows for both tools ("Searched the web for “…”", "5 results"; "Read example.org"), and "not set up" as a plain tag. A new file, `citations.tsx`, reads a reply's web results into numbered sources, and the Markdown reader draws a `[n]` that names one as a small link to it, with the title on hover. A `[n]` with no source stays text. `Conversation.tsx` gets the few lines that hand the reply's sources down. No edit to `Chat.tsx` or `api.ts`.

**Proof.**

- `go test ./internal/tools/web`: the request's shape and key, the numbered results and their caps, each refusal status, a missing key, the key never in a result or error, numbering through a turn, every private range and a name and a redirect that lead to one refused, the readable text and its caps, and the outside mark on every result.
- A test tenant recorded turn, `turns/web-search.json`: the fake model searches the web and answers citing [1] and [2].
- `verify`: a step in `calls recorded` runs it on the real server with the search pointed at a stand-in on this machine and a stored invented key: the call is in the audit log, its result is outside content, and each citation in the answer is a source the stand-in returned. `no keys in output` gains a web search surface: stand-ins that echo the key in errors and results.
- In a browser on port 7951, as the test tenant, against a server from this worktree with the search pointed at a stand-in.

**What is left**, for a session that picks this up (updated at each commit):

- Done: the package, the tools and their tests; the settings; the registration; `outside.go`; the verify steps (`calls recorded` and `no keys in output`); a first `docs/architecture/web-search.md`. `verify` is 19 of 19.
- Next: the screen (`steps.ts` rows, `citations.tsx`, the `[n]` branch in `Markdown.tsx`, a few lines in `Conversation.tsx`), built and checked in a browser on port 7951; commit.
- Then: the screen's part in `web.md` and `web-search.md`, the runbook section, decisions 0250 to 0253 (linked from `web-search.md` already) with overview rows, the journal agents 003, the status; commit.

