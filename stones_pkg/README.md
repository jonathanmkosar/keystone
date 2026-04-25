# stones_pkg/

Drop stone packages here. Each stone is a folder with at least:

```
stones_pkg/<name>/
  __init__.py
  config.json
  schema.sql
  handlers.py
  README.md
```

The runtime auto-discovers any folder under this directory that contains a `config.json`. See [`docs/WRITING_A_STONE.md`](../docs/WRITING_A_STONE.md) for the full contract.

## Pulling in a published stone

```powershell
uv run python scripts/fetch_stone.py <stone_name> https://github.com/<org>/<stone_repo>.git
uv run python scripts/bootstrap_stones.py
```

This folder is tracked in git so its presence is guaranteed in fresh clones, but the individual stone packages dropped in here are at your discretion — some users may clone stones as git submodules, others may `cp -r` them.
