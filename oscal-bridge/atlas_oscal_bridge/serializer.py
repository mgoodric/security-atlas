"""Maps platform protobuf aggregates to canonical OSCAL JSON v1.1.x.

Each ``serialize_*`` function takes a decoded protobuf input message and
returns ``bytes`` — the canonical OSCAL JSON document compliance-trestle
emits. ``round_trip_validate`` parses an OSCAL JSON document back through
trestle, proving structural validity (AC-6 / AC-7).

trestle's pydantic-v1 models are strict: every OSCAL-required field must
be present. The Go aggregator populates the real platform data; this
module fills the handful of OSCAL-structural-but-platform-irrelevant
fields (e.g. ``import-profile.href``) with stable placeholder anchors so
the documents validate without inventing audit content.

No LLM is imported or called here. SSP ``statement`` text is whatever the
Go side passed — human-authored control-bundle descriptions.
"""

from __future__ import annotations

import json
import uuid
from datetime import UTC, datetime

from trestle.oscal.assessment_plan import AssessmentPlan
from trestle.oscal.assessment_results import AssessmentResults, ImportAp, Result
from trestle.oscal.catalog import Catalog
from trestle.oscal.common import (
    ControlSelection,
    ImportSsp,
    Metadata,
    Observation,
    Property,
    ReviewedControls,
    SystemComponent,
    SystemId,
)
from trestle.oscal.common import (
    Status as CommonStatus,
)
from trestle.oscal.poam import PlanOfActionAndMilestones, PoamItem
from trestle.oscal.ssp import (
    AuthorizationBoundary,
    ControlImplementation,
    ImplementedRequirement,
    ImportProfile,
    InformationType,
    Statement,
    SystemCharacteristics,
    SystemImplementation,
    SystemInformation,
    SystemSecurityPlan,
)

from . import OSCAL_VERSION

# trestle's pydantic enums are reachable via each field's own ``.type_``.
# Resolving them this way avoids guessing which of the near-identically
# named ``Status`` / ``OperationalState`` enums a given field validates
# against (there are several; see the slice-030 decisions log).
_SC_STATUS_CLS = SystemCharacteristics.__fields__["status"].type_
_SC_STATE_ENUM = _SC_STATUS_CLS.__fields__["state"].type_
_COMP_STATE_ENUM = SystemComponent.__fields__["status"].type_.__fields__["state"].type_


class SerializeError(ValueError):
    """Raised when an input message cannot be mapped to a valid OSCAL model."""


def _now_iso() -> datetime:
    return datetime.now(UTC)


def _metadata(pb_meta, default_title: str) -> Metadata:
    """Build an OSCAL Metadata block from the proto Metadata message.

    ``oscal_version`` is always forced to the platform-pinned value;
    ``last_modified`` defaults to now when the caller left it blank.
    """
    title = (pb_meta.title or default_title) if pb_meta is not None else default_title
    version = (pb_meta.version or "1.0") if pb_meta is not None else "1.0"
    last_modified = _now_iso()
    if pb_meta is not None and pb_meta.last_modified:
        try:
            last_modified = datetime.fromisoformat(pb_meta.last_modified)
        except ValueError:
            # Tolerate a malformed timestamp rather than fail the export;
            # the Go side always sends RFC-3339, this is belt-and-braces.
            last_modified = _now_iso()
    return Metadata(
        title=title,
        last_modified=last_modified,
        version=version,
        oscal_version=OSCAL_VERSION,
    )


def _prop(name: str, value: str) -> Property:
    return Property(name=name, value=value, ns="https://security-atlas.dev/ns/oscal")


def _oscal_token(raw: str) -> str:
    """Coerce an arbitrary id into an OSCAL NCName-style token.

    OSCAL ``control-id`` / ``statement-id`` must start with a letter or
    underscore and contain no whitespace. SCF codes ("IAC-06") already
    qualify; raw control UUIDs may start with a digit. We lowercase,
    replace any disallowed run with a hyphen, and prefix ``ctrl-`` when
    the result would not start with a letter/underscore — so the mapping
    is stable and collision-free for the inputs the platform produces.
    """
    cleaned = "".join(ch if (ch.isalnum() or ch in "-_.") else "-" for ch in raw.lower())
    if not cleaned or not (cleaned[0].isalpha() or cleaned[0] == "_"):
        cleaned = f"ctrl-{cleaned}"
    return cleaned


