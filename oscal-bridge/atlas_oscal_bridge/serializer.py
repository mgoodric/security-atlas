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
import pathlib
import re
import shutil
import tempfile
import uuid
from datetime import UTC, datetime

from trestle.core.control_interface import ParameterRep
from trestle.core.profile_resolver import ProfileResolver
from trestle.oscal.assessment_plan import AssessmentPlan
from trestle.oscal.assessment_results import AssessmentResults, ImportAp, Result
from trestle.oscal.catalog import Catalog
from trestle.oscal.common import (
    ControlSelection,
    ImportSsp,
    Metadata,
    Observation,
    Property,
    RelevantEvidence,
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
                # Slice 494: walkthrough attachments map onto this same
                # observation's relevant-evidence (decision D2) — co-located
                # with the walkthrough they evidence (canvas §8.3 / §8.5). The
                # bytes are NEVER embedded: href = object-storage URI, the
                # content hash + type + annotation ref ride as props.
                relevant_evidence=_walkthrough_evidence(wt) or None,
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


def _walkthrough_evidence(wt) -> list:
    """Map a walkthrough's attachments to OSCAL ``relevant-evidence`` (D2).

    Each attachment becomes one ``RelevantEvidence`` whose ``href`` is the
    object-storage URI (the reference, NOT the bytes — P0-494-2) and whose
    props carry the content hash, content type, and annotation reference. The
    overflow ref (slice 494 D3) has no storage URI; it is carried as a
    description-only note so the auditor knows attachments exist beyond the
    cap.
    """
    out = []
    for att in wt.attachments:
        props = []
        if att.id:
            props.append(_prop("attachment-id", att.id))
        if att.content_hash:
            props.append(_prop("content-hash", att.content_hash))
        if att.content_type:
            props.append(_prop("content-type", att.content_type))
        if att.annotation_ref:
            props.append(_prop("annotation-ref", att.annotation_ref))
        # The overflow ref carries only a filename note (no storage URI);
        # trestle requires href to be a non-empty string, so fall back to a
        # stable in-document anchor when there is no real URI.
        href = att.storage_uri or "#walkthrough-attachment-overflow"
        out.append(
            RelevantEvidence(
                href=href,
                description=att.filename or "Walkthrough attachment.",
                props=props or None,
            )
        )
    return out


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
        # Slice 494 (AC-1): the DRAWN sample evidence ids, one prop per id in
        # shuffle order. The ordinal-suffixed name preserves the deterministic
        # auditor's-sample order (AC-9) so an importer can re-key the draw.
        for ordinal, ev_id in enumerate(pop.sampled_evidence_ids):
            props.append(_prop(f"population-{pop.population_id}-sampled-{ordinal}", ev_id))
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


# --------------------------------------------------------------------------
# profile import (resolve direction — slice 511)
# --------------------------------------------------------------------------

# Bounds on the inbound documents (threat-model D / I — see slice-511 D5).
# Enforced here in the bridge as defense-in-depth; the Go side enforces the
# same byte cap on every document BEFORE the bytes cross the wire.
MAX_PROFILE_BYTES = 16 * 1024 * 1024  # 16 MiB
MAX_RESOLVED_CONTROLS = 10_000

# A trestle:// href is the ONLY href form the resolver may follow after the
# bridge rewrites imports; it resolves to a LocalFetcher read inside our
# sandbox (see slice-511 D2). Every other scheme (https / sftp / file / a
# bare relative path) is an external/host dereference and is rejected.
_TRESTLE_HREF_PREFIX = "trestle://"

# Any href beginning with one of these is an explicit external/host
# reference the bridge MUST NOT dereference (P0-511-1). It is rejected
# BEFORE any catalog matching — a positional match never overrides this
# check, so a single-catalog import cannot smuggle an external fetch.
_EXTERNAL_HREF_PREFIXES = ("https://", "http://", "sftp://", "ftp://", "file:", "//")


def _is_external_href(href: str) -> bool:
    """Report whether ``href`` names an external/host resource to never fetch."""
    h = href.strip().lower()
    return h.startswith(_EXTERNAL_HREF_PREFIXES)


class ProfileImportResult:
    """Result of ``import_profile`` — mirrors ``ImportProfileResponse``."""

    __slots__ = ("valid", "errors", "controls", "oscal_version", "profile_title")

    def __init__(self, valid, errors, controls, oscal_version, profile_title):
        self.valid = valid
        self.errors = errors
        self.controls = controls
        self.oscal_version = oscal_version
        self.profile_title = profile_title


def _profile_reject(errors: list[str]) -> ProfileImportResult:
    return ProfileImportResult(False, errors, [], "", "")


def _catalog_slug(raw: str) -> str:
    """Lowercase a string into a filesystem-safe sandbox directory token."""
    cleaned = re.sub(r"[^a-z0-9]+", "-", (raw or "").lower()).strip("-")
    return cleaned or "catalog"


def _supplied_catalog_keys(catalogs) -> tuple[list[dict], list[str]]:
    """Parse + validate the supplied catalogs into match descriptors.

    Returns ``(descriptors, errors)``. Each descriptor carries the parsed
    document, a unique sandbox key, and the identity tokens an import.href
    may match against (the catalog uuid + the title slug). No href is read
    or fetched here — only the bytes the caller supplied are parsed.
    """
    descriptors: list[dict] = []
    errors: list[str] = []
    seen_keys: set[str] = set()
    for idx, sc in enumerate(catalogs):
        raw = bytes(sc.oscal_json)
        if len(raw) > MAX_PROFILE_BYTES:
            errors.append(
                f"supplied catalog #{idx} is {len(raw)} bytes, "
                f"over the {MAX_PROFILE_BYTES}-byte cap"
            )
            continue
        try:
            doc = json.loads(raw)
        except (json.JSONDecodeError, UnicodeDecodeError) as exc:
            errors.append(f"supplied catalog #{idx}: invalid JSON: {exc}")
            continue
        if not isinstance(doc, dict) or "catalog" not in doc:
            errors.append(f"supplied catalog #{idx}: missing top-level key 'catalog'")
            continue
        cat = doc["catalog"]
        meta = cat.get("metadata", {}) if isinstance(cat, dict) else {}
        title = str(meta.get("title", "")) if isinstance(meta, dict) else ""
        cat_uuid = str(cat.get("uuid", "")) if isinstance(cat, dict) else ""
        base_key = _catalog_slug(title or cat_uuid or f"catalog-{idx}")
        key = base_key
        bump = 1
        while key in seen_keys:
            key = f"{base_key}-{bump}"
            bump += 1
        seen_keys.add(key)
        descriptors.append(
            {
                "doc": doc,
                "key": key,
                "uuid": cat_uuid,
                "title_slug": _catalog_slug(title),
            }
        )
    return descriptors, errors


def _match_import_href(href: str, descriptors: list[dict]) -> dict | None:
    """Map a profile ``import.href`` to a supplied catalog WITHOUT fetching.

    Matching rules (slice-511 D2 — conservative; a non-match errors, never
    fetches):
      * single import + single catalog -> positional match (handled by caller).
      * fragment / uuid match: an href like ``#<uuid>`` or one containing the
        catalog uuid matches that catalog.
      * trailing-segment slug match against the catalog title slug.
    Returns the matched descriptor or ``None``.
    """
    h = href.strip()
    # Strip a leading fragment marker; OSCAL back-matter resolution uses
    # ``#<resource-uuid>`` but we never resolve back-matter — only the raw
    # token is compared against supplied identities.
    token = h[1:] if h.startswith("#") else h
    trailing = token.rstrip("/").rsplit("/", 1)[-1]
    trailing_slug = _catalog_slug(trailing.removesuffix(".json"))
    for d in descriptors:
        if d["uuid"] and (token == d["uuid"] or d["uuid"] in token):
            return d
        if d["title_slug"] and trailing_slug == d["title_slug"]:
            return d
    return None


def _write_sandbox(root: pathlib.Path, descriptors: list[dict], profile_doc: dict) -> pathlib.Path:
    """Lay out an isolated trestle workspace and return the profile path.

    The workspace holds only the supplied catalogs + the (href-rewritten)
    profile. trestle resolves every import as a LocalFetcher read inside
    this dir; no external fetch is reachable (slice-511 D2).
    """
    (root / ".trestle").mkdir()
    (root / ".trestle" / "cache").mkdir()
    for d in descriptors:
        cat_dir = root / "catalogs" / d["key"]
        cat_dir.mkdir(parents=True)
        (cat_dir / "catalog.json").write_text(json.dumps(d["doc"]))
    prof_dir = root / "profiles" / "imported"
    prof_dir.mkdir(parents=True)
    prof_path = prof_dir / "profile.json"
    prof_path.write_text(json.dumps(profile_doc))
    return prof_path


def import_profile(profile_json: bytes, catalogs, source_label: str = "") -> ProfileImportResult:
    """Resolve an inbound OSCAL profile against SUPPLIED catalogs.

    Returns a ``ProfileImportResult``. ``valid=False`` carries a structured
    error and an empty control list — the Go side persists NOTHING in that
    case (AC-5 / P0-511-3). The resolver NEVER dereferences an external
    ``import.href`` (P0-511-1): every href is rewritten to a sandboxed
    ``trestle://`` path before resolution, and an href that maps to no
    supplied catalog is a structured error, not a fetch. A document over the
    byte cap, a resolved set over the control cap, or a chained
    profile-over-profile import is rejected (threat-model D / AC-3).

    ``catalogs`` is an iterable of objects exposing ``.oscal_json`` (the
    protobuf ``SuppliedCatalog`` shape) — declared structurally so the
    function is unit-testable without the generated stubs.
    """
    if len(profile_json) > MAX_PROFILE_BYTES:
        return _profile_reject(
            [
                f"profile document is {len(profile_json)} bytes, "
                f"over the {MAX_PROFILE_BYTES}-byte cap"
            ]
        )
    catalogs = list(catalogs)
    if not catalogs:
        return _profile_reject(["at least one catalog must be supplied to resolve the profile"])

    try:
        profile_doc = json.loads(profile_json)
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        return _profile_reject([f"invalid JSON: {exc}"])
    if not isinstance(profile_doc, dict) or "profile" not in profile_doc:
        return _profile_reject(["document missing top-level key 'profile'"])

    profile = profile_doc["profile"]
    if not isinstance(profile, dict):
        return _profile_reject(["'profile' is not an object"])
    imports = profile.get("imports") or []
    if not isinstance(imports, list) or not imports:
        return _profile_reject(["profile has no 'imports' to resolve"])

    descriptors, cat_errors = _supplied_catalog_keys(catalogs)
    if cat_errors:
        return _profile_reject(cat_errors)

    # Rewrite every import.href to a sandboxed trestle:// path. An href that
    # maps to no supplied catalog is rejected WITHOUT a fetch (P0-511-1).
    #
    # The positional shortcut (single import + single catalog — the FedRAMP
    # baseline-over-catalog common case) only applies AFTER the external-href
    # gate, so it can never smuggle an external reference through (the
    # security bug an unconditional positional match would introduce).
    positional = descriptors[0] if (len(descriptors) == 1 and len(imports) == 1) else None
    for i, imp in enumerate(imports):
        if not isinstance(imp, dict):
            return _profile_reject([f"import #{i} is not an object"])
        href = str(imp.get("href", "")).strip()
        if not href:
            return _profile_reject([f"import #{i} has no href"])
        # PRIMARY guard: an explicit external/host reference is rejected with
        # no fetch and no positional fallback (P0-511-1 / threat-model I).
        if _is_external_href(href):
            return _profile_reject(
                [
                    f"import #{i} href {href!r} is an external reference; "
                    "external resources are never dereferenced"
                ]
            )
        matched = positional or _match_import_href(href, descriptors)
        if matched is None:
            return _profile_reject(
                [
                    f"import #{i} href {href!r} does not map to any supplied catalog; "
                    "external/unknown references are never dereferenced"
                ]
            )
        imp["href"] = f"{_TRESTLE_HREF_PREFIX}catalogs/{matched['key']}/catalog.json"

    root = pathlib.Path(tempfile.mkdtemp(prefix="atlas_profile_ws_"))
    try:
        prof_path = _write_sandbox(root, descriptors, profile_doc)
        try:
            # VALUE_OR_LABEL_OR_CHOICES substitutes an assigned parameter
            # value into the prose (so modify.set-parameters is reflected in
            # the resolved statement) and falls back to the label/choices when
            # a value was not assigned — the resolved prose never carries a
            # raw {{ insert: param }} moustache.
            resolved = ProfileResolver.get_resolved_profile_catalog(
                root,
                str(prof_path),
                param_rep=ParameterRep.VALUE_OR_LABEL_OR_CHOICES,
            )
        except Exception as exc:  # noqa: BLE001 — trestle raises pydantic + ValueError
            return _profile_reject([f"profile resolution failed: {exc}"])

        controls: list[ImportedControl] = []
        _collect_controls(getattr(resolved, "controls", None), "", controls)
        _collect_groups(getattr(resolved, "groups", None), "", controls)

        if len(controls) > MAX_RESOLVED_CONTROLS:
            return _profile_reject(
                [
                    f"resolved profile has {len(controls)} controls, "
                    f"over the {MAX_RESOLVED_CONTROLS}-control cap"
                ]
            )
        if not controls:
            return _profile_reject(["resolved profile contains zero controls"])

        rmeta = resolved.metadata
        oscal_version = str(getattr(rmeta, "oscal_version", "") or "")
        # The profile's OWN declared title is the provenance label, not the
        # resolved catalog's (trestle stamps the resolved catalog with the
        # profile's metadata, so this is the same value either way).
        pmeta = profile.get("metadata", {}) if isinstance(profile, dict) else {}
        profile_title = str(pmeta.get("title", "")) if isinstance(pmeta, dict) else ""
        return ProfileImportResult(True, [], controls, oscal_version, profile_title)
    finally:
        shutil.rmtree(root, ignore_errors=True)
