"""Create stone data directories and seed config.json from stone packages.

Discovers every folder under ``stones_pkg/<name>/`` that ships a ``config.json``
and seeds a matching ``stones/<name>/`` user-data directory containing:

  - ``config.json`` (copied from the package, never overwritten)
  - empty ``data.db`` and ``memory.db`` (created if missing)
  - ``docs/`` (created if missing)

Then applies each stone's schema (``stones_pkg/<name>/schema.sql``) and the
generic ``memory.sql`` from the core. Idempotent: safe to re-run.

Usage:
    uv run python scripts/bootstrap_stones.py [path/to/stones-data-dir]
"""

from __future__ import annotations

import json
import shutil
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent

# Bootstrap may be invoked before the runtime is ready; make the in-tree
# packages importable regardless of where the user runs us from.
sys.path.insert(0, str(REPO_ROOT / "src"))


def _discover_packages(pkg_root: Path) -> list[str]:
    if not pkg_root.exists():
        return []
    return sorted(
        p.name
        for p in pkg_root.iterdir()
        if p.is_dir() and (p / "config.json").exists()
    )


def ensure_stone(name: str, pkg_root: Path, dest_root: Path) -> None:
    src_config = pkg_root / name / "config.json"
    if not src_config.exists():
        print(f"[skip] {name}: no package config at {src_config}", file=sys.stderr)
        return

    stone_dir = dest_root / name
    docs_dir = stone_dir / "docs"
    stone_dir.mkdir(parents=True, exist_ok=True)
    docs_dir.mkdir(exist_ok=True)

    dest_config = stone_dir / "config.json"
    if not dest_config.exists():
        shutil.copyfile(src_config, dest_config)
        print(f"[create] {dest_config}")
    else:
        try:
            json.loads(dest_config.read_text(encoding="utf-8"))
            print(f"[keep]   {dest_config} (already exists)")
        except json.JSONDecodeError as exc:
            print(f"[warn]   {dest_config} is invalid JSON: {exc}", file=sys.stderr)

    for db_name in ("data.db", "memory.db"):
        path = stone_dir / db_name
        if not path.exists():
            path.touch()
            print(f"[create] {path} (empty)")


def main() -> int:
    import os

    dest_root_str = sys.argv[1] if len(sys.argv) > 1 else None
    if dest_root_str:
        os.environ["KEYSTONE_STONES_DIR"] = dest_root_str

    from keystone.config import SETTINGS
    from keystone.stones.loader import ensure_stone_packages_on_path
    from keystone.stones.registry import _Registry
    from keystone.storage.migrations import migrate_stone

    pkg_root = ensure_stone_packages_on_path()
    dest_root = SETTINGS.stones_dir
    dest_root.mkdir(parents=True, exist_ok=True)

    names = _discover_packages(pkg_root)
    if not names:
        print(
            f"No stone packages found under {pkg_root}.\n"
            "Drop a stone folder there (containing config.json + schema.sql) "
            "and re-run.",
            file=sys.stderr,
        )
        return 0

    print(f"Bootstrapping into {dest_root} from packages at {pkg_root}")
    print(f"Discovered stone packages: {', '.join(names)}\n")

    for name in names:
        ensure_stone(name, pkg_root, dest_root)

    registry = _Registry(dest_root)
    for name in names:
        try:
            stone = registry.get(name)
        except Exception as exc:
            print(f"[skip] {name}: {exc}", file=sys.stderr)
            continue
        try:
            migrate_stone(stone)
            print(f"[migrate] {name}: schema applied")
        except FileNotFoundError as exc:
            print(f"[warn]   {name}: {exc}", file=sys.stderr)

    print("\nDone.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