# --------------------------------------------------------------------------
# SSP
# --------------------------------------------------------------------------


def serialize_ssp(pb_input) -> bytes:
    """Map an ``SspInput`` protobuf message to OSCAL system-security-plan JSON."""
    meta = _metadata(pb_input.metadata, "System Security Plan")

    info_types = [
        InformationType(
            title="Compliance program data",
            description=pb_input.system_description
            or "Information processed by the system under assessment.",
        )
    ]

    sys_chars = SystemCharacteristics(
        system_ids=[SystemId(id=pb_input.tenant_id or "system")],
        system_name=pb_input.system_name or "security-atlas system",
        description=pb_input.system_description or "System under compliance assessment.",
        system_information=SystemInformation(information_types=info_types),
        status=_SC_STATUS_CLS(state=_SC_STATE_ENUM.operational),
        authorization_boundary=AuthorizationBoundary(
            description=_scope_boundary_description(pb_input.scope_cells)
        ),
    )

    # System implementation: one this-system component plus one component
    # per linked policy so the SSP carries the governance surface.
    components = [
        SystemComponent(
            uuid=str(uuid.uuid4()),
            type="this-system",
            title=pb_input.system_name or "security-atlas system",
            description=pb_input.organization_name
            or "The system operated by the assessed organization.",
            status=CommonStatus(state=_COMP_STATE_ENUM.operational),
        )
    ]
    for pol in pb_input.policies:
        components.append(
            SystemComponent(
                uuid=str(uuid.uuid4()),
                type="policy",
                title=pol.title,
                description=f"Governance policy '{pol.title}' (version {pol.version}, "
                f"status {pol.status}).",
                status=CommonStatus(state=_COMP_STATE_ENUM.operational),
                props=[
                    _prop("policy-id", pol.id),
                    _prop("policy-version", pol.version),
                    _prop("policy-status", pol.status),
                ],
            )
        )
    sys_impl = SystemImplementation(components=components)

    implemented = []
    for ci in pb_input.control_implementations:
        props = [_prop("evaluation-result", ci.evaluation_result or "no_evidence")]
        if ci.scf_id:
            props.append(_prop("scf-id", ci.scf_id))
        if ci.evaluated_at:
            props.append(_prop("evaluated-at", ci.evaluated_at))
        for pid in ci.linked_policy_ids:
            props.append(_prop("linked-policy-id", pid))
        # OSCAL control-id and statement-id are NCName-style tokens: they
        # must start with a letter/underscore and contain no spaces. SCF
        # codes (e.g. "IAC-06") satisfy this; raw control UUIDs may start
        # with a digit, so we always prefix with "ctrl-" to guarantee a
        # valid token.
        control_token = _oscal_token(ci.scf_id or ci.control_id)
        # The statement is the human-authored control-bundle description.
        # It is NEVER AI-generated (CLAUDE.md product-runtime boundary).
        statement = Statement(
            statement_id=f"{control_token}_stmt",
            uuid=str(uuid.uuid4()),
            remarks=ci.statement or "Implementation statement pending — see control bundle.",
        )
        implemented.append(
            ImplementedRequirement(
                uuid=str(uuid.uuid4()),
                control_id=control_token,
                props=props,
                statements=[statement],
            )
        )
    if not implemented:
        raise SerializeError("SSP input has no control implementations")

    control_impl = ControlImplementation(
        description="Control implementations derived from the frozen audit "
        "period's control bundles and evaluations.",
        implemented_requirements=implemented,
    )

    ssp = SystemSecurityPlan(
        uuid=str(uuid.uuid4()),
        metadata=meta,
        import_profile=ImportProfile(href="#security-atlas-profile"),
        system_characteristics=sys_chars,
        system_implementation=sys_impl,
        control_implementation=control_impl,
    )
    return ssp.oscal_serialize_json().encode("utf-8")


