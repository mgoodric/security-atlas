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
