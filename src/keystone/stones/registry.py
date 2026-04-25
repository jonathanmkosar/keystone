"""Discover stones on disk and cache loaded instances.

Stones have two related directories:
  - stone-packages dir (env: ``KEYSTONE_STONES_PKG_DIR``, default
    ``./stones_pkg``): drop-in code packages, one folder per stone.
    Added to ``sys.path`` at startup so handler dotted paths can be referenced
    by bare package name.
  - stone-data dir    (env: ``KEYSTONE_STONES_DIR``, default
    ``./stones``): per-user data with the live ``config.json``,
    ``data.db``, ``memory.db``, ``docs/``. The registry resolves a stone
    by reading the live config from here.
"""

from __future__ import annotations

import threading
from pathlib import Path

from keystone.config import SETTINGS
from keystone.stones.base import Stone, load_stone
from keystone.stones.loader import ensure_stone_packages_on_path


class StoneNotFound(KeyError):
    """Raised when a stone directory does not exist."""


class _Registry:
    def __init__(self, root: Path) -> None:
        self._root = root
        self._cache: dict[str, Stone] = {}
        self._lock = threading.Lock()

    @property
    def root(self) -> Path:
        return self._root

    def list_names(self) -> list[str]:
        if not self._root.exists():
            return []
        return sorted(
            p.name
            for p in self._root.iterdir()
            if p.is_dir() and (p / "config.json").exists()
        )

    def get(self, name: str) -> Stone:
        with self._lock:
            cached = self._cache.get(name)
            if cached is not None:
                return cached
            stone_root = self._root / name
            if not stone_root.is_dir():
                raise StoneNotFound(name)
            stone = load_stone(name, stone_root)
            self._cache[name] = stone
            return stone

    def reload(self, name: str) -> Stone:
        with self._lock:
            self._cache.pop(name, None)
        return self.get(name)


ensure_stone_packages_on_path()
REGISTRY = _Registry(SETTINGS.stones_dir)
