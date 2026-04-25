# Keystone

> Personal data should belong to the person who generated it.

Keystone is a local-first runtime for hosting **stones** — small, swappable MCP packages that expose your tools and data to Claude (or any MCP client) over stdio or HTTP. The runtime is the keystone; each domain you care about (your finances, your fantasy league, your reading list) is a stone you slot in.

Out of the box every stone gets:

- **Persistent memory** — `save_memory` / `get_memory` / `save_rule`, backed by SQLite (`memory.db`) so anything Claude learns about you survives across conversations.
- **Read-only SQL** — `run_sql`, `list_tables`, `describe_table` against the stone's `data.db`.
- **Semantic search** — `semantic_search` + `get_document` using `sqlite-vec` for nearest-neighbour lookup over indexed transactions, observations, and documents.

Stones live as **drop-in folders** under `stones_pkg/`. The runtime auto-discovers anything that ships a `config.json` and a `schema.sql`. Stones are meant to live in their own repos — you bring one in with a single `git clone` into `stones_pkg/<name>/`.

This repo is **the framework only**. It ships with no stones. Add one with `scripts/fetch_stone.py` (see below) or by writing your own — the full contract is in [docs/WRITING_A_STONE.md](docs/WRITING_A_STONE.md).

## Quickstart

```powershell
# 1. Install uv (one-time): https://docs.astral.sh/uv/
# 2. Sync dependencies
uv sync

# 3. Bootstrap each stone's data directory (idempotent)
uv run python scripts/bootstrap_stones.py

# 4. Pull in a stone (example — replace with the stone you want)
uv run python scripts/fetch_stone.py <stone_name> https://github.com/<org>/<stone_repo>.git
uv run python scripts/bootstrap_stones.py

# 5. Stash secrets in the OS keychain (Keychain on macOS, Credential Manager on Windows)
uv run python -c "import keyring; keyring.set_password('keystone', 'openai', 'sk-...')"

# 6. Run a stone over stdio (for Claude Desktop)
uv run keystone --stone <stone_name> --transport stdio
```

## Architecture

One Python process, two transports:

- **stdio** — launched per-stone by Claude Desktop / any MCP client.
- **HTTP** — single Starlette app on `127.0.0.1:8787`; stone chosen by request `Host` header (e.g. `<stone>.<your-domain>`) once Cloudflare Tunnel is fronting it.

```
src/keystone/        the framework (generic memory + SQL + semantic search)
stones_pkg/<name>/   stone packages (handlers, schema, config)
stones/<name>/       per-user data (data.db, memory.db, docs/) — gitignored
```

The core never imports anything stone-specific. At startup it adds `stones_pkg/` to `sys.path` so each stone is importable by its bare package name. Handlers in `config.json` are referenced as `<stone_name>.handlers.X` and resolved with `importlib`.

## Generic tools (every stone inherits these)

Every stone gets these out of the box from `keystone.tools.generic`:

| Tool | What it does |
| --- | --- |
| `save_memory(content, type, entity?, source?)` | Append a fact, preference, observation, or scheduled report to `memory.db`. Embeds the content if `OPENAI_API_KEY` is configured. |
| `get_memory(query?, type?, since?, limit?)` | Retrieve persistent observations. Pass `query` for semantic search; pass `type=` / `since=` to scope. |
| `save_rule(pattern, value)` | Upsert a structured rule (used by stone-specific tools, e.g. categorisation rules). |
| `run_sql(query)` | Read-only SELECT against the stone's `data.db`. INSERT / UPDATE / DELETE / DDL / PRAGMA are rejected. |
| `semantic_search(query, scope?, limit?)` | Vector nearest-neighbour search across `data.db` and `memory.db` semantic indexes. Returns pointers; hydrate with `get_document`. |
| `get_document(source_ref)` | Fetch the full content of a document, transaction, or observation by `source_ref` (e.g. `doc:2024-W2.pdf`, `tx:abc123`, `obs:42`). |
| `list_tables()` | Enumerate tables and views across `data.db` + `memory.db`. |
| `describe_table(name)` | Columns, types, primary keys, not-null flags. |

In addition, two MCP **resources** are auto-loaded by every client on connect:

- `schema://current` — the stone's SQL schema and conventions (from `config.json`'s `schema_description`).
- `memory://about-the-user` — facts + preferences + recent scheduled reports for the stone.

Together these make it cheap for the model to orient itself at the start of every conversation without burning tokens on tool calls.

## How stones work

A stone is a folder under `stones_pkg/<name>/` containing:

```
stones_pkg/<name>/
  __init__.py        # marks it as a Python package
  config.json        # name, description, schema_description, system_prompt_hints, tools[]
  schema.sql         # applied to data.db on bootstrap
  handlers.py        # the Python functions referenced from config.json
  README.md          # what this stone does (rendered when extracted to its own repo)
  tests/             # optional: pytest collected automatically
```

See [docs/WRITING_A_STONE.md](docs/WRITING_A_STONE.md) for the full contract and a minimal example.

## Available stones

No stones ship with the core runtime. See the [stones list](#) *(coming soon)* for published stones, or build your own against the contract in [docs/WRITING_A_STONE.md](docs/WRITING_A_STONE.md).

## Pulling a stone from another repo

Drop a stone into this keystone checkout with:

```powershell
uv run python scripts/fetch_stone.py <stone_name> https://github.com/<you>/<stone_repo>.git
uv run python scripts/bootstrap_stones.py
```

`fetch_stone.py` is a thin wrapper around `git clone <url> stones_pkg/<name>`. Manual clone works just as well.

## Client wiring (Claude Desktop)

`%APPDATA%\Claude\claude_desktop_config.json` (Windows) or `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS):

```json
{
  "mcpServers": {
    "<your-server-key>": {
      "command": "uv",
      "args": ["--directory", "<repo_root>",
               "run", "keystone",
               "--stone", "<stone_name>",
               "--transport", "stdio"]
    }
  }
}
```

Replace `<repo_root>` with the absolute path to your clone, `<stone_name>` with the name of a stone under `stones_pkg/`, and `<your-server-key>` with any identifier you want Claude Desktop to show. Add one `mcpServers` entry per stone you want active.

## Configuration

Copy `.env.example` to `.env` and edit. Key knobs:

- `KEYSTONE_STONES_DIR` — where per-user data lives (default `./stones`, gitignored).
- `KEYSTONE_STONES_PKG_DIR` — where stone packages live (default `./stones_pkg`).
- `KEYSTONE_HTTP_HOST` / `KEYSTONE_HTTP_PORT` — HTTP transport bind.
- `KEYSTONE_BASE_DOMAIN` — your domain when running behind Cloudflare Tunnel.
- `CF_ACCESS_TEAM` + `CF_ACCESS_AUD_<STONE_NAME>` — Cloudflare Access JWT validation for remote auth (e.g. `CF_ACCESS_AUD_YAHOO_FANTASY_BASEBALL_STONE`).

Secrets (OpenAI key, SimpleFIN token, Yahoo OAuth refresh token) live in the OS keychain, never in `.env`.

## Development

```powershell
# Run all tests (core + every stone package's tests)
uv run pytest

# Lint
uv run ruff check .
```

## License

Keystone is licensed under the [Functional Source License 1.1, Apache 2.0 Future License](LICENSE) (FSL-1.1-Apache-2.0). Use it for personal, internal, educational, or research purposes; commercial competing use is restricted until each version's two-year change date, after which it auto-converts to Apache 2.0.

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to contribute and the contributor license terms.
