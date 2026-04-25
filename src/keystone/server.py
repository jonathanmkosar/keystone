"""Build a FastMCP server bound to a specific stone."""

from __future__ import annotations

import logging
import os
from typing import Any

from mcp.server.fastmcp import FastMCP
from mcp.server.transport_security import TransportSecuritySettings

from keystone.stones.base import Stone

log = logging.getLogger(__name__)


def _transport_security_for(stone: Stone) -> TransportSecuritySettings:
    """Allow requests from this stone's public subdomain plus localhost.

    The MCP SDK's DNS-rebinding protection rejects unknown Host headers with
    421. When the server runs behind Cloudflare Tunnel, requests arrive with
    the public hostname (e.g. finance.<your-domain>) which must be on the
    allowlist. The list is built from:

    * any explicit hosts in ``KEYSTONE_PUBLIC_HOSTS`` (comma-separated),
    * ``<stone.name>.<KEYSTONE_BASE_DOMAIN>`` (the canonical subdomain),
    * ``<alias>.<KEYSTONE_BASE_DOMAIN>`` for every entry in
      ``stone.config.http_subdomain_aliases`` (so legacy short subdomains
      keep working without disabling the rebinding protection).
    """
    from keystone.config import env_with_fallback

    public_hosts = (env_with_fallback("KEYSTONE_PUBLIC_HOSTS", "") or "").split(",")
    public_hosts = [h.strip() for h in public_hosts if h.strip()]

    base_domain = env_with_fallback("KEYSTONE_BASE_DOMAIN")
    if base_domain:
        public_hosts.append(f"{stone.name}.{base_domain}")
        for alias in stone.config.http_subdomain_aliases:
            if alias:
                public_hosts.append(f"{alias}.{base_domain}")

    # Deduplicate while preserving order so log lines stay deterministic.
    seen: set[str] = set()
    public_hosts = [h for h in public_hosts if not (h in seen or seen.add(h))]

    allowed_hosts = [
        "127.0.0.1:*",
        "localhost:*",
        "[::1]:*",
        *public_hosts,
        *(f"{h}:*" for h in public_hosts),
    ]
    allowed_origins = [
        "http://127.0.0.1:*",
        "http://localhost:*",
        "http://[::1]:*",
        *(f"https://{h}" for h in public_hosts),
        *(f"https://{h}:*" for h in public_hosts),
    ]
    return TransportSecuritySettings(
        enable_dns_rebinding_protection=True,
        allowed_hosts=allowed_hosts,
        allowed_origins=allowed_origins,
    )


def build_server(stone: Stone) -> FastMCP:
    """Construct a FastMCP server with generic tools, stone tools, and resources."""

    instructions = _build_instructions(stone)

    mcp = FastMCP(
        name=f"keystone-{stone.name}",
        instructions=instructions,
        transport_security=_transport_security_for(stone),
    )

    _register_resources(mcp, stone)
    _register_generic_tools(mcp, stone)
    _register_stone_tools(mcp, stone)

    # Count actual registered tools (generic + stone-specific) for log clarity.
    import asyncio as _asyncio

    try:
        registered = len(_asyncio.run(mcp.list_tools()))
    except RuntimeError:
        registered = -1
    log.info(
        "Built MCP server for stone %s (%d tools total, %d stone-specific)",
        stone.name,
        registered,
        len(stone.config.tools),
    )
    return mcp


