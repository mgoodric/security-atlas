# 329 — Compliance meta-audit via voltagent-qa-sec:compliance-auditor

**Cluster:** Compliance
**Estimate:** 2d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Runs `voltagent-qa-sec:compliance-auditor` against security-atlas as if
the platform itself were undergoing a SOC 2 / ISO 27001 / GDPR /
HIPAA / PCI DSS audit. The reference catalog is the Secure Controls
Framework already bundled in the project (per slice 006's importer) —
the same catalog that customers will run their programs against. This
is the "diligence the diligence tool" test: when a security-leader
customer onboards, the first sales-cycle artifact they ask for is
"your SOC 2 report" or "your security questionnaire". This audit
identifies the gaps **before** that conversation happens.

**Audit surface.** Meta-audit across:

- **SOC 2 Trust Services Criteria** (Security + Availability + at-rest
  Confidentiality) — full scope. Reference: slice 010's SCF-anchored
  control kit (50 SOC 2 controls) + slice 007's SOC 2 v2017 crosswalk.
- **ISO 27001:2022** — Annex A controls applicable to a SaaS platform
  operator. Reference: any ISO catalog crosswalk in the SCF bundle.
- **GDPR** — Articles 5, 6, 12-22 (data subject rights), 25 (data
  protection by design), 30 (records of processing), 32 (security of
  processing), 33 (breach notification). Reference: slice 180's
  privacy-module foundation.
- **HIPAA** — Security Rule (164.308 admin safeguards, 164.310 physical
  safeguards [N/A for SaaS-only], 164.312 technical safeguards). Note:
  HIPAA-covered-systems is a separate FrameworkScope per canvas §5.5
  invariant — the meta-audit is per-platform, not per-customer-scope.
- **PCI DSS v4.0** — only applicable controls for a non-CDE SaaS
  (most of req 2-12 except 9 [physical] and the CDE-specific reqs).

**The meta-audit asks per control:**

1. Is the control **applicable** to security-atlas as an operator?
   (PCI req 9 is N/A; most HIPAA admin safeguards apply.)
2. If applicable, is there **evidence** the control is satisfied —
   in the existing codebase, ADRs, ops runbooks, or third-party
   posture (cloud provider IAM, GitHub branch protection, etc)?
3. If evidence exists, is it **discoverable** — could a third-party
   auditor find it in <30 minutes, or is it tribal knowledge?
4. If evidence does not exist, what's the **gap** + the **remediation
   path** (a slice, an ADR, a runbook, a vendor selection)?

**Why now:** the v1 binary success test requires the platform to be
self-audit-passable. If security-atlas can't pass a SOC 2 against its
own catalog, the diligence-the-diligence-tool thesis fails on day one
of the customer conversation.

**Trigger:** Surfaced 2026-05-27 during the agent-driven audit-planning
session — audit slice 3/12.

**Disposition:** read-only meta-audit + follow-up-slice fan-out.

## Threat model

Meta-audit-only slice. STRIDE pass:

- **S (Spoofing):** No new auth surface. CLEAN.
- **T (Tampering):** Read-only — AC enforces no code changes.
- **R (Repudiation):** Findings logged in
  `docs/audit-log/329-compliance-meta-audit-decisions.md` with control
  ID + framework + evidence pointer (or gap).
- **I (Information disclosure):** **Load-bearing.** The decisions log
  documents the platform's compliance posture — gaps + evidence
  locations. This is sensitive: a public log of every gap is a roadmap
  for an attacker AND a problem for the platform's own marketing /
  trust positioning. **Mitigation:** the decisions log SHOULD describe
  gaps at a level that lets the maintainer prioritize follow-ups
  without giving an attacker step-by-step exploitation guidance. The
  follow-up slices each carry their own threat-model pass via
  `/idea-to-slice` Phase 3, which catches exploit-roadmap risks at
  finer granularity. Repository visibility: the repo is destined for
  OSS publication; assume the log is public.
- **D (Denial of service):** Run-once. CLEAN.
- **E (Elevation of privilege):** Reviewer operates dev-level. AC
  enforces.

## Acceptance criteria

- [ ] **AC-1.** The `voltagent-qa-sec:compliance-auditor` agent runs
      against the five framework families (SOC 2 TSC, ISO 27001:2022,
      GDPR, HIPAA Security Rule, PCI DSS v4.0) using the SCF catalog
      bundled per slice 006 as the canonical control set.
- [ ] **AC-2.** Per framework, the decisions log records a
      **coverage table** with columns: control ID · applicable
      (yes / no / partial) · evidence location (file / ADR /
      runbook / third-party) · gap (none / minor / major) ·
      remediation path (slice number filed OR proposed). Stored at
      `docs/audit-log/329-compliance-meta-audit-decisions.md`.
- [ ] **AC-3.** For each **major gap** (a control with no evidence
      AND the audit-pass would fail without remediation), a follow-up
      slice is filed via `/idea-to-slice`. The slice's slot is
      appended to the coverage table.
- [ ] **AC-4.** **Minor gaps** (evidence exists but is not discoverable
      / is tribal-knowledge / needs documentation): bundled into a
      single "compliance-evidence discoverability" slice OR filed
      individually — engineer's call, documented.
- [ ] **AC-5.** Informational findings (control is N/A, control is
      satisfied with no action needed): coverage table only; no
      follow-up slice.
- [ ] **AC-6.** No code modified. Diff = doc files only (this slice +
      \_STATUS.md + decisions log).
- [ ] **AC-7.** The decisions log opens with a **per-framework
      readiness verdict**: "would security-atlas pass a SOC 2 audit
      today" (yes / no / yes-with-N-gaps) for each framework. This
      is the load-bearing output that drives the v1-binary-success
      conversation.
- [ ] **AC-8.** Evidence pointers reference real paths in the repo
      (e.g. `internal/auth/keystore/rotation.go:42` not "we rotate
      keys somewhere"). Tribal-knowledge findings are explicitly
      flagged.
- [ ] **AC-9.** `pre-commit run --files` passes for the three changed
      files at PR-time.

## Constitutional invariants honored

- **SCF is the canonical control catalog (invariant #7).** The audit
  uses SCF anchors as the reference, not framework-specific
  duplicates.
- **OSCAL is the wire format (invariant #8).** Gaps tagged with the
  OSCAL artifact type they'd appear in (SSP / AP / AR / POA&M) for
  future export.
- **Manual evidence is first-class (canvas §4.5).** Tribal-knowledge
  findings are valid manual-evidence candidates — the gap is
  discoverability, not existence.
- **Survive third-party security review (canvas §6).** Direct
  literal application of the invariant.

## Canvas references

- `Plans/canvas/03-ucf.md` — UCF graph (SCF anchors)
- `Plans/canvas/04-evidence-engine.md` §4.5 — manual evidence
- `Plans/canvas/05-scopes.md` §5.5 — FrameworkScope intersection
  (relevant to HIPAA + PCI gap analysis)
- `Plans/canvas/08-audit-workflow.md` — auditor role
- `Plans/canvas/01-vision.md` §6 — survive third-party review

## Dependencies

- **#006** (SCF catalog importer) — `merged`. Provides the
  reference catalog.
- **#007** (SOC 2 v2017 crosswalk) — `merged`. Anchors the SOC 2
  half of the audit.
- **#010** (SOC 2 control kit) — `merged`. 50 SCF-anchored controls
  the audit can reason about as evidence-of-control-design.
- **#180** (privacy module foundation) — `merged`. Provides GDPR
  surface anchor.

## Anti-criteria (P0 — block merge)

- **P0-329-1.** Does NOT operate as if the platform is a customer-side
  GRC user — this is the **operator-side** posture audit. (The
  platform-as-customer view is slice 333's UX flow scope.)
- **P0-329-2.** Does NOT include exploit-roadmap detail in the public
  decisions log. Gap descriptions stay at a "what's missing" level,
  not "here's how to attack the missing thing".
- **P0-329-3.** Does NOT bundle multiple major gaps into one follow-up
  slice. Each major gap is one tracer-bullet slice.
- **P0-329-4.** Does NOT auto-merge.
- **P0-329-5.** Does NOT modify code.
- **P0-329-6.** Does NOT prejudge the SCF licensing review (open
  question #02 in `Plans/canvas/11-open-questions.md`). If the audit
  surfaces an SCF-licensing question, flag it without resolving it.
- **P0-329-7.** Does NOT touch CLAUDE.md or canvas.

## Skill mix

- `voltagent-qa-sec:compliance-auditor` — the named audit agent
- `/idea-to-slice` — for follow-ups
- Standard read/grep — surface enumeration

## Notes for the implementing agent

**Framework prioritization (work-order suggestion):**

1. **SOC 2 first.** Highest-leverage for the v1 user. If
   security-atlas can't pass SOC 2 against its own catalog, the
   binary-success test fails immediately.
2. **GDPR second.** EU operators are a near-term self-host
   demographic; the privacy-module foundation (slice 180) is the
   most-recent infrastructure addition and the audit surface is
   fresh.
3. **ISO 27001 third.** Overlaps SOC 2 heavily via SCF anchors;
   the meta-audit benefits from SOC 2's pass first.
4. **HIPAA fourth.** Per-tenant FrameworkScope drives most of the
   surface; the platform-operator surface is the admin-safeguards
   half.
5. **PCI DSS last.** Smallest applicable surface for a non-CDE SaaS.

**Per-framework readiness verdict format** (load-bearing for AC-7):

```
### SOC 2 Trust Services Criteria readiness

Verdict: YES-WITH-N-GAPS
Gaps: 3 major (filed as slices 380, 381, 382), 7 minor (bundled into
slice 383)
Confidence: high (all 50 controls visited; evidence cross-referenced
against slice 010's bundle)
```

**Bundling discipline (minor gaps).** Unlike slice 327 (one-finding
one-slice) and slice 328 (Medium = category bundles), this audit's
minor gaps share a structural cause — discoverability. A single
"compliance-evidence index" slice that creates a per-framework table
of evidence-pointer-of-record is more useful than 30 individual
"document control CC6.7" slices. Engineer's call per cluster.

**Cross-reference with slice 327 (security-audit).** If the
compliance audit surfaces a control with a security-relevant gap
(e.g. "CC6.7 at-rest encryption — no evidence that backups are
encrypted"), cross-reference slice 327's findings to dedupe. The
compliance perspective and the security perspective often arrive at
the same finding from different vectors.

**Audit log filename:**
`docs/audit-log/329-compliance-meta-audit-decisions.md`
