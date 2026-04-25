"""Stone model: filesystem layout, config schema, handler binding."""

from __future__ import annotations

import importlib
import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable


@dataclass
class ToolSpec:
    """Stone-specific tool declaration loaded from config.json."""

    name: str
    description: str
    handler: str  # dotted path: "keystone.stones.financial.handlers.sync_transactions"
    input_schema: dict[str, Any] = field(default_factory=dict)


@dataclass
class StoneConfig:
    name: str
    description: str
    schema_description: str
    system_prompt_hints: list[str]
    tools: list[ToolSpec]
    # Extra subdomains the HTTP transport should accept for this stone in
    # addition to its canonical name. Useful when the public DNS subdomain is
    # shorter or already established (e.g. `fantasy.example.com` routing to
    # `yahoo_fantasy_baseball_stone`). Optional; defaults to no aliases.
    http_subdomain_aliases: list[str] = field(default_factory=list)


@dataclass
class Stone:
    """Runtime view of a stone directory."""

    name: str
    root: Path
    config: StoneConfig

    @property
    def data_db(self) -> Path:
        return self.root / "data.db"

    @property
    def memory_db(self) -> Path:
        return self.root / "memory.db"

    @property
    def docs_dir(self) -> Path:
        return self.root / "docs"

    @property
    def tokens_path(self) -> Path:
        return self.root / "tokens.json"

    def resolve_handler(self, dotted: str) -> Callable[..., Any]:
        module_name, _, attr = dotted.rpartition(".")
        if not module_name:
            raise ValueError(f"Invalid handler path: {dotted!r}")
        module = importlib.import_module(module_name)
        return getattr(module, attr)


def load_stone_config(config_path: Path) -> StoneConfig:
    raw = json.loads(config_path.read_text(encoding="utf-8"))
    tools = [
        ToolSpec(
            name=t["name"],
            description=t["description"],
            handler=t["handler"],
            input_schema=t.get("input_schema", {}),
        )
        for t in raw.get("tools", [])
    ]
    return StoneConfig(
        name=raw["name"],
        description=raw.get("description", ""),
        schema_description=raw.get("schema_description", ""),
        system_prompt_hints=raw.get("system_prompt_hints", []),
        tools=tools,
        http_subdomain_aliases=list(raw.get("http_subdomain_aliases", [])),
    )


def load_stone(name: str, root: Path) -> Stone:
    config_path = root / "config.json"
    if not config_path.exists():
        raise FileNotFoundError(
            f"Stone {name!r} has no config.json at {config_path}"
        )
    return Stone(name=name, root=root, config=load_stone_config(config_path))
