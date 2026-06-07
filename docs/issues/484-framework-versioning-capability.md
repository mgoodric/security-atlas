# 484 — Framework-versioning capability (multiple live versions + migration-suggest job)

**Cluster:** Catalog
**Estimate:** L (2-3d) — version-aware reads + the migration-suggest job + UI
version selection
**Type:** JUDGMENT (the 1:1 migration-suggestion heuristic + the
deprecated-version read policy are subjective calls)
**Status:** `not-ready` (depends on a second version of an already-loaded
framework existing to migrate between — see Dependencies)

## Narrative

Roadmap §10.1 explicitly defers "framework versions beyond SOC 2:2017" from the
MVP. The phase-2 framework expansion (ISO/PCI/CSF/HIPAA) has so far added
_different frameworks_, each at a single version — none has exercised the
**multiple-live-versions-of-one-framework** capability the canvas commits to in
§3.3. This slice ships that capability.

The canvas §3.3 versioning strategy is specific:

> - `FrameworkVersion` is immutable once `status='current'`. Changes ship as new
>   versions.
> - Mappings (`requirement → SCF`) are pinned to a `FrameworkVersion` AND a SCF
>   release. The mapping table has its own version lineage.
> - A `framework_version_migration` job suggests likely 1:1 mappings between
>   adjacent versions, flagging the rest for human review. Rotting is bounded by
>   SCF release cadence (quarterly), not the user's audit calendar.

The **schema foundation already exists**: `framework_versions` has a `version`
column with `UNIQUE (framework_id, version)`, a `status` enum
(`framework_version_status`, default `current`), `effective_from`/`effective_to`
dates, and `frameworks.latest_version_id` as the current-version pointer
(`migrations/sql/20260511000000_init.sql`). The loader already pins requirements

- edges to a `framework_version_id`. So storage is ready; what is missing is the
  **capability layer**:

1. **A version lifecycle** — promoting a new version to `current` and
   transitioning the prior version to a `superseded`/`deprecated` status,
   honoring the §3.3 immutability rule (a `current` version's requirements are
   frozen; changes ship as a new version, not in-place edits).
2. **The `framework_version_migration` suggest job** — given two adjacent
   versions of the same framework, emit suggested 1:1 requirement mappings
   (by code-match + title similarity) and flag the rest for human review. This
   is the §3.3 job, named in the canvas but unbuilt.
3. **Version-aware reads** — the `/anchors` + `/coverage` reads already accept a
   `?framework_version=slug:version` pin (the endpoint doc notes it); this slice
   makes the pin authoritative across versions and defines the
   default-to-`current` + deprecated-version read policy.
4. **UI version selection** — a version selector on the framework/control views
   so an operator running a SOC 2:2017 audit while the catalog also holds a
   future SOC 2 revision sees the right version's requirements.

**Concrete first exercise.** The cleanest way to prove the capability without
waiting for a real new standard revision is to load a **second version of an
already-shipped framework** (e.g. a synthetic or genuine adjacent SOC 2 / ISO
revision) and demonstrate: both versions coexist, the migration job suggests the
1:1 carryover mappings, the rest are flagged for review, and the read path
returns the pinned version's requirements without cross-contamination.

**Scope discipline.** Version lifecycle + migration-suggest job + version-aware
read policy + UI selector. It does **not** auto-apply suggested migrations (a
human reviews — consistent with the AI-assist boundary "no auto-approve its own
mappings"), does **not** ship cross-version diff visualization (a richer
follow-on), and does **not** retroactively re-version the existing single-version
frameworks (they stay `current`; the capability is exercised by adding a second
version). **Follow-on slices:** cross-version requirement-diff UI; automated
re-mapping on SCF release bump.

## Threat model (STRIDE)

This slice adds a version-promotion lifecycle (catalog-write) + a suggest job +
version-aware reads. The integrity asset is the **version pin**: an audit run
against SOC 2:2017 must draw from 2017's requirements, never silently from a
newer version's — a version-pin bug is an audit-correctness failure.

**S — Spoofing.** Version promotion is a catalog-write capability. An
under-privileged caller must not promote/deprecate a version.
**Mitigation:** the promotion endpoint reuses the 438 catalog-write
admin/maintainer boundary; no new anonymous surface. The suggest job is an
offline CLI/admin op.

