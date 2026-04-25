"""Cloudflare Access JWT validation.

Cloudflare Access protects each subdomain at the edge. Authenticated requests
are forwarded to our origin with a `Cf-Access-Jwt-Assertion` header containing
a JWT signed by Cloudflare (RS256) with audience tag set to the Access app's
AUD value. We validate it offline against Cloudflare's JWKS, cached in memory.

This is defence-in-depth — Cloudflare itself enforces auth at the edge — but
it ensures that anyone who happens to reach the local origin (e.g. via a
misconfigured tunnel) still has to present a valid token.

Configure via env vars:
  CF_ACCESS_TEAM              = your team name (the bit before .cloudflareaccess.com)
  CF_ACCESS_AUD_<STONE_NAME>  = the Access app AUD tag for the stone subdomain

The audience variable suffix is lowercased before matching the request
subdomain, so CF_ACCESS_AUD_MY_STONE protects my_stone.<your-domain>.

If CF_ACCESS_TEAM is unset, the middleware is disabled (useful during local
development before the tunnel is set up).
"""

from __future__ import annotations

import logging
import threading
import time
from typing import Any

import httpx
import jwt
from jwt import PyJWKClient
from starlette.requests import Request
from starlette.responses import JSONResponse
from starlette.types import ASGIApp, Receive, Scope, Send

import keystone.config as _config

log = logging.getLogger(__name__)

JWT_HEADER = "cf-access-jwt-assertion"

_jwks_lock = threading.Lock()
_jwks_client: PyJWKClient | None = None
_jwks_team: str | None = None


def _settings():
    return _config.SETTINGS


def _get_jwks_client() -> PyJWKClient | None:
    global _jwks_client, _jwks_team
    team = _settings().cf_access_team
    if not team:
        return None
    with _jwks_lock:
        if _jwks_client is not None and _jwks_team == team:
            return _jwks_client
        certs_url = f"https://{team}.cloudflareaccess.com/cdn-cgi/access/certs"
        # PyJWKClient caches keys for 5 minutes by default.
        _jwks_client = PyJWKClient(certs_url, cache_keys=True, lifespan=300)
        _jwks_team = team
        log.info("Initialised CF Access JWKS client for team %s", team)
        return _jwks_client


def verify(token: str, audience: str) -> dict[str, Any]:
    """Validate a Cloudflare Access JWT and return its payload claims.

    Validation steps (defense in depth — Cloudflare also enforces at the edge):
      1. RS256 signature against Cloudflare's JWKS for our team
      2. `aud` matches the per-app AUD tag we configured
      3. `iss` matches our team's issuer URL exactly
      4. `exp` is in the future, `iat` and `sub` are present
    """
    team = _settings().cf_access_team
    if not team:
        raise RuntimeError("CF_ACCESS_TEAM is not configured")
    client = _get_jwks_client()
    if client is None:
        raise RuntimeError("CF_ACCESS_TEAM is not configured")
    signing_key = client.get_signing_key_from_jwt(token).key
    expected_issuer = f"https://{team}.cloudflareaccess.com"
    return jwt.decode(
        token,
        signing_key,
        algorithms=["RS256"],
        audience=audience,
        issuer=expected_issuer,
        options={"require": ["exp", "iat", "aud", "iss", "sub"]},
    )


def _audience_for_subdomain(subdomain: str) -> str | None:
    return _settings().cf_access_audiences.get(subdomain.lower())


class CloudflareAccessMiddleware:
    """ASGI middleware that requires a valid Cf-Access-Jwt-Assertion on every request.

    If CF_ACCESS_TEAM is unset, the middleware is a no-op (development mode).
    /healthz and / are always allowed to support uptime checks.
    """

    def __init__(self, app: ASGIApp) -> None:
        self._app = app

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http" or _settings().cf_access_team is None:
            await self._app(scope, receive, send)
            return

        path = scope.get("path", "")
        from keystone.transports.http import PUBLIC_PATHS, _extract_stone

        if path in PUBLIC_PATHS:
            await self._app(scope, receive, send)
            return

        subdomain = _extract_stone(scope) or ""
        audience = _audience_for_subdomain(subdomain)
        if not audience:
            await _reject(send, 404, f"Unknown stone subdomain: {subdomain!r}")
            return

        headers = dict(scope.get("headers") or [])
        host = headers.get(b"host", b"").decode("latin-1").split(":")[0]
        resource_metadata = f"https://{host}/.well-known/oauth-protected-resource"

        token_bytes = headers.get(JWT_HEADER.encode())
        if not token_bytes:
            await _reject(
                send,
                401,
                "Missing Cf-Access-Jwt-Assertion header",
                www_authenticate=(
                    f'Bearer realm="{host}", '
                    f'resource_metadata="{resource_metadata}"'
                ),
            )
            return

        try:
            claims = verify(token_bytes.decode("latin-1"), audience=audience)
        except Exception as exc:
            log.warning("Rejected request for %s: %s", subdomain, exc)
            await _reject(
                send,
                401,
                f"Invalid Access token: {exc}",
                www_authenticate=(
                    f'Bearer realm="{host}", error="invalid_token", '
                    f'resource_metadata="{resource_metadata}"'
                ),
            )
            return

        # Stash claims in scope for downstream consumers (rate limiting, audit logs).
        scope.setdefault("state", {})
        scope["state"]["cf_access_claims"] = claims  # type: ignore[index]
        # Audit log: who made this request, against which subdomain, on which path.
        # Keeps a trail without persisting the JWT itself.
        log.info(
            "auth: subdomain=%s subject=%s email=%s path=%s",
            subdomain,
            claims.get("sub"),
            claims.get("email"),
            scope.get("path"),
        )
        await self._app(scope, receive, send)


async def _reject(
    send: Send,
    status: int,
    message: str,
    *,
    www_authenticate: str | None = None,
) -> None:
    body = (
        f'{{"error": "{message}"}}'.encode("utf-8")
    )
    headers: list[tuple[bytes, bytes]] = [
        (b"content-type", b"application/json"),
        (b"content-length", str(len(body)).encode()),
    ]
    if www_authenticate:
        headers.append((b"www-authenticate", www_authenticate.encode("latin-1")))
    await send(
        {"type": "http.response.start", "status": status, "headers": headers}
    )
    await send({"type": "http.response.body", "body": body, "more_body": False})
