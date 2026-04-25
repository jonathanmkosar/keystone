"""Thin wrapper around `keyring` for our credential layout.

Service: 'keystone'

Username conventions:
  - 'openai'                          -> OpenAI API key
  - 'simplefin:setup'                 -> SimpleFIN setup token (one-time)
  - 'simplefin:access'                -> SimpleFIN access URL (basic auth in URL)
  - 'yahoo:client_id'                 -> Yahoo OAuth client id
  - 'yahoo:client_secret'             -> Yahoo OAuth client secret
  - 'yahoo:refresh_token'             -> Yahoo refresh token (long lived)

Backwards compatibility: this module previously used the service name
'personal-mcp'. Reads transparently fall back to that legacy service if the
canonical 'keystone' service has no entry, and on a successful fallback read
we migrate the value forward so subsequent reads are fast and the legacy
service can be wiped at the user's leisure.
"""

from __future__ import annotations

import logging

import keyring

SERVICE = "keystone"
LEGACY_SERVICE = "personal-mcp"

log = logging.getLogger(__name__)


def _get_with_fallback(username: str) -> str | None:
    value = keyring.get_password(SERVICE, username)
    if value is not None:
        return value
    legacy = keyring.get_password(LEGACY_SERVICE, username)
    if legacy is not None:
        log.info(
            "keychain: migrating %r from legacy service %r to %r",
            username,
            LEGACY_SERVICE,
            SERVICE,
        )
        try:
            keyring.set_password(SERVICE, username, legacy)
        except Exception as exc:
            log.warning("keychain: failed to copy %r forward: %s", username, exc)
        return legacy
    return None


def get(username: str) -> str | None:
    try:
        return _get_with_fallback(username)
    except Exception as exc:
        log.warning("keyring.get(%r) failed: %s", username, exc)
        return None


def set_(username: str, value: str) -> None:
    keyring.set_password(SERVICE, username, value)


def delete(username: str) -> None:
    try:
        keyring.delete_password(SERVICE, username)
    except keyring.errors.PasswordDeleteError:
        pass


def require(username: str) -> str:
    value = get(username)
    if not value:
        raise RuntimeError(
            f"Missing credential {username!r} in keychain. "
            f"Set it with: keyring set {SERVICE} {username}"
        )
    return value