def _scope_boundary_description(scope_cells) -> str:
    if not scope_cells:
        return "Authorization boundary: the assessed system in its entirety."
    labels = ", ".join(sc.label for sc in scope_cells if sc.label)
    return (
        f"Authorization boundary spans the following scope cells: {labels}."
        if labels
        else "Authorization boundary: multidimensional scope (see scope cells)."
    )


# --------------------------------------------------------------------------
# Assessment Plan + Assessment Results
# --------------------------------------------------------------------------


def serialize_assessment(pb_input):
    """Map an ``AssessmentInput`` to (assessment-plan JSON, assessment-results JSON).

    Returns a 2-tuple of ``bytes``.
    """
    ap_meta = _metadata(pb_input.metadata, "Assessment Plan")
    ar_meta = _metadata(pb_input.metadata, "Assessment Results")

    # ReviewedControls: include-all is the honest v1 default — the
    # populations enumerate the controls actually sampled, carried as
    # props on the plan, but the formal selection is the whole baseline.
    reviewed = ReviewedControls(control_selections=[ControlSelection(include_all={})])

    ap = AssessmentPlan(
        uuid=str(uuid.uuid4()),
        metadata=ap_meta,
        import_ssp=ImportSsp(href="#security-atlas-ssp"),
        reviewed_controls=reviewed,
    )

    # Observations: one per walkthrough, one per audit note. trestle
    # requires uuid + description + methods + collected on each.
    observations = []
    for wt in pb_input.walkthroughs:
        props = [
            _prop("observation-source", "walkthrough"),
            _prop("walkthrough-id", wt.id),
            _prop("control-id", wt.control_id),
            _prop("walkthrough-status", wt.status),
        ]
        if wt.canonical_hash:
            props.append(_prop("canonical-hash", wt.canonical_hash))
        if wt.tamper_detected:
            props.append(_prop("tamper-detected", "true"))
        observations.append(
            Observation(
                uuid=str(uuid.uuid4()),
                description=wt.narrative or "Walkthrough recorded by the auditor.",
                methods=["INTERVIEW"],
                collected=_now_iso(),
                props=props,
            )
        )
    for note in pb_input.audit_notes:
        observations.append(
            Observation(
                uuid=str(uuid.uuid4()),
                description=note.body or "Audit note.",
                methods=["EXAMINE"],
                collected=_note_collected(note.created_at),
                props=[
                    _prop("observation-source", "audit-note"),
                    _prop("audit-note-id", note.id),
                    _prop("scope-kind", note.scope_kind),
                    _prop("scope-ref", note.scope_ref),
                    _prop("author", note.author),
                ],
            )
        )

    result = Result(
        uuid=str(uuid.uuid4()),
        title=f"Assessment results for {pb_input.audit_period_name or 'audit period'}",
        description="Sampled evidence, walkthroughs, and auditor notes for the "
        "frozen audit period.",
        start=_now_iso(),
        reviewed_controls=reviewed,
        observations=observations or None,
        props=_population_props(pb_input.populations),
    )

    ar = AssessmentResults(
        uuid=str(uuid.uuid4()),
        metadata=ar_meta,
        import_ap=ImportAp(href="#security-atlas-assessment-plan"),
        results=[result],
    )

    return (
        ap.oscal_serialize_json().encode("utf-8"),
        ar.oscal_serialize_json().encode("utf-8"),
    )


def _note_collected(created_at: str) -> datetime:
    if created_at:
        try:
            return datetime.fromisoformat(created_at)
        except ValueError:
            pass
    return _now_iso()


def _population_props(populations):
    props = []
    for pop in populations:
        props.append(_prop("population-id", pop.population_id))
        props.append(_prop(f"population-{pop.population_id}-control", pop.control_id))
        props.append(
            _prop(
                f"population-{pop.population_id}-size",
                str(pop.population_size),
            )
        )
        props.append(
            _prop(
                f"population-{pop.population_id}-sampled",
                str(len(pop.sampled_evidence_ids)),
            )
        )
    return props or None


# --------------------------------------------------------------------------
# POA&M
# --------------------------------------------------------------------------


