# ADR 0019 — Framework-versioning capability (multiple live versions + migration-suggest)

**Status:** Accepted

**Date:** 2026-06-15

**Resolves:** [`Plans/canvas/10-roadmap.md`](../../Plans/canvas/10-roadmap.md) §10.1 ("framework versions beyond SOC 2:2017" deferred from MVP) + makes [`Plans/canvas/03-ucf.md`](../../Plans/canvas/03-ucf.md) §3.3 versioning strategy concrete.

**Implements through:** [`docs/issues/484-framework-versioning-capability.md`](../issues/484-framework-versioning-capability.md)

---

## Context

Canvas §3.3 commits to multiple live versions of one framework: `FrameworkVersion` is immutable once `current`, changes ship as new versions, mappings are pinned to a `FrameworkVersion` **and** a SCF release, and a `framework_version_migration` job suggests likely 1:1 carryover mappings between adjacent versions. The **schema foundation already exists** (`framework_versions` with `UNIQUE (framework_id, version)`, a `framework_version_status` enum already carrying `current`/`deprecated`/`superseded`, `effective_from`/`effective_to`, and `frameworks.latest_version_id`; the loader already pins requirements + edges to a `framework_version_id`).

What is missing is the **capability layer**: a version lifecycle, the migration-suggest job, a version-aware read policy, and a UI selector. The integrity asset is the **version pin** — an audit run against SOC 2:2017 must draw from 2017's requirements, never silently from a newer version's. The phase-2 expansion has so far added _different_ frameworks each at a single version, so the multiple-versions-of-one-framework path is unexercised.

## Decision

### 1. Version lifecycle

A promotion path moves a newly-loaded `framework_version` to `current` and transitions the prior version to **`superseded`** (reusing the existing enum value; `superseded` = "replaced by a newer current version but still valid for historical audits", as distinct from `deprecated` = "discouraged"). Promotion updates `frameworks.latest_version_id`, is audited (who promoted, when — mirroring the slice-018 approval-record pattern), and is reversible. Promotion is an **admin/maintainer** catalog-write capability (reuse the slice-438 boundary); a non-admin promotion is rejected 403.

### 2. Immutability (§3.3)

A `current` version's requirements are **frozen** — an attempt to edit them in-place is rejected; changes ship as a new version. No in-place mutation of a frozen version.

### 3. Migration-suggest heuristic — exact requirement-code match only

The `framework_version_migration` CLI/admin job, given two adjacent versions of the same framework, emits a suggested 1:1 carryover **only when the requirement code matches exactly** between the two versions. Everything else (renamed, split, merged, new, removed requirements) is **flagged for human review** — not auto-suggested. We chose exact-code-match over a fuzzier code+title-similarity heuristic to maximize precision: a low-false-positive suggestion set the reviewer can trust, rather than a noisier set that erodes trust in the suggestions. (A title-similarity pass is a possible future enhancement once operators have used the exact-match version and asked for more recall.)

The job **only suggests** (writes to a review queue); it **never auto-applies**. A human approves each suggested carryover one at a time, and each approval is audited (who, when) — consistent with the AI-assist "no auto-approve its own mappings" boundary.

### 4. Version-aware read policy

- A read pinned with `?framework_version=slug:version` returns **that** version's requirements, with no cross-version contamination (the load-bearing audit-correctness property).
- Absent the pin, the read defaults to the framework's `current` version.
- **`superseded` versions are readable when explicitly pinned** (a historical audit can still draw from the version it was conducted against); they are simply not the default. They are not hidden.
- Version pinning composes with audit-period freezing (slice 028): a frozen period draws from the version pinned at freeze time.

### 5. Proof exercise — synthetic adjacent SOC 2 revision

To exercise the capability without waiting for a real new standard revision, the slice loads a **synthetic adjacent SOC 2 revision** (clearly labeled synthetic) alongside SOC 2:2017 and demonstrates: both versions coexist, the migration job suggests the exact-code 1:1 carryovers and flags the rest, and a pinned read returns the correct version's requirements without bleed. This is the fastest fully-controlled proof; loading a genuine adjacent standard (e.g. ISO 27001:2013 → :2022) is deferred to a later real-data slice.

### 6. Out of scope

- Cross-version requirement-diff visualization (richer follow-on).
- Automated re-mapping on SCF release bump (follow-on).
- Retroactively re-versioning the existing single-version frameworks — they stay `current`; the capability is exercised by adding a second version.

## Consequences

