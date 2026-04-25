"""Per-identity token-bucket rate limiter, applied as ASGI middleware.

Cloudflare WAF is the primary rate limiter (free tier supports per-IP rules).
This is a backstop at the origin so that even authenticated requests can't
hammer the local server. Identity is derived from the Cf-Access-Jwt-Assertion
claims when present, otherwise the client IP.

Defaults: 60 requests / minute per identity. Returns 429 with Retry-After when
exceeded.
"""

from __future__ import annotations

import threading
import time
from dataclasses import dataclass

from starlette.types import ASGIApp, Receive, Scope, Send


@dataclass
class _Bucket:
    tokens: float
    updated_at: float


class TokenBucketLimiter:
    """In-process token bucket.

    capacity: max tokens (burst).
    refill_rate: tokens added per second.
    """

    def __init__(self, capacity: int = 60, refill_rate: float = 1.0) -> None:
        self.capacity = capacity
        self.refill_rate = refill_rate
        self._buckets: dict[str, _Bucket] = {}
        self._lock = threading.Lock()

    def allow(self, key: str) -> tuple[bool, float]:
        """Return (allowed, retry_after_seconds)."""
        now = time.monotonic()
        with self._lock:
            bucket = self._buckets.get(key)
            if bucket is None:
                bucket = _Bucket(tokens=float(self.capacity), updated_at=now)
                self._buckets[key] = bucket
            else:
                elapsed = now - bucket.updated_at
                bucket.tokens = min(
                    self.capacity, bucket.tokens + elapsed * self.refill_rate
                )
                bucket.updated_at = now
            if bucket.tokens >= 1.0:
                bucket.tokens -= 1.0
                return True, 0.0
            needed = 1.0 - bucket.tokens
            retry_after = (
                needed / self.refill_rate if self.refill_rate > 0 else float("inf")
            )
            return False, retry_after


class RateLimitMiddleware:
    """ASGI middleware that enforces a per-identity token bucket."""

    def __init__(
        self,
        app: ASGIApp,
        capacity: int = 60,
        refill_rate: float = 1.0,
    ) -> None:
        self._app = app
        self._limiter = TokenBucketLimiter(capacity=capacity, refill_rate=refill_rate)

    @staticmethod
    def _identity(scope: Scope) -> str:
        state = scope.get("state") or {}
        claims = state.get("cf_access_claims") if isinstance(state, dict) else None
        if isinstance(claims, dict):
            sub = claims.get("sub") or claims.get("email")
            if sub:
                return f"jwt:{sub}"
        client = scope.get("client")
        if client:
            return f"ip:{client[0]}"
        return "anon"

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self._app(scope, receive, send)
            return

        path = scope.get("path", "")
        if path in ("/", "/healthz"):
            await self._app(scope, receive, send)
            return

        key = self._identity(scope)
        allowed, retry_after = self._limiter.allow(key)
        if allowed:
            await self._app(scope, receive, send)
            return

        body = b'{"error": "rate limit exceeded"}'
        retry_seconds = (
            max(1, int(retry_after) + 1) if retry_after != float("inf") else 60
        )
        await send(
            {
                "type": "http.response.start",
                "status": 429,
                "headers": [
                    (b"content-type", b"application/json"),
                    (b"retry-after", str(retry_seconds).encode()),
                    (b"content-length", str(len(body)).encode()),
                ],
            }
        )
        await send({"type": "http.response.body", "body": body, "more_body": False})