def serialize_poam(pb_input) -> bytes:
    """Map a ``PoamInput`` to OSCAL plan-of-action-and-milestones JSON."""
    meta = _metadata(pb_input.metadata, "Plan of Action and Milestones")

    items = []
    for it in pb_input.items:
        props = [
            _prop("control-id", it.control_id),
            _prop("severity", it.severity or "moderate"),
        ]
        if it.owner:
            props.append(_prop("owner", it.owner))
        if it.due_date:
            props.append(_prop("due-date", it.due_date))
        if it.milestone:
            props.append(_prop("milestone", it.milestone))
        items.append(
            PoamItem(
                uuid=str(uuid.uuid4()),
                title=it.title or f"Open finding for control {it.control_id}",
                description=it.description or "Open finding requiring remediation.",
                props=props,
            )
        )
    # OSCAL requires poam-items to be a non-empty list. An audit period
    # with zero open findings still produces a valid (informative) POA&M
    # with a single "no open findings" item.
    if not items:
        items.append(
            PoamItem(
                uuid=str(uuid.uuid4()),
                title="No open findings",
                description="The frozen audit period has no open findings "
                "(no failing control evaluations and no open audit notes).",
                props=[_prop("severity", "informational")],
            )
        )

    poam = PlanOfActionAndMilestones(
        uuid=str(uuid.uuid4()),
        metadata=meta,
        poam_items=items,
    )
    return poam.oscal_serialize_json().encode("utf-8")


# --------------------------------------------------------------------------
# round-trip validation
# --------------------------------------------------------------------------

_MODEL_BY_TYPE = {
    "system-security-plan": (SystemSecurityPlan, "system-security-plan"),
    "assessment-plan": (AssessmentPlan, "assessment-plan"),
    "assessment-results": (AssessmentResults, "assessment-results"),
    "plan-of-action-and-milestones": (
        PlanOfActionAndMilestones,
        "plan-of-action-and-milestones",
    ),
}


def round_trip_validate(model_type: str, oscal_json: bytes):
    """Parse ``oscal_json`` back through trestle and re-serialize it.

    Returns ``(valid: bool, errors: list[str])``. A document is valid
    only if trestle can deserialize it into the named model AND
    re-serialize it without loss. This is the AC-6 / AC-7 gate.
    """
    entry = _MODEL_BY_TYPE.get(model_type)
    if entry is None:
        return False, [f"unknown model_type: {model_type!r}"]
    model_cls, top_key = entry
    try:
        doc = json.loads(oscal_json)
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        return False, [f"invalid JSON: {exc}"]
    if top_key not in doc:
        return False, [f"document missing top-level key {top_key!r}"]
    try:
        parsed = model_cls(**doc[top_key])
        # Re-serialize: proves the model is whole, not just parseable.
        parsed.oscal_serialize_json()
    except Exception as exc:  # noqa: BLE001 — trestle raises pydantic + ValueError
        return False, [f"{model_type} failed trestle round-trip: {exc}"]
    return True, []


# --------------------------------------------------------------------------
# catalog import (ingest direction — slice 492)
# --------------------------------------------------------------------------

# Bounds on the inbound document (threat-model D / I — see decisions log D3).
# Enforced here in the bridge (the parser) as defense-in-depth; the Go side
# enforces the same byte cap BEFORE the bytes cross the wire.
MAX_CATALOG_BYTES = 16 * 1024 * 1024  # 16 MiB
MAX_CATALOG_CONTROLS = 10_000


class ImportedControl:
    """A normalized control extracted from an imported OSCAL catalog.

    Plain attribute holder (not a protobuf) so ``import_catalog`` is unit
    testable without the generated stubs. The server maps these onto the
    ``ImportedControl`` protobuf message.
    """

    __slots__ = ("control_id", "title", "statement", "group_path")

    def __init__(self, control_id: str, title: str, statement: str, group_path: str):
        self.control_id = control_id
        self.title = title
        self.statement = statement
        self.group_path = group_path


class ImportResult:
    """Result of ``import_catalog`` — mirrors ``ImportCatalogResponse``."""

    __slots__ = ("valid", "errors", "controls", "oscal_version", "catalog_title")

    def __init__(self, valid, errors, controls, oscal_version, catalog_title):
        self.valid = valid
        self.errors = errors
        self.controls = controls
        self.oscal_version = oscal_version
        self.catalog_title = catalog_title


