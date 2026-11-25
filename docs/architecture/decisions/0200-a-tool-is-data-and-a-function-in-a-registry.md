# 0200. A tool is data and a function in a registry made by plain calls, and the registry is the one place a tool runs

November 25, 2026 · decided in P7-01

## The question

The tool loop needs tools, and later items bring many more: web search, connectors, skills, artifacts, recall's lookups. How is a tool described, how does it get into the server, and where are the rules every tool call must keep (record it, ask the approval rule, survive a tool that breaks) enforced?

## The decision

**A tool is a struct**: a name, a description, a JSON schema for its input, a flag that says it only reads, and one function, `Run(ctx, Input) (string, error)`. No interface to implement and no class to extend.

**A registry is made with `tools.New()` and filled with `Add`**, by plain calls from where the server is put together: `app.Tools` in `internal/app/tools.go`. There is no global registry and no `init` that registers a tool. A test makes its own registry, and so does the `approval` check in `verify`, through `app.Config.Tools`.

**`Registry.Run` is the only way a tool runs.** It writes the audit entry before the tool runs and the entry for its end, asks `approval.Check`, runs the tool in a goroutine of its own under the turn's context and a timeout, turns a failure, a panic or a timeout into an error result, and cuts a long result. It always returns a `tool_result` block a provider takes. `Decline` answers a call without running anything and records it the same way.

**The package reads no setting.** The timeout and the most characters of a result come in each `Call`, read by the turn from `settings/agents.json`. That keeps `internal/tools` a guard of fixed rule 2 in `verify`.

**A tool returns text.** What the model reads is a string, put in one text block.

## Why

- The plan prefers plain functions and data. A tool written as a struct literal is read in one place, and a list of them is a list.
- A global registry filled by `init` would put every tool in every test that imports the package, and a test could not take one out. Plain calls make the server's tools a list in one file, which is where a reader looks.
- Fixed rules 2 and 4 hold for every tool only if no tool can be run any other way. One function that every call goes through is easy to check: nothing else calls a tool's `Run`.
- A tool that breaks must not break the turn, and a tool that ignores its context must not hold a stopped turn open. Doing that once, in the registry, means no tool author has to remember it.
- Every provider takes text in a tool result, and nothing built yet returns anything else. An image or a file result can be added to `Input` and the return when a tool needs one.

## What it costs

- A tool that ignores its context keeps running in its goroutine after the turn has moved on. The turn does not wait for it, and its result is thrown away. A tool that holds something open must watch its context.
- The registry is made when the server starts. A tool cannot be added to a running server, and a person cannot add one of their own. Connectors and skills (P7-04, P7-05) decide how theirs are offered: a registry for each tenant, or a filter over this one.
- Every tool in the registry is offered to every model with the `tools` capability, all or none. A choice of tools for each chat or person is later work.
