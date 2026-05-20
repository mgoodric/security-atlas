# 179 — `schema-removal-age` CI check (enforce 90-day deprecation window)

**Cluster:** Infra / Quality
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

**WHY.** Canvas open questions #9 + #17 resolved 2026-05-20 lock a 90-day deprecation window for breaking-major schema bumps: when a v2.0.0 lands, v1.x.x stays in the registry for at least 90 days. The contributor-side rule lives in `CONTRIBUTING.md` "Contributing an `evidence_kind` schema". Today the floor is a maintainer-discipline rule — no CI enforcement. This slice adds the CI check that makes the floor structural.

**WHAT.** A new CI job `Schema · removal-age (90-day floor)` that runs on every PR touching `internal/api/schemaregistry/schemas/`. For each schema-version file the PR removes, the check:

1. Finds the file's introduction date on `main` via `git log --diff-filter=A --format=%cI -- <path>`
2. Computes age = `now - introduction_date`
3. If `age < 90 days`, the check fails with a clear error message naming the file + age + remaining days until eligible
4. The check is **bypassable** via the `[deprecation-override]` PR label (escape hatch for emergencies — e.g., a schema published with a security-sensitive defect must be unpublished immediately). Override requires a maintainer's approval AND an audit-log entry under `docs/audit-log/` documenting the override reason.

**SCOPE DISCIPLINE — what's deliberately out.**

- **Not adding** the schema-removal-age check to branch-protection required-checks in this slice. The check ships as informational/required-by-convention; promotion to branch-protection is a follow-on slice after the check proves stable.
- **Not** rewriting the existing `additive.go` / `semver.go` enforcement (slice 014 already shipped those structurally; this slice only adds the deprecation-window dimension).
- **Not** tracking deprecations in a separate registry table — the introduction-date floor is computed from git history, not from a deprecation-metadata table. Simpler; matches the OpenTelemetry pattern.
- **Not** UI-surfacing the deprecated-since timestamp on records in this slice. The CONTRIBUTING.md rule mentions a UI marker; that's a separate frontend slice if/when needed.

## Threat model

Pure CI-job slice — minimal threat surface.

**S — Spoofing.** None.

**T — Tampering.** A bad-actor PR could try to remove a schema file with a forged "introduction date" claim. Mitigation: the check reads ONLY from `git log` on `main`, not from any PR-mutable source. PR cannot rewrite `main`'s history.

