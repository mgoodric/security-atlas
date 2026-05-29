# 375 — Data retention and disposal policy (governance document)

**Cluster:** Governance
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `in-review`

## Narrative

Slice 329's compliance meta-audit
(`docs/audits/329-compliance-meta-audit-report.md` finding **H-4**, severity
**High**) surfaced that the project has no documented retention or disposal
policy for the artifacts the project itself produces and stores. SOC 2
C1.2 (Confidentiality — disposal of confidential information), ISO 27001
8.10 (Information deletion), and GDPR Art 5(1)(e) (storage limitation) all
expect a documented retention policy with disposal procedures.

Scattered references exist:
`deploy/docker/test-self-host-bundle.sh:160` (backup suffix deletion),
`migrations/sql/20260519000000_audit_periods_vendors_export.down.sql:11`
(audit-meta retention policy comment),
`migrations/sql/20260521010000_tenants_rename.sql:195` (tenant removal
retention-semantics deferral). All are inline comments; no consolidated
policy exists.

**Important scope distinction.** The platform-as-product retention semantics
(operator-side; operator decides their own retention) are out of scope for
this slice. THIS slice scopes the **project's** own retention policy — the
artifacts the maintainer / project keeps about itself:

- GitHub Actions logs (default 90 days; the policy documents whether the
  project lengthens or accepts the default)
- ghcr.io container images per tag (release tags forever; latest rolling)
- Slice docs / decisions logs / audit reports under `docs/`
- CHANGELOG.md entries
- MEMORY.md persistent state (kept under `/Users/gmoney/.claude/`)
- Issue tracker artifacts (GitHub Issues / PRs — kept indefinitely)
- Release tags + signed artifacts

**What ships.** A new governance document at
`docs/governance/data-retention-policy.md` covering:

1. **Data classes** — categorize the artifacts above into ~5-8 classes
   (operational logs, build artifacts, governance docs, audit-trail
   artifacts, release artifacts, code/PR history, vendor data).
2. **Retention period per class** — honest periods. Code/PR history:
   indefinite. CI logs: GitHub default (90 days; documented). Container
   images for released versions: indefinite. Container images for `main`:
   30 days rolling. Governance docs: indefinite. Audit-trail artifacts:
   indefinite (they ARE the audit trail).
3. **Disposal method per class** — how artifacts are removed at end of
   retention. CI logs: automatic via GitHub. Container images: explicit
   prune via `.github/workflows/edge-image-prune.yml` (already exists for
   `main` rolling tag). Governance docs: never disposed; superseded
   versions referenced in CHANGELOG.
4. **Legal hold provisions** — under what circumstances retention is
   extended (incident-response forensic preservation, legal request).
5. **Cross-references** to `.github/workflows/edge-image-prune.yml`,
   `migrations/sql/20260519000000_audit_periods_vendors_export.down.sql`,
   `docs/SELF_HOSTING.md` (operator-side guidance).
6. **GDPR Art 5(1)(e) note** — this policy closes the storage-limitation
   gap **for the project itself**. Operator-side GDPR storage-limitation is
   the operator's responsibility per the platform's tenancy model.

**No code modified.** Pure documentation slice. The
`edge-image-prune.yml` workflow exists; this slice references it without
modifying it.

## Threat model

Document-only slice. STRIDE pass:

- **S/T/R:** No new auth surface.
- **I:** Retention policy tells an attacker "audit logs persist
  indefinitely; CI logs at 90 days." **Mitigation:** retention periods are
  policy-level, not exploit-roadmap. The publicly-known GitHub Actions
  default (90 days) and the slice 037 `edge-image-prune.yml` policy are
  already public; this slice consolidates rather than reveals.
- **D/E:** N/A.

## Acceptance criteria

- [ ] **AC-1.** `docs/governance/data-retention-policy.md` exists with the
      six sections above.
- [ ] **AC-2.** Data classes enumerated with honest retention periods.
- [ ] **AC-3.** Disposal method per class documented.
- [ ] **AC-4.** Legal hold provisions documented.
- [ ] **AC-5.** Cross-references to existing assets (edge-image-prune.yml,
      migrations comments).
- [ ] **AC-6.** GDPR Art 5(1)(e) closure noted explicitly.
- [ ] **AC-7.** CHANGELOG.md Unreleased `### Documentation` bullet records
      the new policy.
- [ ] **AC-8.** No code modified — diff = governance doc files only.
- [ ] **AC-9.** `pre-commit run --files <touched paths>` passes.

## Constitutional invariants honored

- **Survive third-party security review (canvas §6).** A reviewer asking
  "what's your retention policy?" gets a documented answer.
- **OQ #10 deferral honored.** This slice does NOT touch GDPR Art 33
  breach-notification scope, which OQ #10 defers to phase 3.

## Canvas references

- `Plans/canvas/01-vision.md §6` — survive third-party review
- `Plans/canvas/11-open-questions.md #10` — breach notification deferred

## Dependencies

- **#329** (compliance meta-audit) — `merged` at this slice's spawn time.

## Anti-criteria (P0 — block merge)

- **P0-375-1.** Does NOT modify platform-side retention semantics (operator
  decides their own; out of scope for this slice).
- **P0-375-2.** Does NOT promise retention periods the project cannot
  honor (e.g., "we delete CI logs after 30 days" — false; GitHub default
  is 90 and the project doesn't override it).
- **P0-375-3.** Does NOT modify code or workflows.
- **P0-375-4.** Does NOT auto-merge.
- **P0-375-5.** Does NOT touch GDPR Art 33 / breach-notification scope.

## Notes for the implementing agent

**Tone discipline.** Honest periods, not aspirational. If the GitHub default
is the policy, say so explicitly rather than inflating.

**Length target.** ~100-200 lines.

**Decision log.** File
`docs/audit-log/375-data-retention-disposal-policy-decisions.md` recording
the data-class taxonomy, the retention-period calibration choices, and
explicit confirmation that platform-side retention is out of scope.
