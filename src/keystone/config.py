"""Process-wide configuration loaded from environment variables.

Auto-loads `./.env` (relative to the repo root) on import so users don't have
to remember to source it. Does NOT override values already set in the real
environment, so explicit env vars still win.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path


def _load_dotenv() -> None:
    """Tiny dotenv parser. Avoids adding python-dotenv as a dependency.

    Skipped under pytest so tests can deterministically control the environment.
    """
    if (
        "PYTEST_CURRENT_TEST" in os.environ
        or os.environ.get("KEYSTONE_SKIP_DOTENV")
        or os.environ.get("PERSONAL_MCP_SKIP_DOTENV")
    ):
        return
    candidates = [
        Path.cwd() / ".env",
        Path(__file__).resolve().parents[2] / ".env",  # repo root
    ]
    for path in candidates:
        if not path.is_file():
            continue
        for raw in path.read_text(encoding="utf-8").splitlines():
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            if "=" not in line:
                continue
            key, _, value = line.partition("=")
            key = key.strip()
            value = value.strip().strip('"').strip("'")
            if key and key not in os.environ:
                os.environ[key] = value
        break


_load_dotenv()


# Map: legacy PERSONAL_MCP_* env var name -> canonical KEYSTONE_* env var name.
# Reads via env_with_fallback() consult the new name first; if it's empty
# they consult the legacy name. This keeps old .env files working through
# the rename so users don't have to edit them on upgrade.
#
# IMPORTANT: directory env vars (PROFILES_DIR / PROFILES_PKG_DIR) are
# intentionally NOT in this fallback table.  Their values from the pre-rename
# era point at directories that were renamed during the rebrand
# (./profiles -> ./stones, ./profiles_pkg -> ./stones_pkg) and would
# silently make the runtime read from the wrong path on upgrade.  Users
# upgrading must either accept the new defaults or set the new
# KEYSTONE_STONES_DIR / KEYSTONE_STONES_PKG_DIR explicitly.
LEGACY_ENV_FALLBACKS: dict[str, str] = {
    "KEYSTONE_HTTP_HOST":    "PERSONAL_MCP_HTTP_HOST",
    "KEYSTONE_HTTP_PORT":    "PERSONAL_MCP_HTTP_PORT",
    "KEYSTONE_LOG_LEVEL":    "PERSONAL_MCP_LOG_LEVEL",
    "KEYSTONE_BASE_DOMAIN":  "PERSONAL_MCP_BASE_DOMAIN",
    "KEYSTONE_PUBLIC_HOSTS": "PERSONAL_MCP_PUBLIC_HOSTS",
    "KEYSTONE_SKIP_DOTENV":  "PERSONAL_MCP_SKIP_DOTENV",
}


def env_with_fallback(name: str, default: str | None = None) -> str | None:
    """Read a KEYSTONE_* env var, falling back to its PERSONAL_MCP_* legacy name.

    Use this instead of ``os.environ.get`` for any env var that was renamed in
    the Keystone rebrand so old .env files keep working.
    """

    value = os.environ.get(name)
    if value:
        return value
    legacy = LEGACY_ENV_FALLBACKS.get(name)
    if legacy:
        legacy_value = os.environ.get(legacy)
        if legacy_value:
            return legacy_value
    return default


def _env_path(name: str, default: str) -> Path:
    raw = env_with_fallback(name, default) or default
    return Path(raw).expanduser().resolve()


@dataclass(frozen=True)
class Settings:
    stones_dir: Path
    stone_packages_dir: Path
    http_host: str
    http_port: int
    log_level: str
    openai_embedding_model: str
    cf_access_team: str | None
    cf_access_audiences: dict[str, str]


def load_settings() -> Settings:
    audiences: dict[str, str] = {}
    for key, value in os.environ.items():
        if key.startswith("CF_ACCESS_AUD_") and value:
            audiences[key.removeprefix("CF_ACCESS_AUD_").lower()] = value

    return Settings(
        stones_dir=_env_path("KEYSTONE_STONES_DIR", "./stones"),
        stone_packages_dir=_env_path(
            "KEYSTONE_STONES_PKG_DIR", "./stones_pkg"
        ),
        http_host=env_with_fallback("KEYSTONE_HTTP_HOST", "127.0.0.1") or "127.0.0.1",
        http_port=int(env_with_fallback("KEYSTONE_HTTP_PORT", "8787") or "8787"),
        log_level=env_with_fallback("KEYSTONE_LOG_LEVEL", "INFO") or "INFO",
        openai_embedding_model=os.environ.get(
            "OPENAI_EMBEDDING_MODEL", "text-embedding-3-small"
        ),
        cf_access_team=os.environ.get("CF_ACCESS_TEAM"),
        cf_access_audiences=audiences,
    )


SETTINGS = load_settings()