**R — Repudiation.** The override label leaves a permanent audit trail (PR has the label visible in the merge commit's PR linkage; the required audit-log entry under `docs/audit-log/` documents the rationale).

**I — Information disclosure.** The CI job runs on public schema files; no tenant data.

**D — Denial of service.** None (CI-internal job, bounded runtime).

**E — Elevation of privilege.** Only maintainers can apply the `[deprecation-override]` label (GitHub branch-protection label enforcement). Non-maintainer override attempts fail the check.

**Verdict.** **has-mitigations** — override-label gating + git-log-on-main as the trust root.

## Acceptance criteria

### CI check construction

- **AC-1.** NEW shell script at `cmd/scripts/schema-removal-age-check` (or `.sh`) takes a list of removed schema-version files (paths) and validates each against the 90-day floor.
- **AC-2.** Script reads introduction date via `git log --diff-filter=A --format=%cI -- <path>` on `main`.
- **AC-3.** Script emits a clear error message per failing file: `<path> introduced <date>, age <N> days, must be >= 90 days; remaining: <90-N> days. Override with [deprecation-override] label + audit-log entry.`
- **AC-4.** Script exits 0 if all removed files satisfy the floor; exits 1 otherwise.
- **AC-5.** Script honors `SCHEMA_REMOVAL_OVERRIDE=1` env var to bypass — set by the CI job when the PR has the `[deprecation-override]` label.

### CI integration

- **AC-6.** NEW job `Schema · removal-age (90-day floor)` in `.github/workflows/ci.yml` (or appropriate workflow). Runs only on PRs that touch `internal/api/schemaregistry/schemas/**`.
- **AC-7.** Job computes the removed-file list via `git diff --diff-filter=D --name-only origin/main...HEAD -- internal/api/schemaregistry/schemas/`.
- **AC-8.** Job reads the PR's labels via `gh pr view --json labels` and exports `SCHEMA_REMOVAL_OVERRIDE=1` when the `[deprecation-override]` label is present.
- **AC-9.** Job fails when AC-1's script exits non-zero (i.e., any removal violates the floor and no override is present).

### Tests

- **AC-10.** Unit test for the script: fixture-driven test cases for (a) all removals satisfy floor (pass), (b) one removal violates floor (fail), (c) override env var bypasses failure, (d) no removals at all (pass with no output).
- **AC-11.** Integration test: CI job runs against a synthetic PR that removes a known-young schema; assert the job fails. Companion synthetic PR that removes a known-old schema (>= 90 days); assert the job passes.

### Documentation

- **AC-12.** README block in `internal/api/schemaregistry/schemas/README.md` (or the `schemas/README.md` discovery breadcrumb) explaining the 90-day floor + override workflow.
- **AC-13.** CHANGELOG entry under `[Unreleased] / Added`: "Schema-removal age CI check enforcing the 90-day deprecation window for breaking-major bumps (#179)."

## Constitutional invariants honored

- **Schema-of-evidence governance** (resolved OQ #9/#17): this slice operationalizes the 90-day deprecation rule that the canvas resolution commits to.
- **OSCAL is the wire format, not the daily data model** (invariant #8): the deprecation window applies to internal evidence_kind schemas, not OSCAL wire types. OSCAL versioning is governed by NIST, not us.
- **Append-only evidence ledger** (invariant #2): `v1` records remain queryable forever even after the schema is removed from the registry. The CI check protects the registry's deprecation discipline; it doesn't touch the ledger.

## Canvas references

- `Plans/canvas/11-open-questions.md` #9 and #17 (resolved 2026-05-20)
- `Plans/canvas/04-evidence-engine.md` §4 (evidence engine + schema registry)
- `Plans/EVIDENCE_SDK.md` §4.5 (schema registry design)
- OpenTelemetry semantic-conventions deprecation model (pattern source)

## Dependencies

- **#014** (Schema registry + additive.go + semver.go) — `merged`. This slice operationalizes the deprecation-window dimension on top of slice 014's foundation.

## Anti-criteria (P0 — block merge)

- **P0-179-1.** Script reads introduction dates ONLY from `git log` on `main`. Does NOT accept a PR-supplied "introduction date" via filename, frontmatter, or any other PR-mutable source.
- **P0-179-2.** Override label is `[deprecation-override]` (exact spelling). Other labels MUST NOT bypass the check. The label name is hard-coded in the CI job (not a configurable input).
- **P0-179-3.** Override REQUIRES a maintainer's approval AND an audit-log entry under `docs/audit-log/`. The CI job does NOT enforce the audit-log presence (that's a PR-review-time check); the maintainer's approval is the structural gate.
- **P0-179-4.** Does NOT add this job to `.github/branch-protection.json` required-checks in this slice. Promotion to branch-protection is a follow-on slice after the check proves stable across at least one schema-deletion PR.
- **P0-179-5.** Does NOT introduce a deprecation-metadata database table. Introduction date is read from git history; deprecation is a function of (a) presence in main + (b) age. No new database surface.
- **P0-179-6.** Script does NOT panic / crash on edge cases (file not present on `main`, empty input, malformed git output). Exits with a clear error and useful message.
- **P0-179-7.** Neutral test-fixture tokens only (slice 005 convention).

## Skill mix (3-5)

1. **Bash + `git log`** — script implementation
2. **GitHub Actions workflow YAML** — CI job authoring (slice 069 path-filter pattern as reference)
3. **`gh` CLI for PR-label introspection** — AC-8
4. **CHANGELOG + README discipline** — AC-12 / AC-13

## Notes for the implementing agent

### Surfaced via OQ #9/#17 resolution

This slice is the implementation half of the OQ #9 + #17 resolution. The governance rule lives in `CONTRIBUTING.md` "Contributing an `evidence_kind` schema"; this slice makes the floor structural via CI.

### Implementation order

1. Build the script (`cmd/scripts/schema-removal-age-check`) + unit tests against fixtures
2. Wire the CI job
3. Test the integration via a synthetic local PR (delete a known-young schema, assert the job fails)
4. CHANGELOG + README update

### Spillover candidates

If during this slice an out-of-scope finding emerges:

- **UI marker for deprecated records** — separate frontend slice (mentioned in CONTRIBUTING.md but not in this slice's scope)
- **Promotion to branch-protection required-checks** — separate slice after the check proves stable
- **Out-of-tree schema registry migration** — gated on the OQ #9 resolution's triggers (count >100 OR PRs >1/week sustained)

### Provenance

Filed 2026-05-20 as the implementation slice for the OQ #9 + #17 resolution. The governance rules landed in the same PR as this slice doc (per the maintainer-recommended-bundle session). Engineer at pickup time should ensure CONTRIBUTING.md and this slice's CI check stay in sync (any rule change in one needs reflecting in the other).
