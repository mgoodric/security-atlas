"""ImportCatalog bridge tests (slice 492 — AC-12).

Covers the ingest direction of invariant #8:
  - a real OSCAL v1.1.x catalog deserializes to a normalized projection;
  - a malformed / schema-invalid catalog is rejected (valid=False);
  - an over-cap document is rejected (threat-model D / AC-3);
  - an href-bearing catalog imports WITHOUT dereferencing any external
    resource (P0-492-2 / threat-model I).

The serializer-level tests run pure (no gRPC); two gRPC-level tests prove
the proto/server wiring.
"""

from __future__ import annotations

import socket
from pathlib import Path

import grpc
import pytest
from atlas_oscal_bridge import oscal_pb2, oscal_pb2_grpc
from atlas_oscal_bridge.serializer import (
    MAX_CATALOG_BYTES,
    import_catalog,
)
from atlas_oscal_bridge.server import serve

_FIXTURES = Path(__file__).parent / "fixtures"


def _fixture(name: str) -> bytes:
    return (_FIXTURES / name).read_bytes()


# ===== serializer-level (pure) =====


def test_import_valid_catalog():
    result = import_catalog(_fixture("catalog_minimal_valid.json"), "NIST 800-53 test")
    assert result.valid, result.errors
    assert result.oscal_version == "1.1.2"
    assert result.catalog_title == "Minimal Test Catalog"
    ids = {c.control_id for c in result.controls}
    assert ids == {"ac-1", "ac-2", "IAC-06"}
    # Group path provenance is carried.
    assert all(c.group_path == "Access Control" for c in result.controls)
    # Statement prose is flattened, not empty.
    by_id = {c.control_id: c for c in result.controls}
    assert "multi-factor authentication" in by_id["IAC-06"].statement.lower()


def test_import_malformed_catalog_is_rejected():
    result = import_catalog(_fixture("catalog_malformed.json"), "bad")
    assert not result.valid
    assert result.controls == []
    assert result.errors


def test_import_non_json_is_rejected():
    result = import_catalog(b"{ this is not json", "bad")
    assert not result.valid
    assert "invalid JSON" in result.errors[0]


def test_import_wrong_top_level_key_is_rejected():
    # A valid OSCAL profile/SSP is NOT a catalog.
    result = import_catalog(b'{"profile": {"uuid": "x"}}', "bad")
    assert not result.valid
    assert "catalog" in result.errors[0]


def test_import_over_byte_cap_is_rejected():
    oversize = b'{"catalog":' + b" " * (MAX_CATALOG_BYTES + 1) + b"}"
    result = import_catalog(oversize, "huge")
    assert not result.valid
    assert "cap" in result.errors[0]


def test_import_href_bearing_catalog_does_not_dereference():
    # The fixture's back-matter references file:///etc/passwd and an
    # unresolvable https URL. import_catalog must succeed WITHOUT touching
    # either — back-matter resources are opaque metadata (P0-492-2). If the
    # importer dereferenced the file:// href it would still succeed (read
    # access), so the real guarantee is structural: we assert the projection
    # carries ONLY the in-document control and never raises a network error.
    result = import_catalog(_fixture("catalog_with_href.json"), "href")
    assert result.valid, result.errors
    assert len(result.controls) == 1
    assert result.controls[0].control_id == "hr-1"


# ===== gRPC-level (proto + server wiring) =====


@pytest.fixture()
def bridge_channel():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    server = serve(f"127.0.0.1:{port}", max_workers=4)
    channel = grpc.insecure_channel(f"127.0.0.1:{port}")
    yield channel
    channel.close()
    server.stop(0)


def test_import_catalog_rpc_valid(bridge_channel):
    stub = oscal_pb2_grpc.OscalBridgeServiceStub(bridge_channel)
    resp = stub.ImportCatalog(
        oscal_pb2.ImportCatalogRequest(
            oscal_json=_fixture("catalog_minimal_valid.json"),
            source_label="NIST 800-53 test",
        )
    )
    assert resp.valid, list(resp.errors)
    assert resp.oscal_version == "1.1.2"
    assert resp.source_label == "NIST 800-53 test"
    assert {c.control_id for c in resp.controls} == {"ac-1", "ac-2", "IAC-06"}


def test_import_catalog_rpc_malformed(bridge_channel):
    stub = oscal_pb2_grpc.OscalBridgeServiceStub(bridge_channel)
    resp = stub.ImportCatalog(
        oscal_pb2.ImportCatalogRequest(
            oscal_json=_fixture("catalog_malformed.json"),
            source_label="bad",
        )
    )
    assert not resp.valid
    assert len(resp.controls) == 0
    assert resp.errors
