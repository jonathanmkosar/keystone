"""HTTP transport: one process serves every stone, dispatched by Host header.

Architecture:
  - Each discovered stone gets its own FastMCP instance (built once at startup)
    and its own Starlette sub-app exposing /mcp.
  - A top-level Starlette app dispatches incoming requests to the right sub-app
    by extracting the leading subdomain from the Host header.
  - Lifespans are chained so each FastMCP's session manager starts/stops cleanly.
  - Cloudflare Access JWT validation runs as middleware (Phase 7).
  - Per-JWT rate limiting also runs as middleware (Phase 9).
  - Binds to 127.0.0.1 only; Cloudflare Tunnel is the sole external reach.
"""

from __future__ import annotations

import contextlib
import logging
from typing import AsyncIterator

import uvicorn
from starlette.applications import Starlette
from starlette.requests import Request
from starlette.responses import JSONResponse, Response
from starlette.routing import Route
from starlette.types import ASGIApp, Receive, Scope, Send

from keystone.config import SETTINGS
from keystone.stones.registry import REGISTRY
from keystone.server import build_server

log = logging.getLogger(__name__)

# Paths that must remain publicly reachable for MCP OAuth discovery.
# claude.ai (and any MCP client) fetches these unauthenticated to learn
# which OAuth provider issues tokens for this resource.
PUBLIC_PATHS: frozenset[str] = frozenset(
    {
        "/",
        "/healthz",
        "/.well-known/oauth-protected-resource",
        "/.well-known/oauth-protected-resource/mcp",
        "/.well-known/oauth-authorization-server",
        "/.well-known/openid-configuration",
    }
)


def _extract_stone(scope: Scope) -> str | None:
    """Pull the leading subdomain from the Host header.

    finance.example.com -> 'finance'
    fantasy.example.com -> 'fantasy'
    localhost or single-label hosts -> None (caller decides what to do).
    """
    headers = dict(scope.get("headers") or [])
    host_bytes = headers.get(b"host", b"")
    host = host_bytes.decode("latin-1").split(":")[0].lower()
    parts = host.split(".")
    if len(parts) < 2:
        return None
    return parts[0]


def _is_local_host(scope: Scope) -> bool:
    """Treat localhost/127.0.0.1 as trusted for diagnostic endpoints."""
    headers = dict(scope.get("headers") or [])
    host = headers.get(b"host", b"").decode("latin-1").split(":")[0].lower()
    return host in ("localhost", "127.0.0.1", "[::1]", "::1")


class _SubdomainDispatcher:
    """ASGI app that routes to one sub-app per stone based on Host header."""

    def __init__(self, sub_apps: dict[str, ASGIApp]) -> None:
        self._sub_apps = sub_apps

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] not in ("http", "websocket"):
            # lifespan etc. handled by the parent Starlette app's lifespan.
            return
        stone = _extract_stone(scope)
        target = self._sub_apps.get(stone or "")
        if target is None:
            await _send_404(scope, send, stone)
            return
        await target(scope, receive, send)


async def _send_404(scope: Scope, send: Send, stone: str | None) -> None:
    if scope["type"] != "http":
        return
    body = (
        f'{{"error": "unknown stone {stone!r}; '
        f'expected one of: {sorted(REGISTRY.list_names())}"}}'
    ).encode("utf-8")
    await send(
        {
            "type": "http.response.start",
            "status": 404,
            "headers": [
                (b"content-type", b"application/json"),
                (b"content-length", str(len(body)).encode()),
            ],
        }
    )
    await send({"type": "http.response.body", "body": body, "more_body": False})


