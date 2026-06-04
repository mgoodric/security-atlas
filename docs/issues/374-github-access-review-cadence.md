# 374 — GitHub org access review cadence (governance document)

**Cluster:** Governance
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 329's compliance meta-audit
(`docs/audits/329-compliance-meta-audit-report.md` finding **H-3**, severity
**High**) surfaced that the project has no documented access-review
cadence. SOC 2 CC6.2 + CC6.3 + CC6.5, ISO 27001 5.18 + 8.2, and NIST CSF
PR.AC-1 all expect a documented periodic review of who has access to
production systems and code-repository administration. The first
diligence-call question — "do you review access at least annually?" — has
no documented answer today.

The platform's technical access controls are excellent (OIDC RP, OAuth AS,
JWT current_tenant claim, DB-layer RLS). The gap is **at the GitHub
organization layer**: who has push access to the repo, who has access to
CI secrets, who has access to ghcr.io publishing, who has access to
third-party integrations (Codecov, GitGuardian, branch-protection PAT).
No documented cadence reviews-and-revokes these.

**What ships.** A new governance document at
`docs/governance/access-review-cadence.md` covering:

1. **Review scope** — every access surface the project depends on:
   - GitHub repo collaborators (read / triage / write / maintain / admin)
   - GitHub organization owners / members
   - CI secrets (every `secrets.*` reference in `.github/workflows/`)
   - ghcr.io image registry push tokens
   - Codecov upload token
   - GitGuardian integration
   - Branch-protection PAT (per ADR-0005)
   - Maintainer-side GPG signing keys (used for DCO sign-off + release tags)
2. **Review cadence** — quarterly for high-criticality (CI secrets, PAT,
   GPG keys); annual for low-criticality (repo collaborators read-only).
3. **Review checklist** — a copy-paste checklist for each scheduled review:
   list every access grant, verify the grant is still needed, document
   any revocations, document the next review date.
4. **First scheduled review date** — committed in the document (e.g.,
   2026-08-28 for the first quarterly review; subsequent dates derived).
5. **Review evidence pattern** — each completed review lands as
   `docs/audit-log/access-review-YYYY-Q[1-4].md` with the checklist filled
   in. Empty reviews (nothing revoked) still file the artifact.
6. **Cross-references** to ADR-0005 (PAT scope), GOVERNANCE.md (advisory
   council trigger), `.github/branch-protection.json` (the ruleset itself).

**No code modified.** Pure documentation slice.

## Threat model

Document-only slice. STRIDE pass:

- **S/T/R:** No new auth surface; document edits only.
- **I:** Cadence document tells an attacker "here's what we audit and
  when." **Mitigation:** the cadence is procedural; the document does
  NOT list current grants (those go in the per-review evidence artifacts,
  which are themselves audit-log entries; current vs. previous grants are
  not exposed verbatim).
- **D/E:** N/A.

## Acceptance criteria

- [ ] **AC-1.** `docs/governance/access-review-cadence.md` exists with the
      six sections above.
- [ ] **AC-2.** Review scope enumerates every access surface (repo, org,
      secrets, registry tokens, third-party integrations, PAT, GPG).
- [ ] **AC-3.** Cadence assigns quarterly / annual frequency per
      criticality tier.
- [ ] **AC-4.** Checklist is copy-pasteable; ready for the first review.
- [ ] **AC-5.** First scheduled review date committed in the document.
- [ ] **AC-6.** Review-evidence template at
      `docs/audit-log/access-review-template.md`.
- [ ] **AC-7.** CHANGELOG.md Unreleased `### Documentation` bullet records
      the new policy.
- [ ] **AC-8.** No code modified — diff = governance doc files only.
- [ ] **AC-9.** `pre-commit run --files <touched paths>` passes.

## Constitutional invariants honored

- **Survive third-party security review (canvas §6).** A reviewer asking
  "do you review access annually?" gets a documented quarterly cadence.

## Canvas references

- `Plans/canvas/01-vision.md §6` — survive third-party review

## Dependencies

- **#329** (compliance meta-audit) — `merged` at this slice's spawn time.

## Anti-criteria (P0 — block merge)

- **P0-374-1.** Does NOT enumerate current access grants in the public
  cadence document — those live in the per-review evidence artifacts only.
- **P0-374-2.** Does NOT name specific GitHub usernames except the
  maintainer's own (who is publicly identified anyway).
- **P0-374-3.** Does NOT modify code.
- **P0-374-4.** Does NOT auto-merge.
- **P0-374-5.** Does NOT commit to a cadence the maintainer cannot
  unilaterally sustain (quarterly is realistic; weekly would be
  aspirational and self-defeating).

## Notes for the implementing agent

**Tone discipline.** Procedural and dry. This document is a checklist
template plus a schedule; it does not need narrative. The first review
itself (filing the first `access-review-YYYY-Q[1-4].md`) is out of scope
for this slice — the cadence document lands first; the first review files
separately at the committed date.

**Length target.** ~100-200 lines.

**Decision log.** File
`docs/audit-log/374-github-access-review-cadence-decisions.md` recording
the quarterly-vs-annual tier assignment, the first-review-date selection,
and the public-vs-private cadence-vs-evidence boundary.
