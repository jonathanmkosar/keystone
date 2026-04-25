"""Pull a stone package into ``stones_pkg/<name>/``.

The runtime treats ``stones_pkg/`` as a drop-in folder: anything there
that contains ``config.json`` + ``schema.sql`` becomes an available stone.
Right now the reference stones (``yahoo_fantasy_baseball_stone``,
``personal_accountant_stone``) ship in this repo. Once they graduate to their
own GitHub repos, this script is the convenience wrapper that fetches them.

Usage:
    uv run python scripts/fetch_stone.py <name> <git-url> [<ref>]

Example:
    uv run python scripts/fetch_stone.py yahoo_fantasy_baseball_stone \
        https://github.com/<you>/yahoo_fantasy_baseball_stone.git

This is just a thin wrapper around ``git clone`` so you don't have to
remember where the target directory is. Manual ``git clone <url> stones_pkg/<name>``
works just as well.
"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent


def main() -> int:
    if len(sys.argv) < 3:
        print(__doc__, file=sys.stderr)
        return 2

    name = sys.argv[1]
    url = sys.argv[2]
    ref = sys.argv[3] if len(sys.argv) > 3 else None

    if not shutil.which("git"):
        print("git not found on PATH. Install git and retry.", file=sys.stderr)
        return 1

    pkg_root = Path(
        os.environ.get("KEYSTONE_STONES_PKG_DIR", REPO_ROOT / "stones_pkg")
    ).expanduser().resolve()
    pkg_root.mkdir(parents=True, exist_ok=True)
    target = pkg_root / name

    if target.exists():
        print(
            f"{target} already exists. Remove it first or use 'git -C {target} pull' "
            "to update.",
            file=sys.stderr,
        )
        return 1

    cmd = ["git", "clone", url, str(target)]
    if ref:
        cmd[2:2] = ["--branch", ref]

    print(f"$ {' '.join(cmd)}")
    result = subprocess.run(cmd, check=False)
    if result.returncode != 0:
        return result.returncode

    if not (target / "config.json").exists():
        print(
            f"Warning: cloned repo at {target} has no config.json -- "
            "this does not look like a valid stone package.",
            file=sys.stderr,
        )

    print(f"\nProfile package installed at {target}.")
    print(f"Run: uv run python scripts/bootstrap_stones.py")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
