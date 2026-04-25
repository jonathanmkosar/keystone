"""CloudflareAccessMiddleware behaviour: dev no-op vs configured rejection."""

from __future__ import annotations

from pathlib import Path

import pytest


TEST_STONE_PKG_DIR = Path(__file__).resolve().parent / "fixtures"
STONE_NAMES = ("test_stone",)


@pytest.fixture
def http_app(tmp_path: Path, monkeypatch):
    monkeypatch.setenv("KEYSTONE_STONES_DIR", str(tmp_path))
    monkeypatch.setenv("KEYSTONE_STONES_PKG_DIR", str(TEST_STONE_PKG_DIR))
    monkeypatch.delenv("CF_ACCESS_TEAM", raising=False)
    import importlib

    import keystone.config as cfg

    importlib.reload(cfg)
    import keystone.stones.registry as reg

    importlib.reload(reg)

    for name in STONE_NAMES:
        d = tmp_path / name
        d.mkdir()
        (d / "config.json").write_text(
            (TEST_STONE_PKG_DIR / name / "config.json").read_text(encoding="utf-8"),
            encoding="utf-8",
        )

    from keystone.stones.registry import REGISTRY
    from keystone.storage.migrations import migrate_stone

    for name in STONE_NAMES:
        migrate_stone(REGISTRY.get(name))

    import keystone.transports.http as http_mod

    importlib.reload(http_mod)
    return http_mod.build_app()


def test_disabled_when_team_unset(http_app, monkeypatch):
    """When CF_ACCESS_TEAM is unset, middleware passes through unchanged."""
    monkeypatch.delenv("CF_ACCESS_TEAM", raising=False)
    import importlib

    import keystone.config as cfg

    importlib.reload(cfg)
    from keystone.auth.cf_access import CloudflareAccessMiddleware
    from starlette.testclient import TestClient

    app = CloudflareAccessMiddleware(http_app)
    with TestClient(app) as client:
        resp = client.get("/healthz")
        assert resp.status_code == 200
        # No auth header but middleware is no-op.
        resp = client.get("/mcp", headers={"host": "test_stone.example.com"})
        assert resp.status_code != 401


def test_rejects_missing_jwt_when_configured(http_app, monkeypatch):
    """With CF_ACCESS_TEAM set, requests without the JWT header get 401."""
    monkeypatch.setenv("CF_ACCESS_TEAM", "myteam")
    monkeypatch.setenv("CF_ACCESS_AUD_TEST_STONE", "aud-test")
    import importlib

    import keystone.config as cfg

    importlib.reload(cfg)
    from keystone.auth.cf_access import CloudflareAccessMiddleware
    from starlette.testclient import TestClient

    app = CloudflareAccessMiddleware(http_app)
    with TestClient(app) as client:
        # /healthz still allowed.
        assert client.get("/healthz").status_code == 200

        resp = client.get("/mcp", headers={"host": "test_stone.example.com"})
        assert resp.status_code == 401
        assert "Missing" in resp.json()["error"]


def test_rejects_unknown_subdomain_when_configured(http_app, monkeypatch):
    monkeypatch.setenv("CF_ACCESS_TEAM", "myteam")
    monkeypatch.setenv("CF_ACCESS_AUD_TEST_STONE", "aud-test")
    import importlib

    import keystone.config as cfg

    importlib.reload(cfg)
    from keystone.auth.cf_access import CloudflareAccessMiddleware
    from starlette.testclient import TestClient

    app = CloudflareAccessMiddleware(http_app)
    with TestClient(app) as client:
        resp = client.get(
            "/mcp",
            headers={"host": "nosuch.example.com", "cf-access-jwt-assertion": "xxx"},
        )
        assert resp.status_code == 404