def _flatten_prose(parts) -> str:
    """Flatten an OSCAL part/prose tree into a single statement string.

    Only in-document ``prose`` text is read — no ``href`` / link is ever
    dereferenced (P0-492-2 / threat-model I). ``links`` are intentionally
    ignored: they may carry external ``href`` values that this importer
    treats as opaque metadata, never fetched.
    """
    if not parts:
        return ""
    chunks: list[str] = []
    for part in parts:
        prose = getattr(part, "prose", None)
        if prose:
            chunks.append(prose.strip())
        nested = getattr(part, "parts", None)
        if nested:
            nested_text = _flatten_prose(nested)
            if nested_text:
                chunks.append(nested_text)
    return "\n\n".join(c for c in chunks if c)


def _collect_controls(controls, group_path: str, acc: list[ImportedControl]) -> None:
    """Walk a control list (and nested controls), appending projections."""
    if not controls:
        return
    for ctl in controls:
        acc.append(
            ImportedControl(
                control_id=ctl.id,
                title=ctl.title or "",
                statement=_flatten_prose(getattr(ctl, "parts", None)),
                group_path=group_path,
            )
        )
        # OSCAL controls may nest sub-controls (control enhancements).
        _collect_controls(getattr(ctl, "controls", None), group_path, acc)


def _collect_groups(groups, parent_path: str, acc: list[ImportedControl]) -> None:
    """Walk a group tree, collecting every control with its group path."""
    if not groups:
        return
    for grp in groups:
        title = grp.title or grp.id or ""
        path = f"{parent_path}/{title}" if parent_path else title
        _collect_controls(getattr(grp, "controls", None), path, acc)
        _collect_groups(getattr(grp, "groups", None), path, acc)


def import_catalog(oscal_json: bytes, source_label: str = "") -> ImportResult:
    """Deserialize + validate an inbound OSCAL catalog JSON document.

    Returns an ``ImportResult``. ``valid=False`` carries a structured
    error and an empty control list — the Go side persists NOTHING in that
    case (AC-5 / P0-492-3). No ``href`` / external resource is dereferenced
    (P0-492-2). A document over ``MAX_CATALOG_BYTES`` or with more than
    ``MAX_CATALOG_CONTROLS`` controls is rejected (threat-model D / AC-3).
    """
    if len(oscal_json) > MAX_CATALOG_BYTES:
        return ImportResult(
            valid=False,
            errors=[
                f"catalog document is {len(oscal_json)} bytes, "
                f"over the {MAX_CATALOG_BYTES}-byte import cap"
            ],
            controls=[],
            oscal_version="",
            catalog_title="",
        )

    try:
        doc = json.loads(oscal_json)
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        return ImportResult(False, [f"invalid JSON: {exc}"], [], "", "")

    if not isinstance(doc, dict) or "catalog" not in doc:
        return ImportResult(False, ["document missing top-level key 'catalog'"], [], "", "")

    try:
        catalog = Catalog(**doc["catalog"])
    except Exception as exc:  # noqa: BLE001 — trestle raises pydantic + ValueError
        return ImportResult(False, [f"catalog failed OSCAL v1.1.x validation: {exc}"], [], "", "")

    controls: list[ImportedControl] = []
    _collect_controls(getattr(catalog, "controls", None), "", controls)
    _collect_groups(getattr(catalog, "groups", None), "", controls)

    if len(controls) > MAX_CATALOG_CONTROLS:
        return ImportResult(
            valid=False,
            errors=[
                f"catalog has {len(controls)} controls, "
                f"over the {MAX_CATALOG_CONTROLS}-control import cap"
            ],
            controls=[],
            oscal_version="",
            catalog_title="",
        )

    if not controls:
        return ImportResult(False, ["catalog contains zero controls"], [], "", "")

    meta = catalog.metadata
    oscal_version = getattr(meta, "oscal_version", "") or ""
    catalog_title = getattr(meta, "title", "") or ""
    return ImportResult(True, [], controls, str(oscal_version), str(catalog_title))
