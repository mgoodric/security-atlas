"""gRPC server integration test (AC-13).

Spins the bridge server up in-process on an ephemeral loopback port,
dials it with a real gRPC channel, and exercises every RPC. This proves
the proto contract and the server wiring, not just the serializer.
"""

from __future__ import annotations

import json

import grpc
import pytest
from atlas_oscal_bridge import oscal_pb2, oscal_pb2_grpc
from atlas_oscal_bridge.server import serve


@pytest.fixture()
def bridge_channel():
    # Port 0 -> the OS picks a free loopback port.
    server = serve("127.0.0.1:0", max_workers=4)
    # grpc's add_insecure_port returns the bound port; serve() raises on
    # 0, so re-bind explicitly here to learn the port for the client.
    server.stop(0)
    # Re-create on a known ephemeral port range pick: bind to 0 and read
    # it back via a fresh server we control.
    import socket

    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    server = serve(f"127.0.0.1:{port}", max_workers=4)
    channel = grpc.insecure_channel(f"127.0.0.1:{port}")
    yield channel
    channel.close()
    server.stop(0)


def test_serialize_ssp_rpc(bridge_channel):
    stub = oscal_pb2_grpc.OscalBridgeServiceStub(bridge_channel)
    req = oscal_pb2.SerializeSSPRequest(
        input=oscal_pb2.SspInput(
            metadata=oscal_pb2.Metadata(title="t", version="1.0"),
            tenant_id="t-1",
            organization_name="Org",
            system_name="Sys",
            system_description="desc",
            control_implementations=[
                oscal_pb2.ControlImplementation(
                    control_id="c-1",
                    scf_id="IAC-01",
                    title="Identity",
                    statement="Implemented.",
                    evaluation_result="pass",
                )
            ],
        )
    )
    resp = stub.SerializeSSP(req)
    doc = json.loads(resp.oscal_json)
    assert "system-security-plan" in doc


def test_serialize_assessment_rpc(bridge_channel):
    stub = oscal_pb2_grpc.OscalBridgeServiceStub(bridge_channel)
    req = oscal_pb2.SerializeAssessmentRequest(
        input=oscal_pb2.AssessmentInput(
            metadata=oscal_pb2.Metadata(title="t", version="1.0"),
            audit_period_id="p-1",
            audit_period_name="Q2",
        )
    )
    resp = stub.SerializeAssessment(req)
    assert b"assessment-plan" in resp.assessment_plan_json
    assert b"assessment-results" in resp.assessment_results_json


def test_serialize_poam_rpc(bridge_channel):
    stub = oscal_pb2_grpc.OscalBridgeServiceStub(bridge_channel)
    req = oscal_pb2.SerializePOAMRequest(
        input=oscal_pb2.PoamInput(
            metadata=oscal_pb2.Metadata(title="t", version="1.0"),
            audit_period_id="p-1",
        )
    )
    resp = stub.SerializePOAM(req)
    assert b"plan-of-action-and-milestones" in resp.oscal_json


def test_round_trip_validate_rpc(bridge_channel):
    stub = oscal_pb2_grpc.OscalBridgeServiceStub(bridge_channel)
    # First produce a real SSP, then validate it back.
    ssp_resp = stub.SerializeSSP(
        oscal_pb2.SerializeSSPRequest(
            input=oscal_pb2.SspInput(
                metadata=oscal_pb2.Metadata(title="t", version="1.0"),
                tenant_id="t-1",
                system_name="Sys",
                system_description="desc",
                control_implementations=[
                    oscal_pb2.ControlImplementation(
                        control_id="c-1",
                        scf_id="IAC-01",
                        title="Identity",
                        statement="Implemented.",
                        evaluation_result="pass",
                    )
                ],
            )
        )
    )
    rt = stub.RoundTripValidate(
        oscal_pb2.RoundTripValidateRequest(
            model_type="system-security-plan",
            oscal_json=ssp_resp.oscal_json,
        )
    )
    assert rt.valid, list(rt.errors)

    bad = stub.RoundTripValidate(
        oscal_pb2.RoundTripValidateRequest(
            model_type="system-security-plan",
            oscal_json=b"{garbage",
        )
    )
    assert not bad.valid
    assert bad.errors


def test_serialize_ssp_rpc_rejects_empty_controls(bridge_channel):
    stub = oscal_pb2_grpc.OscalBridgeServiceStub(bridge_channel)
    with pytest.raises(grpc.RpcError) as exc_info:
        stub.SerializeSSP(
            oscal_pb2.SerializeSSPRequest(
                input=oscal_pb2.SspInput(
                    metadata=oscal_pb2.Metadata(title="t", version="1.0"),
                    tenant_id="t-1",
                    system_name="Sys",
                )
            )
        )
    assert exc_info.value.code() == grpc.StatusCode.INVALID_ARGUMENT
