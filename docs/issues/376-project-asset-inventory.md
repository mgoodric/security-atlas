# 376 — Project asset inventory (governance document)

**Cluster:** Governance
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 329's compliance meta-audit
(`docs/audits/329-compliance-meta-audit-report.md` finding **H-5**, severity
**High**) surfaced that the project has no asset inventory document. SOC 2
CC3.2 (Risk assessment — identification of assets), ISO 27001 5.9
(Inventory of information and other associated assets), and NIST CSF ID.AM
(Asset Management) all expect a documented inventory of the assets in the
operator's control. The first questionnaire question — "list all assets in
scope" — has no documented answer today.

**Scope.** Project-side assets only (operator-side; the things the project
itself owns / maintains / depends on). Customer-side asset management (what
operators inventory inside their own deployments) is the
`scopes`/`asset_class` half of the platform's data model — out of scope for
this slice.

**What ships.** A new governance document at
`docs/governance/asset-inventory.md` covering:

1. **Code assets** — the repo itself (one row), monorepo language surfaces
   (Go binaries `cmd/atlas` + `cmd/atlas-cli` + `cmd/atlas-openapi` +
   `cmd/scripts/*`; Python `oscal-bridge`; TypeScript `web/` + `sdk/`).
2. **Infrastructure assets** — GitHub repo + org, ghcr.io image registry
   namespace, GitHub Pages docs site, GitHub Actions runners.
3. **Third-party services** — Codecov (coverage), GitGuardian (secret
   scanning), Anthropic / OpenAI / Bedrock (AI inference; opt-in per
   tenant), dependabot, codeQL, StepSecurity Harden-Runner.
4. **Secrets** — names only, NOT values. The `secrets.*` references from
   `.github/workflows/*.yml` (BRANCH_PROTECTION_READ_TOKEN, codecov
   token if any, ghcr.io credentials, GitHub-Actions-default GITHUB_TOKEN).
5. **Owners + criticality** — for each asset, owner (= maintainer at sole-
   maintainer stage; advisory-council successor designated when council
   trigger fires per GOVERNANCE.md) + criticality (Critical / Important /
   Nice-to-have).
6. **Classification scheme** — Public / Internal / Confidential / Secret.
   The repo is Public. Branch-protection PAT is Secret. CI logs are
   Internal. ghcr.io images are Public.
7. **Cross-references** to ADR-0005 (PAT scope), `.github/dependabot.yml`,
   `GOVERNANCE.md`.

**Format.** Single Markdown table per category, designed for a third-party
auditor to read top-to-bottom in <5 minutes.

**No code modified.** Pure documentation slice.

## Threat model

Document-only slice. STRIDE pass:

- **S/T/R:** No new auth surface; document edits only.
- **I:** **Load-bearing.** An asset inventory IS a target list for an
  attacker. **Mitigation strategy:**
  - List asset **types and owners**, not asset **secrets or credentials**
  - Reference workflow files where secrets are configured WITHOUT exposing
    the workflow-internal usage path
  - Reference existing public information (GitHub org, ghcr.io path) and
    do NOT add new information that an attacker doesn't already have
  - Mark Secret-classification assets with the classification label but
    keep their existence-vs-name boundary tight (e.g., "branch-protection
    PAT — Secret — rotated quarterly" not "branch-protection PAT lives in
    repo secret `BRANCH_PROTECTION_READ_TOKEN`" — though that name is
    already public in workflow YAML, so this specific name is acceptable).
- **D/E:** N/A.

The slice 329 audit-report H-5 description explicitly avoided enumerating
the assets in the audit report to defer enumeration to this slice's threat-
modeled execution.

## Acceptance criteria

- [ ] **AC-1.** `docs/governance/asset-inventory.md` exists with the seven
      sections above.
- [ ] **AC-2.** Code assets enumerated (repo + per-language surface).
- [ ] **AC-3.** Infrastructure assets enumerated (GitHub org/repo, ghcr.io,
      docs site, runners).
- [ ] **AC-4.** Third-party services enumerated (CodeQL, Codecov, etc.).
- [ ] **AC-5.** Secrets enumerated by name only (not value); cross-
      referenced to workflow YAML.
- [ ] **AC-6.** Owners + criticality assigned per asset.
- [ ] **AC-7.** Classification scheme defined and applied.
- [ ] **AC-8.** Threat-model section explicitly states the inventory-
      hardens-defense vs. inventory-is-target-list trade-off and how it
      was navigated.
- [ ] **AC-9.** CHANGELOG.md Unreleased `### Documentation` bullet records
      the new policy.
- [ ] **AC-10.** No code modified — diff = governance doc files only.
- [ ] **AC-11.** `pre-commit run --files <touched paths>` passes.

## Constitutional invariants honored

- **Survive third-party security review (canvas §6).** A reviewer asking
  "list all in-scope assets" gets a documented inventory.
- **No exploit-roadmap detail (slice 329 D10).** Assets are listed by
  type and owner; values, paths, and exploitation specifics omitted.

## Canvas references

- `Plans/canvas/01-vision.md §6` — survive third-party review

## Dependencies

- **#329** (compliance meta-audit) — `merged` at this slice's spawn time.
- **#373** (BCP/DR plan) — sibling; the asset-criticality grading here
  should align with the BCP RTO/RPO targets there. Either order works;
  whichever lands first seeds the other.

## Anti-criteria (P0 — block merge)

- **P0-376-1.** Does NOT enumerate secret VALUES (names are public via
  workflow YAML and acceptable; values never).
- **P0-376-2.** Does NOT enumerate the maintainer's personal infrastructure
  (workstation hardware, password-manager choice, hardware-key model,
  network architecture) — out of scope for the project asset inventory.
- **P0-376-3.** Does NOT modify code.
- **P0-376-4.** Does NOT auto-merge.
- **P0-376-5.** Does NOT touch CLAUDE.md or canvas.

## Notes for the implementing agent

**Threat-model first.** Before listing any asset, decide whether listing it
adds risk (it's a new disclosure) vs reduces risk (it's already known and
the inventory hardens defense by surfacing owners). Apply the slice 329
D10 boundary — names + types + owners + criticality; not values + paths +
exploitation specifics.

**Format discipline.** One table per category. Auditor reads top-to-bottom
in <5 minutes. No long prose between tables.

**Length target.** ~150-250 lines.

**Decision log.** File
`docs/audit-log/376-project-asset-inventory-decisions.md` recording the
inventory-vs-disclosure threat-model decisions per category, the
classification-scheme calibration, and any specific assets explicitly
omitted with rationale.