**T — Tampering.** Two risks: (a) editing a `current` version's requirements
in-place (violating the §3.3 immutability rule), and (b) the migration job
silently rewriting mappings.
**Mitigation:** the immutability rule is enforced — a `current` version's
requirements are not editable; changes require a new version. The migration job
only _suggests_ (writes to a review queue), never auto-applies; a human approves
each suggested mapping. No in-place mutation of frozen versions.

**R — Repudiation.** Version promotion + migration approvals must be auditable.
**Mitigation:** version-status transitions and migration approvals append audit
rows (who promoted/approved, when), mirroring the slice-018 approval-record
pattern.

**I — Information disclosure.** Versions + requirements are catalog reference
data (not tenant-confidential); the suggest job operates on catalog data.
**Mitigation:** no tenant-confidential field is added; the version-aware
`/anchors` read keeps the existing catalog-reference-only payload discipline
(no tenant state). The §3.5 cross-tenant read note still holds.

**D — Denial of service.** The migration suggest job compares two versions'
requirement sets — O(n×m) similarity but bounded by a single framework's
requirement count (hundreds, not millions), and it is an offline job, not a
request hot path.
**Mitigation:** the job is CLI/admin offline; the version-aware read pins to one
version (bounded). No unbounded all-versions scan on the hot path.

**E — Elevation of privilege.** Version promotion + migration approval are
admin/maintainer capabilities.
**Mitigation:** both reuse the catalog-write role boundary; a non-admin
promotion attempt is rejected 403 (asserted in test). The suggest job's output
is a review queue, not an applied change — approval is the privileged act.

## Acceptance criteria

**Backend — version lifecycle**

- [ ] **AC-1.** A version-promotion path moves a newly-loaded
      `framework_version` to `current` and transitions the prior version to a
      `superseded`/`deprecated` status, updating `frameworks.latest_version_id`.
      Reversible, audited.
- [ ] **AC-2.** A `current` version's requirements are immutable (§3.3): an
      attempt to edit a frozen version's requirements is rejected; changes ship
      as a new version.

**Backend — migration-suggest job**

- [ ] **AC-3.** A `framework_version_migration` CLI/admin job, given two
      adjacent versions of the same framework, emits suggested 1:1 requirement
      mappings (code-match + title similarity heuristic) into a review queue and
      flags the unmatched remainder for human review. It does NOT auto-apply.
- [ ] **AC-4.** Suggested migrations are human-approved one at a time; approval
      is audited (who, when).

**Backend — version-aware reads**

- [ ] **AC-5.** `GET /v1/requirements/{slug}/anchors` + `/coverage` with
      `?framework_version=slug:version` return the pinned version's requirements
      without cross-version contamination; absent the pin, the read defaults to
      the framework's `current` version. The deprecated-version read policy is
      defined (deprecated versions are readable when explicitly pinned).

**Frontend — version selection**

- [ ] **AC-6.** A version selector on the framework/control view lets the
      operator choose which version's requirements to view; vitest covers the
      BFF version param; a Playwright assertion covers the selector.

**Tests**

- [ ] **AC-7.** Integration test (`//go:build integration`): two versions of one
      framework coexist; a pinned read returns the correct version's
      requirements; the migration job suggests the 1:1 carryovers + flags the
      rest, against real Postgres.
- [ ] **AC-8.** Integration test (threat-model T): editing a `current` version's
      requirements in-place is rejected.
- [ ] **AC-9.** Integration test (threat-model E): a non-admin version-promotion
      attempt is rejected 403.

**Docs / JUDGMENT artifact + ADR**

- [ ] **AC-10.** An ADR (`docs/adr/NNNN-framework-versioning-capability.md`)
      records the version lifecycle, the migration-suggest heuristic, the
      deprecated-version read policy, and the immutability enforcement — the
      §3.3 strategy made concrete.
- [ ] **AC-11.** A decisions log
      (`docs/audit-log/484-framework-versioning-decisions.md`) records the 1:1
      suggestion heuristic, the deprecated-read policy, confidence, and "Revisit
      once in use." Include the `detection_tier_actual` /
      `detection_tier_target` header.
- [ ] **AC-12.** A changelog entry.

## Constitutional invariants honored

- **#1 — One control, N framework satisfactions.** Versioning is the canvas's
  argument for _why_ the graph beats flat crosswalks: "an ISO 27001:2013 → ISO
  27001:2022 update changes only the edges from ISO requirements to SCF, not the
  SCF graph itself" (§3.1). This slice realizes that property.
