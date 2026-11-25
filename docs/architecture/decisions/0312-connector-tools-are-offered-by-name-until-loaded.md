# 0312. A connector's tools are offered by name until loaded, and what is loaded is read from the chat's branch

November 25, 2026 · decided in P7-04

## The question

The plan says "most tool definitions sent as names until needed". A connector can list dozens of tools, each with a schema of hundreds of tokens, and a person can have twenty connectors. How is a model offered a tool it may never use, and how does it get the definition when it does?

## The decision

**Every connector tool is offered as a real tool, with a stub.** Its name, one line of its description cut at `agents.connector_line_chars` with the connector's label and "load_tools first", and a schema that takes any object. So the model knows each tool exists and can name it, and the call stays small.

**`load_tools` gives full definitions.** A tool of the turn's own (not in the server's registry), offered while any connector tool is not loaded, taking `names`, up to `agents.load_tools_max`. Its result is each tool's full description and its schema.

**What is loaded is read from the chat's branch.** A tool named in a call of `load_tools` whose result is in the branch, and is not an error, is offered with its full definition from then on. Nothing is stored for it: an edit that goes back before the load forgets it, as it should, and a restart changes nothing.

**`load_tools` only reads.** What it changes is which definitions later calls of the chat are sent, which is the chat's own record, so it runs without asking. Its result holds the servers' own words, so it is outside content, with the mark, unless every connector offered is marked otherwise.

**Always by name first**, with no threshold below which full definitions are sent.

## Why

- A stub that is a real tool works with every adapter and every provider, needs nothing provider-specific, and lets the model call a tool directly if it already knows how; the server validates the arguments.
- Reading the loaded set from the branch keeps one truth, the record, and needs no table or migration step to keep in step with edits and branches.
- The tools part of a call changes only when a tool is loaded, so the start of a call stays the same between loads, which is what a provider's cache needs (P5-03).
- A threshold would make two ways a chat can look, and the proof and the screen would have to cover both. One way is simpler; a threshold can be added as a setting.

## What it costs

- A first use of a connector tool usually takes one more model call, for `load_tools`.
- A model that calls a stub with wrong arguments gets the server's error and then loads it: a wasted step.
- The one-line stubs are the servers' words in every call. They are offered as tools are by every MCP client.