def _build_instructions(stone: Stone) -> str:
    """Compose the server preamble (FastMCP `instructions`) sent to clients on connect.

    The preamble is the most reliable way to give Claude top-level orientation
    -- system prompts and config.json hints alone aren't always surfaced. We
    include:

      * stone name + one-line description
      * a tiny tool-family map so Claude knows which family to reach for first
      * the data.db / memory.db relationship
      * pointers to the auto-loaded resources (schema://current,
        memory://about-the-user) so Claude knows the schema is already in
        context and doesn't need list_tables to orient
      * an explicit instruction to call get_memory at session start, since the
        about-the-user resource only surfaces type='fact'/'preference' rows
        and silently skips type='observation'
      * the stone-specific system_prompt_hints (richer per-stone guidance)
    """

    stone_tool_names = [t.name for t in stone.config.tools]
    stone_tools_inline = (
        ", ".join(stone_tool_names) if stone_tool_names else "(none)"
    )

    parts: list[str] = [
        f"# keystone '{stone.name}' stone",
        "",
        stone.config.description,
        "",
        "## Orientation -- read this first",
        "",
        "Two SQLite databases back this stone:",
        "  - data.db   -- live stone data (transactions, league cache, ...). "
        "Read via the purpose-built tools below or run_sql.",
        "  - memory.db -- persistent state (observations, rules, entities) that "
        "outlives any single conversation. Read via get_memory; write via save_memory.",
        "",
        "Two MCP resources are auto-exposed (clients should fetch them at connect time):",
        "  - schema://current        -> the SQL schema + conventions for this stone.",
        "  - memory://about-the-user -> facts, preferences, recent reports for this stone.",
        "These cover schema and high-signal user state; you don't need to call "
        "list_tables just to orient.",
        "",
        "## Memory: load at session start, then on demand",
        "",
        "**At the start of every session**, call get_memory(limit=100) with "
        "no other filters to load the full recent context across all types. "
        "The memory://about-the-user resource only surfaces type='fact', "
        "'preference', and recent 'scheduled_report' rows -- it silently "
        "omits plain 'observation' and 'rule' entries (open questions, "
        "in-progress context, strategic rules, prior tool results worth "
        "remembering). get_memory fills that gap. Stone-specific "
        "guidance below shows what each `type=` value means in this domain.",
        "",
        "**During the conversation**, call get_memory whenever you suspect "
        "prior context might exist for the topic at hand -- use the `query` "
        "argument for semantic search, or `type=` / `since=` to scope. It is "
        "cheap (single SQLite read) and the cost of missing relevant "
        "history is much higher than the cost of an extra call.",
        "",
        "**When the user shares anything durable** -- a fact, preference, "
        "rule, decision, or piece of context that should outlast this "
        "conversation -- call save_memory immediately. Don't wait to be "
        "asked. Pair every save_memory with the smallest correct `type`: "
        "'fact' for stable truths, 'preference' for stated likes/dislikes, "
        "'scheduled_report' for daily/weekly digests, 'observation' for "
        "everything else (open questions, working context, mid-task notes).",
        "",
        "## Tool families",
        "",
        f"  Stone-specific (preferred for any '{stone.name}' question): "
        f"{stone_tools_inline}.",
        "  Generic introspection: list_tables, describe_table.",
        "  Generic SQL          : run_sql (read-only SELECT against data.db).",
        "  Generic semantic     : semantic_search + get_document.",
        "  Persistent memory    : get_memory, save_memory.",
        "",
        "Always reach for a stone-specific tool first; fall back to run_sql / "
        "semantic_search only when no purpose-built tool fits.",
        "",
    ]
    preamble = "\n".join(parts)
    # Hints are paragraph-level; join with blank lines so markdown headings
    # inside hints render correctly instead of collapsing onto their bodies.
    hints_block = "\n\n".join(stone.config.system_prompt_hints)
    return preamble + "\n" + hints_block


def _register_resources(mcp: FastMCP, stone: Stone) -> None:
    """Expose the schema description and 'about-the-user' memory as auto-loaded resources."""

    schema_text = stone.config.schema_description or "(no schema description provided)"

    @mcp.resource(
        "schema://current",
        name="stone-schema",
        title=f"{stone.name} schema",
        description="SQL schema and conventions for this stone's data.db.",
        mime_type="text/plain",
    )
    def _schema() -> str:
        return schema_text

    @mcp.resource(
        "memory://about-the-user",
        name="about-the-user",
        title="About the user",
        description=(
            "Persistent facts, preferences, and recent scheduled reports. "
            "Auto-loaded at connection time so Claude has context immediately."
        ),
        mime_type="text/plain",
    )
    def _about_user() -> str:
        try:
            from keystone.tools.generic import format_about_user

            return format_about_user(stone)
        except Exception as exc:  # pragma: no cover - resource must never raise
            log.warning("about-the-user resource failed: %s", exc)
            return "(memory not yet initialized)"


def _register_generic_tools(mcp: FastMCP, stone: Stone) -> None:
    """Bind the six generic tools (run_sql, semantic_search, ...) to this stone."""

    try:
        from keystone.tools import generic
    except ImportError:
        log.warning("Generic tools module not yet implemented; skipping registration.")
        return

    generic.register(mcp, stone)


def _register_stone_tools(mcp: FastMCP, stone: Stone) -> None:
    """Bind tools declared in the stone's config.json to handler callables."""

    for spec in stone.config.tools:
        try:
            handler = stone.resolve_handler(spec.handler)
        except Exception as exc:
            log.error(
                "Failed to resolve tool %s -> %s: %s", spec.name, spec.handler, exc
            )
            continue

        wrapped = _wrap_handler(handler, stone)
        wrapped.__name__ = spec.name
        wrapped.__doc__ = spec.description
        mcp.add_tool(wrapped, name=spec.name, description=spec.description)


def _wrap_handler(handler: Any, stone: Stone) -> Any:
    """Inject `stone` as the first arg if the handler declares it."""

    import inspect

    sig = inspect.signature(handler)
    params = list(sig.parameters.values())
    needs_stone = bool(params) and params[0].name == "stone"

    if not needs_stone:
        return handler

    if inspect.iscoroutinefunction(handler):
        async def async_wrapper(*args: Any, **kwargs: Any) -> Any:
            return await handler(stone, *args, **kwargs)

        async_wrapper.__signature__ = sig.replace(parameters=params[1:])  # type: ignore[attr-defined]
        return async_wrapper

    def sync_wrapper(*args: Any, **kwargs: Any) -> Any:
        return handler(stone, *args, **kwargs)

    sync_wrapper.__signature__ = sig.replace(parameters=params[1:])  # type: ignore[attr-defined]
    return sync_wrapper
