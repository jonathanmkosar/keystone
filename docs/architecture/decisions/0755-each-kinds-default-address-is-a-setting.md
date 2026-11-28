# 0755. Each kind's default address is a setting

November 28, 2026 · decided in P7-07

## The question

An entry's endpoint is optional: empty is the kind's default (`SPEC-web-search.md`, "Providers"). Tavily's default is the setting `agents.web_search_endpoint`. Where are Brave's, Exa's and Serper's?

## The decision

Three install settings in `settings/agents.json`, each a full search address as Tavily's is:

| Key | Default |
| --- | --- |
| `web.brave_endpoint` | `https://api.search.brave.com/res/v1/web/search` |
| `web.exa_endpoint` | `https://api.exa.ai/search` |
| `web.serper_endpoint` | `https://google.serper.dev/search` |

`web.ReadOptions` reads them into `Options.Endpoints`, and each must be an http or https address with no user name. An entry with an endpoint of its own still searches there, at its base with the kind's path (`/res/v1/web/search` for Brave, `/search` for the others), as Tavily's entries do. SearXNG has no default: its entry names its own.

## Why

- **A value someone might change is a setting** (`AGENTS.md`). A provider can move its API, and the install can point every entry of a kind at a stand-in or a proxy without editing each.
- **The same shape as Tavily's**, so the four keyed kinds read alike.

## What it costs

- Three more settings. A setting emptied by hand is refused when the server starts, rather than leaving the kind without a default.