- **#7 — Mappings go requirement → SCF anchor.** The migration job suggests
  requirement → requirement _carryover_ mappings BETWEEN versions for review,
  but the SCF-anchor edges are re-pinned to the new version — no
  cross-framework requirement → requirement edge is created.
- **AI-assist boundary (hard) — "no auto-approve its own mappings."** The
  migration job suggests; a human approves. Nothing auto-applies.
- **#10 — Audit-period freezing.** Version pinning composes with freezing: a
  frozen audit period draws from the version pinned at freeze time (noted for
  the implementing agent; the freeze machinery is slice 028).

## Canvas references

- `Plans/canvas/03-ucf.md` §3.3 — the versioning strategy this slice implements
  (immutable current version, mapping lineage, the `framework_version_migration`
  job).
- `Plans/canvas/03-ucf.md` §3.1 — the "versioning changes only the edges, not
  the SCF graph" property.
- `Plans/canvas/10-roadmap.md` §10.1 — "framework versions beyond SOC 2:2017"
  deferred from MVP; this is the phase-2 capability.

## Dependencies

- **#438** (generic crosswalk loader) — `merged`. Loads each version's
  requirements + edges pinned to a `framework_version_id`.
- **#006** (SCF catalog importer) — `merged`. The SCF-release pin half of the
  §3.3 dual-pin.
- **A second version of an already-loaded framework** — the capability needs two
  versions of one framework to migrate between. This slice loads that second
  version (synthetic or a genuine adjacent revision) as part of its proof.
  Marked `not-ready` until at least one framework has a stable first version on
  `main` to add a second version to (SOC 2:2017 / ISO:2022 qualify — so the
  block is light; flip to `ready` once the implementing agent picks the concrete
  second version to load).
- **#028** (audit-period freezing) — `merged`. Composes; version pin × freeze.

## Anti-criteria (P0 — block merge)

- **P0-484-1.** Does NOT auto-apply suggested migrations — a human approves each
  (AI-assist boundary; AC-4).
- **P0-484-2.** Does NOT mutate a `current` (frozen) version's requirements
  in-place (§3.3 immutability; threat-model T; AC-8).
- **P0-484-3.** Does NOT let a non-admin promote/deprecate a version
  (threat-model E; AC-9).
- **P0-484-4.** Does NOT create cross-framework requirement → requirement edges;
  the only requirement → requirement suggestions are intra-framework
  version-carryover suggestions for review (invariant #7).
- **P0-484-5.** A version-pinned read does NOT return another version's
  requirements (version-pin integrity; AC-5/AC-7).
- **P0-484-6.** Does NOT retroactively re-version the existing single-version
  frameworks — they stay `current`; the capability is exercised by adding a
  second version.
- **P0-484-7.** Does NOT ship cross-version diff visualization — that is a
  follow-on.

## Skill mix (3-5)

`grill-with-docs` · `database-designer` (version lifecycle state + the
migration-review-queue table + immutability enforcement) · `tdd`
(integration-first; the version-pin integrity + immutability + role-gate
assertions are load-bearing) · `security-review` (catalog-write lifecycle +
version-pin audit correctness) · `simplify`.

## Notes for the implementing agent

- The schema already supports multiple versions (`UNIQUE (framework_id,
version)`, the `status` enum, `latest_version_id`). The work is the lifecycle
  - the suggest job + the version-aware read policy + the UI selector — NOT a
    schema rebuild. Confirm what the existing `framework_version_status` enum
    values are before adding a `superseded`/`deprecated` value.
- **JUDGMENT calls you own:** the 1:1 migration-suggestion heuristic (code-match
  - title similarity threshold), the deprecated-version read policy
    (readable-when-pinned vs hidden), and the concrete second framework version to
    load for the proof. Record all three in the decisions log.
- AC-5/AC-7 (version-pin integrity — a pinned read never bleeds another
  version's requirements) is the load-bearing audit-correctness assertion.
- `not-ready` is a light block: flip to `ready` once the second version to load
  is chosen. If picked up cold, choose SOC 2:2017 + a synthetic adjacent
  revision for the proof and proceed.
- Detection-tier classification: set both fields to `none` unless a bug
  surfaces during the build.
