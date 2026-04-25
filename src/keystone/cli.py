"""Command-line entrypoint for the Keystone runtime."""

from __future__ import annotations

import argparse
import logging
import sys

from keystone.config import SETTINGS


def _build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="keystone",
        description="Keystone: a local-first MCP runtime hosting swappable stones.",
    )
    # --stone is the canonical flag; --profile is kept as a hidden alias so any
    # legacy Claude Desktop configs that still pass --profile keep working until
    # the user updates them.
    p.add_argument(
        "--stone",
        "--profile",
        dest="stone",
        help="Stone name (required for stdio transport).",
    )
    p.add_argument(
        "--transport",
        choices=("stdio", "http"),
        default="stdio",
        help="Transport: stdio (Claude Desktop) or http (Cloudflare Tunnel).",
    )
    p.add_argument(
        "--host",
        default=SETTINGS.http_host,
        help="HTTP bind host (http transport only).",
    )
    p.add_argument(
        "--port",
        type=int,
        default=SETTINGS.http_port,
        help="HTTP bind port (http transport only).",
    )
    return p


def _configure_logging() -> None:
    logging.basicConfig(
        level=getattr(logging, SETTINGS.log_level.upper(), logging.INFO),
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
        stream=sys.stderr,
    )


def main(argv: list[str] | None = None) -> int:
    args = _build_parser().parse_args(argv)
    _configure_logging()

    from keystone.stones.loader import ensure_stone_packages_on_path

    ensure_stone_packages_on_path()

    if args.transport == "stdio":
        if not args.stone:
            print("--stone is required for stdio transport", file=sys.stderr)
            return 2
        from keystone.transports.stdio import run_stdio

        run_stdio(args.stone)
        return 0

    from keystone.transports.http import run_http

    run_http(args.host, args.port)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
