"""Stdio transport: one process per stone, launched by Claude Desktop."""

from __future__ import annotations

import logging

from keystone.stones.registry import REGISTRY
from keystone.server import build_server

log = logging.getLogger(__name__)


def run_stdio(stone_name: str) -> None:
    stone = REGISTRY.get(stone_name)
    log.info("Starting stdio transport for stone %s", stone.name)
    mcp = build_server(stone)
    mcp.run("stdio")
