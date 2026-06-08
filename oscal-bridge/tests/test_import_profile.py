"""ImportProfile bridge tests (slice 511 — AC-9..12 bridge half).

Covers the resolve direction of invariant #8:
  - a real FedRAMP-style profile resolves import / merge / modify against a
    SUPPLIED catalog into a normalized control projection;
  - an external (https) import.href is rejected WITHOUT a network fetch
    (P0-511-1 / threat-model I — the load-bearing assertion);
  - an unmatched / unknown import.href is a structured error, not a fetch;
  - an over-cap document is rejected (threat-model D / AC-3);
  - a malformed profile is rejected (valid=False).

The serializer-level tests run pure (no gRPC, no network); two gRPC-level
tests prove the proto/server wiring.
"""

from __future__ import annotations

import socket
from pathlib import Path

import grpc
import pytest
from atlas_oscal_bridge import oscal_pb2, oscal_pb2_grpc
from atlas_oscal_bridge.serializer import (
    MAX_PROFILE_BYTES,
    import_profile,
)
from atlas_oscal_bridge.server import serve

_FIXTURES = Path(__file__).parent / "fixtures"


def _fixture(name: str) -> bytes:
    return (_FIXTURES / name).read_bytes()


class _SuppliedCatalog:
    """Structural stand-in for the SuppliedCatalog protobuf (pure tests)."""

    def __init__(self, oscal_json: bytes):
        self.oscal_json = oscal_json


def _base_catalogs():
    return [_SuppliedCatalog(_fixture("base_catalog.json"))]


# ===== serializer-level (pure) =====


def test_import_profile_resolves_against_supplied_catalog():
    result = import_profile(
        _fixture("profile_baseline.json"),
        _base_catalogs(),
        "FedRAMP Moderate test",
    )
    assert result.valid, result.errors
    assert result.profile_title == "FedRAMP-style Test Moderate Baseline"
    ids = {c.control_id for c in result.controls}
    # include-controls selected exactly ac-1, ac-2, IAC-06 (NOT ac-3).
    assert ids == {"ac-1", "ac-2", "IAC-06"}
    assert "ac-3" not in ids
    # set-parameters resolved the moustache into the prose.
    by_id = {c.control_id: c for c in result.controls}
    assert "quarterly" in by_id["ac-1"].statement.lower()
    # modify.alters added baseline-specific guidance to ac-2.
    assert "baseline-specific account-management guidance" in by_id["ac-2"].statement.lower()


def test_import_profile_external_href_is_rejected_without_fetch(monkeypatch):
    # Make ANY socket connect raise: if the resolver tried to fetch the
    # external href, this would surface as a connect error, not our clean
    # structured rejection. A pass proves no network call was attempted.
    def _boom(*_args, **_kwargs):
        raise AssertionError("network access attempted during profile import (P0-511-1 violation)")

    monkeypatch.setattr(socket.socket, "connect", _boom)
    result = import_profile(
        _fixture("profile_external_href.json"),
        _base_catalogs(),
        "malicious",
    )
    assert not result.valid
    assert result.controls == []
    assert any("external reference" in e for e in result.errors)


def test_import_profile_unknown_href_is_structured_error():
    # A trestle-looking but unmatched local href maps to no supplied catalog.
    profile = (
        b'{"profile":{"uuid":"55555555-5555-4555-8555-555555555555",'
        b'"metadata":{"title":"X","last-modified":"2026-06-07T00:00:00+00:00",'
        b'"version":"1.0","oscal-version":"1.1.2"},'
        b'"imports":[{"href":"some-unknown-catalog.json"}]}}'
    )
    # Two catalogs supplied so the single-import positional shortcut does NOT
    # fire; the href must be matched explicitly and will not be.
    cats = [
        _SuppliedCatalog(_fixture("base_catalog.json")),
        _SuppliedCatalog(_fixture("catalog_minimal_valid.json")),
    ]
    result = import_profile(profile, cats, "")
    assert not result.valid
    assert any("does not map to any supplied catalog" in e for e in result.errors)


def test_import_profile_no_catalogs_is_rejected():
    result = import_profile(_fixture("profile_baseline.json"), [], "")
    assert not result.valid
    assert any("at least one catalog" in e for e in result.errors)


def test_import_profile_oversize_is_rejected():
    big = b"x" * (MAX_PROFILE_BYTES + 1)
    result = import_profile(big, _base_catalogs(), "")
    assert not result.valid
    assert any("over the" in e for e in result.errors)


def test_import_profile_malformed_is_rejected():
    result = import_profile(_fixture("profile_malformed.json"), _base_catalogs(), "")
    assert not result.valid
    assert result.controls == []
    assert result.errors


def test_import_profile_non_json_is_rejected():
    result = import_profile(b"{not json", _base_catalogs(), "")
    assert not result.valid
    assert any("invalid JSON" in e for e in result.errors)


def test_import_profile_missing_top_key_is_rejected():
    result = import_profile(b'{"catalog":{}}', _base_catalogs(), "")
    assert not result.valid
    assert any("top-level key 'profile'" in e for e in result.errors)


def test_import_profile_no_imports_is_rejected():
    profile = (
        b'{"profile":{"uuid":"66666666-6666-4666-8666-666666666666",'
        b'"metadata":{"title":"X","last-modified":"2026-06-07T00:00:00+00:00",'
        b'"version":"1.0","oscal-version":"1.1.2"},"imports":[]}}'
    )
    result = import_profile(profile, _base_catalogs(), "")
    assert not result.valid
    assert any("no 'imports'" in e for e in result.errors)


# ===== gRPC-level (proto/server wiring) =====


@pytest.fixture()
def _bridge_channel():
    # Bind an ephemeral loopback port.
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    addr = f"127.0.0.1:{port}"
    server = serve(addr)
    channel = grpc.insecure_channel(addr)
    try:
        yield channel
    finally:
        channel.close()
        server.stop(grace=None)


def test_grpc_import_profile_valid(_bridge_channel):
    stub = oscal_pb2_grpc.OscalBridgeServiceStub(_bridge_channel)
    resp = stub.ImportProfile(
        oscal_pb2.ImportProfileRequest(
            profile_json=_fixture("profile_baseline.json"),
            catalogs=[oscal_pb2.SuppliedCatalog(oscal_json=_fixture("base_catalog.json"))],
            source_label="FedRAMP Moderate test",
        )
    )
    assert resp.valid, list(resp.errors)
    ids = {c.control_id for c in resp.controls}
    assert ids == {"ac-1", "ac-2", "IAC-06"}
    assert resp.source_label == "FedRAMP Moderate test"
    assert resp.profile_title == "FedRAMP-style Test Moderate Baseline"


def test_grpc_import_profile_external_href_rejected(_bridge_channel):
    stub = oscal_pb2_grpc.OscalBridgeServiceStub(_bridge_channel)
    resp = stub.ImportProfile(
        oscal_pb2.ImportProfileRequest(
            profile_json=_fixture("profile_external_href.json"),
            catalogs=[oscal_pb2.SuppliedCatalog(oscal_json=_fixture("base_catalog.json"))],
            source_label="malicious",
        )
    )
    assert not resp.valid
    assert list(resp.controls) == []
    assert any("external reference" in e for e in resp.errors)