def build_app() -> Starlette:
    stone_names = REGISTRY.list_names()
    if not stone_names:
        raise RuntimeError(
            f"No stones found under {REGISTRY.root}. Run scripts/bootstrap_stones.py."
        )

    sub_apps: dict[str, ASGIApp] = {}
    fastmcp_instances = []
    for name in stone_names:
        stone = REGISTRY.get(name)
        mcp = build_server(stone)
        app = mcp.streamable_http_app()
        sub_apps[name] = app
        fastmcp_instances.append((name, mcp))
        log.info("Mounted stone %s at subdomain %s", name, name)
        # Mount the same ASGI app under any extra subdomain aliases declared
        # in the stone's config.json.  Lets the public DNS subdomain stay
        # short (e.g. `fantasy.example.com`) while the canonical stone name
        # stays self-describing (e.g. `yahoo_fantasy_baseball_stone`).
        for alias in stone.config.http_subdomain_aliases:
            if not alias:
                continue
            if alias in sub_apps:
                log.warning(
                    "stone %s alias %r collides with an existing mount; skipping",
                    name,
                    alias,
                )
                continue
            sub_apps[alias] = app
            log.info("Mounted stone %s under alias subdomain %s", name, alias)

    dispatcher = _SubdomainDispatcher(sub_apps)

    @contextlib.asynccontextmanager
    async def lifespan(app: Starlette) -> AsyncIterator[None]:
        async with contextlib.AsyncExitStack() as stack:
            for name, mcp in fastmcp_instances:
                await stack.enter_async_context(mcp.session_manager.run())
                log.info("Started session manager for stone %s", name)
            yield

    async def healthz(request: Request) -> Response:
        # Stone names leak our internal architecture; only return them
        # when the request comes from localhost (cloudflared healthcheck or a
        # local operator). Public callers get a minimal liveness ping.
        if _is_local_host(request.scope):
            return JSONResponse({"ok": True, "stones": stone_names})
        return JSONResponse({"ok": True})

    async def root(request: Request) -> Response:
        if _is_local_host(request.scope):
            return JSONResponse(
                {
                    "service": "keystone",
                    "stones": stone_names,
                    "hint": "Connect via subdomain (e.g. finance.<your-domain>/mcp).",
                }
            )
        return JSONResponse({"service": "keystone"})

    async def oauth_protected_resource(request: Request) -> Response:
        """RFC 9728 protected-resource metadata.

        Tells unauthenticated MCP clients which OAuth authorization server
        issues tokens for this resource. claude.ai fetches this on first
        connect to discover Cloudflare Access as the auth provider.
        """
        host = request.headers.get("host", "")
        scheme = request.url.scheme if request.url.scheme in ("http", "https") else "https"
        resource = f"{scheme}://{host}/mcp"
        team = SETTINGS.cf_access_team
        if team:
            auth_server = f"https://{team}.cloudflareaccess.com"
        else:
            auth_server = f"{scheme}://{host}"
        return JSONResponse(
            {
                "resource": resource,
                "authorization_servers": [auth_server],
                "bearer_methods_supported": ["header"],
                "scopes_supported": [],
            }
        )

    async def oauth_authorization_server(_: Request) -> Response:
        """Mirror Cloudflare Access's authorization-server metadata.

        Some MCP clients fetch this from the resource origin instead of from
        the authorization server itself. Proxy a minimal copy that points back
        to Cloudflare's real endpoints.
        """
        team = SETTINGS.cf_access_team
        if not team:
            return JSONResponse(
                {"error": "CF_ACCESS_TEAM not configured"}, status_code=503
            )
        base = f"https://{team}.cloudflareaccess.com"
        return JSONResponse(
            {
                "issuer": base,
                "authorization_endpoint": f"{base}/cdn-cgi/access/oauth/authorization",
                "token_endpoint": f"{base}/cdn-cgi/access/oauth/token",
                "registration_endpoint": f"{base}/cdn-cgi/access/oauth/registration",
                "revocation_endpoint": f"{base}/cdn-cgi/access/oauth/revoke",
                "response_types_supported": ["code"],
                "response_modes_supported": ["query"],
                "grant_types_supported": ["authorization_code", "refresh_token"],
                "code_challenge_methods_supported": ["S256"],
                "token_endpoint_auth_methods_supported": [
                    "client_secret_basic",
                    "client_secret_post",
                    "none",
                ],
            }
        )

    routes = [
        Route("/healthz", healthz, methods=["GET"]),
        Route("/", root, methods=["GET"]),
        Route(
            "/.well-known/oauth-protected-resource",
            oauth_protected_resource,
            methods=["GET"],
        ),
        Route(
            "/.well-known/oauth-protected-resource/mcp",
            oauth_protected_resource,
            methods=["GET"],
        ),
        Route(
            "/.well-known/oauth-authorization-server",
            oauth_authorization_server,
            methods=["GET"],
        ),
    ]
    app = Starlette(routes=routes, lifespan=lifespan)

    # Wrap the Starlette app so Host-based dispatch wins for every other path.
    return _DispatchingApp(app, dispatcher)


class _DispatchingApp:
    """Routes /healthz and / to the Starlette routes, everything else to the dispatcher."""

    def __init__(self, starlette_app: Starlette, dispatcher: _SubdomainDispatcher) -> None:
        self._starlette = starlette_app
        self._dispatcher = dispatcher

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] == "lifespan":
            await self._starlette(scope, receive, send)
            return
        if scope["type"] == "http":
            path = scope.get("path", "")
            if path in PUBLIC_PATHS:
                await self._starlette(scope, receive, send)
                return
        await self._dispatcher(scope, receive, send)


def run_http(host: str, port: int) -> None:
    from keystone.auth.cf_access import CloudflareAccessMiddleware
    from keystone.util.rate_limit import RateLimitMiddleware

    app: ASGIApp = build_app()
    # Order is important: outermost runs first.
    # CF Access JWT validation -> rate limit per identity -> dispatch.
    app = RateLimitMiddleware(app, capacity=60, refill_rate=1.0)
    app = CloudflareAccessMiddleware(app)
    log.info("Starting HTTP transport on %s:%d", host, port)
    uvicorn.run(app, host=host, port=port, log_level="info")
