"""HTTP transport: subdomain dispatch and healthz."""

from __future__ import annotations

import os
from pathlib import Path

import pytest


TEST_STONE_PKG_DIR = Path(__file__).resolve().parent / "fixtures"
STONE_NAMES = ("test_stone",)


@pytest.fixture
def http_app(tmp_path: Path, monkeypatch):
    monkeypatch.setenv("KEYSTONE_STONES_DIR", str(tmp_path))
    monkeypatch.setenv("KEYSTONE_STONES_PKG_DIR", str(TEST_STONE_PKG_DIR))
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

    # Reload http transport so it sees fresh REGISTRY.
    import keystone.transports.http as http_mod

    importlib.reload(http_mod)
    return http_mod.build_app()


def test_extract_stone():
    from keystone.transports.http import _extract_stone

    scope = {"headers": [(b"host", b"test_stone.example.com")]}
    assert _extract_stone(scope) == "test_stone"

    scope = {"headers": [(b"host", b"localhost:8787")]}
    assert _extract_stone(scope) is None


def test_root_lists_stones_only_on_localhost(http_app):
    """Stone names must NOT leak to public callers (Host: !localhost)."""
    from starlette.testclient import TestClient

    with TestClient(http_app) as client:
        # Public-style request (Host: testserver) -> minimal body, no stones
        public = client.get("/")
        assert public.status_code == 200
        assert "stones" not in public.json()
        assert public.json() == {"service": "keystone"}

        # localhost request (e.g. cloudflared healthcheck) -> full info OK
        local = client.get("/", headers={"host": "localhost"})
        assert local.status_code == 200
        body = local.json()
        assert "test_stone" in body["stones"]


def test_healthz_minimal_for_public(http_app):
    """Public /healthz returns only liveness, no architecture details."""
    from starlette.testclient import TestClient

    with TestClient(http_app) as client:
        public = client.get("/healthz")
        assert public.status_code == 200
        assert public.json() == {"ok": True}

        local = client.get("/healthz", headers={"host": "localhost"})
        assert local.status_code == 200
        assert local.json()["ok"] is True
        assert "stones" in local.json()


def test_unknown_subdomain_returns_404(http_app):
    from starlette.testclient import TestClient

    with TestClient(http_app) as client:
        resp = client.get("/mcp", headers={"host": "nosuch.example.com"})
        assert resp.status_code == 404
        assert "unknown stone" in resp.text


def test_known_stone_subdomain_dispatches(http_app):
    """A request to test_stone.* should hit that sub-app (returns 405/406 for GET, but NOT 404)."""
    from starlette.testclient import TestClient

    with TestClient(http_app) as client:
        resp = client.get("/mcp", headers={"host": "test_stone.example.com"})
        assert resp.status_code != 404


def test_subdomain_alias_routes_to_stone(http_app):
    """A subdomain listed in `http_subdomain_aliases` should hit the same sub-app.

    The fixture stone declares ``"http_subdomain_aliases": ["legacy_test"]``;
    a request to ``legacy_test.*`` must reach the sub-app (NOT 404) so legacy
    public DNS names can keep routing to a stone whose canonical name has
    since been renamed.
    """

    from starlette.testclient import TestClient

    with TestClient(http_app) as client:
        resp = client.get("/mcp", headers={"host": "legacy_test.example.com"})
        assert resp.status_code != 404, (
            "Expected the alias subdomain to dispatch to the stone, got 404. "
            "Check that build_app() mounts each stone under its "
            "http_subdomain_aliases entries in addition to its canonical name."
        )
