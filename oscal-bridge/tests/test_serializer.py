"""Unit tests for the OSCAL serializer (AC-9..AC-12, AC-14).

These tests build protobuf input messages directly, run them through the
serializer, and assert the emitted JSON is canonical OSCAL v1.1.x AND
survives a trestle round-trip. No gRPC server and no Go side are needed.
"""

from __future__ import annotations

import json

import pytest
from atlas_oscal_bridge import OSCAL_VERSION, oscal_pb2
from atlas_oscal_bridge.serializer import (
    SerializeError,
    round_trip_validate,
    serialize_assessment,
    serialize_poam,
    serialize_ssp,
)


def _metadata() -> oscal_pb2.Metadata:
    return oscal_pb2.Metadata(
        title="Test Export",
        version="1.0",
        oscal_version="1.1.2",
        last_modified="2026-05-14T12:00:00+00:00",
        frozen_at="2026-05-01T00:00:00+00:00",
    )


def _ssp_input() -> oscal_pb2.SspInput:
    return oscal_pb2.SspInput(
        metadata=_metadata(),
        tenant_id="11111111-1111-1111-1111-111111111111",
        organization_name="Acme Security Inc.",
        system_name="Acme Compliance Platform",
        system_description="The SaaS platform under SOC 2 assessment.",
        scope_cells=[
            oscal_pb2.ScopeCell(
                id="cell-1",
                label="prod / us-east / aws",
                dimensions_json='{"env":"prod","geo":"us-east","cloud":"aws"}',
            )
        ],
        control_implementations=[
            oscal_pb2.ControlImplementation(
                control_id="ctrl-1",
                scf_id="IAC-06",
                title="Multi-factor authentication",
                statement="MFA is enforced for all administrative access via Okta.",
                evaluation_result="pass",
                evaluated_at="2026-04-30T00:00:00+00:00",
                linked_policy_ids=["pol-1"],
            )
        ],
        policies=[
            oscal_pb2.Policy(
                id="pol-1",
                title="Access Control Policy",
                version="2.0",
                status="published",
            )
        ],
    )


def test_serialize_ssp_emits_valid_oscal():
    data = serialize_ssp(_ssp_input())
    doc = json.loads(data)
    assert "system-security-plan" in doc
    ssp = doc["system-security-plan"]
    assert ssp["metadata"]["oscal-version"] == OSCAL_VERSION
    # The human-authored statement must be carried verbatim — never
    # regenerated. (CLAUDE.md product-runtime AI-assist boundary.)
    blob = json.dumps(ssp)
    assert "MFA is enforced for all administrative access via Okta." in blob
    # round-trip
    valid, errors = round_trip_validate("system-security-plan", data)
    assert valid, errors


def test_serialize_ssp_rejects_empty_control_implementations():
    bad = _ssp_input()
    del bad.control_implementations[:]
    with pytest.raises(SerializeError):
        serialize_ssp(bad)


def test_serialize_assessment_emits_valid_ap_and_ar():
    inp = oscal_pb2.AssessmentInput(
        metadata=_metadata(),
        tenant_id="11111111-1111-1111-1111-111111111111",
        audit_period_id="period-1",
        audit_period_name="SOC 2 2026 Q2",
        populations=[
            oscal_pb2.SamplePopulation(
                population_id="pop-1",
                control_id="ctrl-1",
                population_size=40,
                sampled_evidence_ids=["ev-1", "ev-2", "ev-3"],
                frozen_at="2026-05-01T00:00:00+00:00",
            )
        ],
        walkthroughs=[
            oscal_pb2.Walkthrough(
                id="wt-1",
                control_id="ctrl-1",
                narrative="Auditor observed the MFA enrollment flow end to end.",
                status="finalized",
                canonical_hash="abc123",
                tamper_detected=False,
            )
        ],
        audit_notes=[
            oscal_pb2.AuditNote(
                id="note-1",
                scope_kind="control",
                scope_ref="ctrl-1",
                author="auditor@example.com",
                body="Please confirm the MFA policy applies to break-glass accounts.",
                created_at="2026-05-02T09:00:00+00:00",
            )
        ],
    )
    ap_json, ar_json = serialize_assessment(inp)
    ap_doc = json.loads(ap_json)
    ar_doc = json.loads(ar_json)
    assert "assessment-plan" in ap_doc
    assert "assessment-results" in ar_doc
    # walkthrough + audit note both became observations
    obs = ar_doc["assessment-results"]["results"][0].get("observations", [])
    assert len(obs) == 2
    ap_valid, ap_errors = round_trip_validate("assessment-plan", ap_json)
    ar_valid, ar_errors = round_trip_validate("assessment-results", ar_json)
    assert ap_valid, ap_errors
    assert ar_valid, ar_errors


def test_serialize_poam_emits_valid_oscal():
    inp = oscal_pb2.PoamInput(
        metadata=_metadata(),
        tenant_id="11111111-1111-1111-1111-111111111111",
        audit_period_id="period-1",
        items=[
            oscal_pb2.PoamItem(
                id="finding-1",
                control_id="ctrl-9",
                title="Backup restore test overdue",
                description="The quarterly restore test has not run this period.",
                severity="high",
                owner="control_owner",
                due_date="2026-06-15T00:00:00+00:00",
                milestone="Run and document a full restore test",
            )
        ],
    )
    data = serialize_poam(inp)
    doc = json.loads(data)
    assert "plan-of-action-and-milestones" in doc
    items = doc["plan-of-action-and-milestones"]["poam-items"]
    assert len(items) == 1
    valid, errors = round_trip_validate("plan-of-action-and-milestones", data)
    assert valid, errors


def test_serialize_poam_handles_zero_findings():
    # An audit period with no open findings still produces a valid POA&M.
    inp = oscal_pb2.PoamInput(
        metadata=_metadata(),
        tenant_id="11111111-1111-1111-1111-111111111111",
        audit_period_id="period-1",
    )
    data = serialize_poam(inp)
    valid, errors = round_trip_validate("plan-of-action-and-milestones", data)
    assert valid, errors
    items = json.loads(data)["plan-of-action-and-milestones"]["poam-items"]
    assert len(items) == 1
    assert items[0]["title"] == "No open findings"


def test_round_trip_rejects_garbage():
    valid, errors = round_trip_validate("system-security-plan", b"{not json")
    assert not valid
    assert errors
    valid, errors = round_trip_validate("unknown-model", b"{}")
    assert not valid


def test_round_trip_rejects_wrong_top_key():
    # Valid JSON, valid model_type, but the document body is missing.
    valid, errors = round_trip_validate("assessment-plan", b'{"wrong-key": {}}')
    assert not valid
    assert "missing top-level key" in errors[0]
