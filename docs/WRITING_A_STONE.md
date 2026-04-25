# Writing a stone

A stone is a self-contained Python package that lives under `stones_pkg/<name>/`. The runtime auto-discovers any folder there that ships a `config.json` and a `schema.sql`. Nothing about the stone leaks into the core repo, which means a stone can move to its own GitHub repo at any point without touching the framework.

## The contract

Every stone MUST contain:

```
stones_pkg/<name>/
  __init__.py        # makes it a Python package
  config.json        # name, description, schema_description, system_prompt_hints, tools[]
  schema.sql         # idempotent CREATE TABLE statements applied to data.db
  handlers.py        # the Python functions referenced from config.json (or any module
                     # path that resolves once <name> is on sys.path)
```

It SHOULD also contain:

- `README.md` — describes what this stone does. Renders nicely once the stone is its own repo.
- `tests/` — pytest collected automatically by the runtime's `pyproject.toml` (`testpaths`).
- `scheduler.py` — entrypoint for any Windows Task Scheduler / cron job. Run with `uv run python -m <name>.scheduler`.

`<name>` should be a valid Python package name (snake_case, no dashes). It also becomes the directory name under `stones/` (the user-data dir) and the value of the `--stone` CLI flag.

## config.json

```json
{
  "name": "<name>",
  "http_subdomain_aliases": [],
  "description": "One-line summary of what this stone does. Shown in the system prompt.",
  "schema_description": "Free-form text describing the data.db schema and conventions. Auto-loaded as the schema://current MCP resource.",
  "system_prompt_hints": [
    "First paragraph: when to use which tool family.",
    "Second paragraph: stone-specific quirks.",
    "Each entry becomes a section in the FastMCP server's `instructions` preamble."
  ],
  "tools": [
    {
      "name": "do_something",
      "description": "Description Claude sees in the tool list.",
      "handler": "<name>.handlers.do_something",
      "input_schema": {
        "type": "object",
        "properties": {
          "arg": { "type": "string", "description": "What the arg does." }
        },
        "required": ["arg"]
      }
    }
  ]
}
```

The runtime resolves each `handler` via `importlib`. Because `stones_pkg/` is on `sys.path` at startup, you reference handlers by bare package name (`<name>.handlers.func`) rather than a path inside the core source tree.

### `http_subdomain_aliases` (HTTP transport only)

Optional. By default the HTTP transport routes a request to a stone when the `Host` header's leading subdomain matches the stone's `name` (e.g. `Host: my_stone.example.com` → `my_stone`). Sometimes the public DNS subdomain you want to use is shorter or already established (`fantasy.example.com` → `yahoo_fantasy_baseball_stone`). List those extra subdomains here and the runtime mounts the same MCP sub-app under each one in addition to the canonical name. Aliases are matched first-come-first-served — the first stone to claim a given alias wins; collisions log a warning and are skipped.

## handlers.py

```python
from keystone.stones.base import Stone


def do_something(stone: Stone, arg: str) -> dict:
    """First arg can be ``stone`` and the runtime injects it automatically.

    Drop the ``stone`` parameter if you don't need it; the runtime checks
    the signature and only injects when the first parameter is named ``stone``.
    """
    with stone.data_db.open("rb"):
        ...
    return {"ok": True, "arg": arg}
```

What you get from `stone`:

- `stone.name` — the stone name.
- `stone.root` — the user-data directory (`stones/<name>/`).
- `stone.data_db` — `Path` to `data.db`.
- `stone.memory_db` — `Path` to `memory.db`.
- `stone.docs_dir` — `Path` to `docs/`.
- `stone.tokens_path` — `Path` to `tokens.json` if you want a per-stone token cache (alternative to keychain).

## schema.sql

Idempotent SQL applied to the stone's `data.db` on every `bootstrap_stones.py` run:

```sql
CREATE TABLE IF NOT EXISTS my_table (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at  INTEGER NOT NULL,
    payload     TEXT
);

CREATE INDEX IF NOT EXISTS idx_my_table_created
    ON my_table(created_at DESC);

-- Optional: index stone-specific content for semantic_search.
CREATE VIRTUAL TABLE IF NOT EXISTS semantic_index USING vec0(
    embedding   float[1536],
    +source_type TEXT,
    +source_ref  TEXT
);
```

The generic `memory.sql` (observations, rules, entities, and the memory-side `semantic_index`) is applied separately by the core; stones do not redeclare it.

## Generic tools you inherit for free

Every stone gets these without writing any code:

- `save_memory`, `get_memory`, `save_rule`
- `run_sql`, `list_tables`, `describe_table`
- `semantic_search`, `get_document`

Plus two MCP resources clients auto-load on connect:

- `schema://current` — your `schema_description`.
- `memory://about-the-user` — facts + preferences + recent scheduled reports from `memory.db`.

## Tests

Drop tests under `stones_pkg/<name>/tests/`. Add them to the root `pyproject.toml`'s `[tool.pytest.ini_options].testpaths` list. The runtime's `pythonpath = ["src", "stones_pkg"]` setting makes both core and stone packages importable from tests.

A typical test fixture:

```python
@pytest.fixture
def my_stone(tmp_path: Path, monkeypatch):
    monkeypatch.setenv("KEYSTONE_STONES_DIR", str(tmp_path))
    import importlib, keystone.config as cfg
    importlib.reload(cfg)
    import keystone.stones.registry as reg
    importlib.reload(reg)

    src_config = Path(__file__).resolve().parent.parent / "config.json"
    stone_root = tmp_path / "<name>"
    stone_root.mkdir()
    (stone_root / "config.json").write_text(
        src_config.read_text(encoding="utf-8"), encoding="utf-8"
    )

    from keystone.stones.registry import REGISTRY
    from keystone.storage.migrations import migrate_stone

    stone = REGISTRY.get("<name>")
    migrate_stone(stone)
    return stone
```

## Running locally

```powershell
# Drop the stone under stones_pkg/<name>/, then:
uv run python scripts/bootstrap_stones.py
uv run keystone --stone <name> --transport stdio
```

## Publishing as its own repo

When the stone is mature enough to live independently, copy the folder out, `git init`, and push. To re-install in any keystone checkout:

```powershell
uv run python scripts/fetch_stone.py <name> https://github.com/<you>/<repo>.git
uv run python scripts/bootstrap_stones.py
```

Nothing about the stone package needs to change — the contract is the same whether the package ships in this monorepo or arrives via `git clone`.
