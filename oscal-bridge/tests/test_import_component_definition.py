"""ImportComponentDefinition bridge tests (slice 512 — AC-2/AC-3/AC-10..13).

Covers the vendor-claim ingest direction of invariant #8:
  - a real component-definition deserializes + validates into a normalized
    component + vendor-claim projection;
  - links / sources pointing at an external host are NEVER dereferenced
    (P0-512-2 / threat-model I — the load-bearing assertion: the bridge reads
    only in-document prose and never fetches an href);
  - an over-cap document is rejected (threat-model D / AC-3);
  - a malformed component-definition is rejected (valid=False);
  - non-JSON / missing-top-key / zero-component / zero-claim are rejected.

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
    MAX_COMPONENT_DEF_BYTES,
    import_component_definition,
)
from atlas_oscal_bridge.server import serve

_FIXTURES = Path(__file__).parent / "fixtures"


def _fixture(name: str) -> bytes:
    return (_FIXTURES / name).read_bytes()


# ===== serializer-level (pure) =====


def test_import_component_definition_parses_components_and_claims():
    result = import_component_definition(
        _fixture("component_definition.json"),
        "Acme Cloud",
    )
    assert result.valid, result.errors
    assert result.component_definition_title == "Acme Cloud Platform Component Definition"
    titles = {c.title for c in result.components}
    assert titles == {"Acme Identity Service", "Acme Audit Logger"}
    # Three vendor claims across the two components.
    all_control_ids = {cl.control_id for comp in result.components for cl in comp.claims}
    assert all_control_ids == {"IAC-06", "ac-2", "au-2"}
    # The vendor's implementation narrative is flattened (description +
    # statement prose).
    by_id = {cl.control_id: cl for comp in result.components for cl in comp.claims}
    assert "phishing-resistant mfa" in by_id["IAC-06"].statement.lower()
    assert "fido2/webauthn" in by_id["IAC-06"].statement.lower()
    # The requirement uuid is echoed for provenance.
    assert by_id["IAC-06"].requirement_uuid == "dddddddd-dddd-4ddd-8ddd-dddddddddddd"


def test_import_component_definition_external_href_is_never_fetched(monkeypatch):
    # Make ANY socket connect raise: if the parser tried to fetch any href
    # (link / source), this would surface as a connect error. A pass proves
    # no network call was attempted — links/sources are opaque metadata
    # (P0-512-2 / threat-model I).
    def _boom(*_args, **_kwargs):
        raise AssertionError(
            "network access attempted during component-definition import (P0-512-2 violation)"
        )

    monkeypatch.setattr(socket.socket, "connect", _boom)
    result = import_component_definition(
        _fixture("component_definition_external_href.json"),
        "Externally-Linked Vendor",
    )
    # The document is structurally valid; the external links are simply never
    # followed. The claim is surfaced with its in-document prose only.
    assert result.valid, result.errors
    claims = [cl for comp in result.components for cl in comp.claims]
    assert len(claims) == 1
    assert claims[0].control_id == "IAC-06"
    # No external content leaked into the statement (only in-document prose).
    assert "attacker.example.com" not in claims[0].statement
    assert "implement mfa" in claims[0].statement.lower()


def test_import_component_definition_oversize_is_rejected():
    big = b"x" * (MAX_COMPONENT_DEF_BYTES + 1)
    result = import_component_definition(big, "")
    assert not result.valid
    assert any("over the" in e for e in result.errors)


def test_import_component_definition_malformed_is_rejected():
    result = import_component_definition(_fixture("component_definition_malformed.json"), "")
    assert not result.valid
    assert result.components == []
    assert result.errors


def test_import_component_definition_non_json_is_rejected():
    result = import_component_definition(b"{not json", "")
    assert not result.valid
    assert any("invalid JSON" in e for e in result.errors)


def test_import_component_definition_missing_top_key_is_rejected():
    result = import_component_definition(b'{"catalog":{}}', "")
    assert not result.valid
    assert any("top-level key 'component-definition'" in e for e in result.errors)


def test_import_component_definition_zero_components_is_rejected():
    doc = (
        b'{"component-definition":{"uuid":"99999999-9999-4999-8999-999999999999",'
        b'"metadata":{"title":"Empty","last-modified":"2026-06-07T00:00:00+00:00",'
        b'"version":"1.0","oscal-version":"1.1.2"}}}'
    )
    result = import_component_definition(doc, "")
    assert not result.valid
    assert any("zero components" in e for e in result.errors)


def test_import_component_definition_zero_claims_is_rejected():
    # A component with no control-implementations yields zero vendor claims.
    doc = (
        b'{"component-definition":{"uuid":"a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1",'
        b'"metadata":{"title":"No Claims","last-modified":"2026-06-07T00:00:00+00:00",'
        b'"version":"1.0","oscal-version":"1.1.2"},'
        b'"components":[{"uuid":"b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2",'
        b'"type":"service","title":"Bare","description":"no claims"}]}}'
    )
    result = import_component_definition(doc, "")
    assert not result.valid
    assert any("zero implemented-requirements" in e for e in result.errors)


# ===== gRPC-level (proto/server wiring) =====


@pytest.fixture()
def _bridge_channel():
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


def test_grpc_import_component_definition_valid(_bridge_channel):
    stub = oscal_pb2_grpc.OscalBridgeServiceStub(_bridge_channel)
    resp = stub.ImportComponentDefinition(
        oscal_pb2.ImportComponentDefinitionRequest(
            oscal_json=_fixture("component_definition.json"),
            source_label="Acme Cloud",
        )
    )
    assert resp.valid, list(resp.errors)
    assert resp.source_label == "Acme Cloud"
    assert resp.component_definition_title == "Acme Cloud Platform Component Definition"
    titles = {c.title for c in resp.components}
    assert titles == {"Acme Identity Service", "Acme Audit Logger"}
    control_ids = {cl.control_id for comp in resp.components for cl in comp.claims}
    assert control_ids == {"IAC-06", "ac-2", "au-2"}


def test_grpc_import_component_definition_malformed_rejected(_bridge_channel):
    stub = oscal_pb2_grpc.OscalBridgeServiceStub(_bridge_channel)
    resp = stub.ImportComponentDefinition(
        oscal_pb2.ImportComponentDefinitionRequest(
            oscal_json=_fixture("component_definition_malformed.json"),
            source_label="bad",
        )
    )
    assert not resp.valid
    assert list(resp.components) == []
    assert resp.errors
