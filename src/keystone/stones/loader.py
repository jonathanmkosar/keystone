"""Make stone packages importable by their bare package name.

Stone packages live as drop-in folders under `stones_pkg/<name>/`.
The core runtime adds that directory to `sys.path` once at startup so
handlers in `config.json` can be referenced as `<name>.handlers.X` rather
than full dotted paths inside the core source tree.

Idempotent: repeated calls do not re-add the path.
"""

from __future__ import annotations

import sys
from pathlib import Path

from keystone.config import SETTINGS


def ensure_stone_packages_on_path() -> Path:
    """Prepend the stone-packages dir to sys.path. Returns the resolved dir.

    Safe to call from anywhere (cli.py, bootstrap script, tests). Creates the
    directory if missing so first-run feels friendly; the registry will return
    an empty list of stones in that case.
    """

    pkg_dir = SETTINGS.stone_packages_dir
    pkg_dir.mkdir(parents=True, exist_ok=True)
    s = str(pkg_dir)
    if s not in sys.path:
        sys.path.insert(0, s)
    return pkg_dir