- Realizes invariant #1's payoff: a framework version bump changes only the edges from that framework's requirements to SCF, not the SCF graph itself (§3.1).
- The version pin becomes an audit-correctness boundary, tested end-to-end (a pinned read never returns another version's requirements).
- No schema rebuild — the lifecycle + job + read policy + UI selector are the work; the enum already has the needed status values.
- The exact-code-match heuristic favors a small, trustworthy suggestion set over recall; the review queue absorbs everything else.

## Alternatives considered

- **Code-match + title-similarity heuristic.** Rejected as the default: higher recall but more false positives / review noise; precision-first is the safer starting point for a human-reviewed queue.
- **`deprecated` (vs `superseded`) for the prior version.** Rejected: `deprecated` connotes "discouraged"; a superseded standard revision remains valid for the audits conducted against it — `superseded` is the accurate term.
- **Hiding superseded versions from reads.** Rejected: a historical audit must be able to pin and read the version it ran against.
- **Genuine ISO 27001:2013 → :2022 as the proof.** Deferred: more real-world value but more sourcing/verification effort; a synthetic SOC 2 revision proves the capability faster and is clearly labeled.

## Implementation notes (slice 484)

Recorded when the capability landed; these refine the decision above against the schema as it actually exists on `main`. The full rationale + confidence + "revisit once in use" live in [`docs/audit-log/484-framework-versioning-decisions.md`](../audit-log/484-framework-versioning-decisions.md).

1. **"Superseded" status = the existing `legacy` enum value.** §1 above calls the prior-version status `superseded` and asserts that value already exists in the enum. The actual `framework_version_status` enum on `main` is `{ current, legacy, withdrawn }` — it has **no** `superseded`/`deprecated` value. The existing value that carries the intended "replaced-but-still-valid-for-historical-audits" semantic is **`legacy`** (and the `soc2import` loader already demotes a superseded version to `legacy`). The implementation therefore uses `legacy` AS the ADR's "superseded" status, and `withdrawn` as the ADR's "deprecated"/discouraged. No enum change was needed — the ADR's core premise ("the enum already has the needed status values; no schema rebuild") holds once `legacy` is read as "superseded". The read policy is unchanged in substance: a `legacy` version is readable when explicitly pinned, never the default.

2. **Immutability is enforced by a DB trigger on UPDATE only** (`trg_framework_requirement_immutability`), not on DELETE. §3.3 / threat-model T forbid _editing_ a frozen version's requirements in place ("changes ship as a new version, not in-place edits") — that is an UPDATE. DELETE is a legitimate catalog-lifecycle operation (tearing down an obsolete version, FK-cascade or explicit GC); a DELETE guard would make every frozen version permanently undeletable by any role (it fires even on a CASCADE and even for the superuser) and break catalog GC + test teardown. For the application role (`atlas_app`) immutability is additionally GRANT-enforced (SELECT-only on `framework_requirements`), so the trigger is the defense-in-depth that also stops the privileged loader (`atlas_migrate`) from a content-changing in-place re-import.

3. **Write mechanism = narrow column grants** (not a privileged pool): `GRANT UPDATE(status) ON framework_versions` + `GRANT UPDATE(latest_version_id) ON frameworks` to `atlas_app`, plus SELECT/INSERT on the review queue + audit, mirroring slice 483's pattern. The lifecycle legality is enforced server-side in `internal/frameworkversion`; the trust gate is the admin-role authz check.

4. **The migration-suggest job is an admin ENDPOINT** (`POST /v1/admin/framework-versions/migrations:suggest`), not a CLI subcommand. The issue allowed either ("CLI/admin job"); the endpoint reuses the same admin-gated `/v1` surface as the promotion + approve/reject routes, so the whole capability has one authz boundary and one audit path.

5. **The version-migration review queue is an intra-framework table, not an invariant-#1 crosswalk.** `framework_version_migrations` is catalog-level (no `tenant_id`) and carries two FKs into `framework_requirements` (`from_requirement_id`, `to_requirement_id`). Its rows are intra-framework version carryovers between two adjacent versions of the SAME framework (P0-484-4 / invariant #7) — never a cross-framework requirement→requirement bridge. The slice-007 invariant-#1 FK guard (`TestImport_NoDirectRequirementToRequirementTableExists`) was refined to exclude this table by name, exactly as `fw_to_scf_edges` is the one allowed crosswalk referencer; a genuinely-new cross-framework bridge with any other name is still caught.
