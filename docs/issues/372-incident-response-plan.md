# 372 — Incident response plan (governance document)

**Cluster:** Governance
**Estimate:** 1d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 329's compliance meta-audit
(`docs/audits/329-compliance-meta-audit-report.md` finding **H-1**, severity
**High**) surfaced the load-bearing compliance gap for the v1 binary success
criterion: the project has no documented Incident Response plan. SECURITY.md
documents the **inbound** coordinated-disclosure process (5 business days
ack, 10 business days assessment, 30 days fix target for high/critical) —
that is a coordinated-disclosure policy, not an IR plan. A third-party
reviewer asking "what's your IR plan?" on a v1 sales-cycle diligence call
cannot be pointed at SECURITY.md alone.

**This is the most load-bearing finding for the v1 binary criterion** per
the slice 329 audit report. Without it closed, the project answers "we
don't have one" in a sales-cycle moment that costs the deal.

**What ships.** A new governance document at
`docs/governance/incident-response.md` covering:

1. **Severity rubric** — P0 (active exploitation in the wild), P1 (confirmed
   vulnerability, not yet exploited), P2 (suspected vulnerability), P3
   (security-relevant operational issue, e.g., GitHub Actions runner
   compromise advisory).
2. **Sole-maintainer role-stacking** — explicit acknowledgment that the
   maintainer is incident commander + comms lead + technical lead at this
   stage of the project. References GOVERNANCE.md's bus-factor / succession
   plan for what happens if the maintainer is unavailable.
3. **Containment / eradication / recovery procedures** per severity —
   P0/P1 timelines aligned with SECURITY.md's 30-day high/critical commitment;
   P2/P3 timelines aligned with the standard release cadence.
4. **Communications playbook** — when to file a GitHub Security Advisory,
   when to publish a CHANGELOG security entry, when to email reporters,
   when to coordinate with downstream operators via mailing list / Discord.
5. **Post-incident review template** — a markdown template that lands as
   `docs/audit-log/incident-NNN-<slug>.md` after the fact, capturing
   timeline, root cause, action items, and the slice spillovers filed.
6. **Cross-references** to SECURITY.md (vuln intake), GOVERNANCE.md
   (succession), CHANGELOG.md (security entry pattern).

**No code modified.** This is a pure documentation slice. The diff is the
new `docs/governance/incident-response.md` file + a one-line cross-reference
from SECURITY.md ("see also: incident response plan") + a CHANGELOG bullet.

## Threat model

Document-only slice. STRIDE pass:

- **S/T/R:** No new auth surface; document edits only.
- **I:** The IR plan IS sensitive — a published playbook tells an attacker
  the maintainer's response timing. **Mitigation:** stay at the procedural
  level (severity rubric, escalation paths, comms triggers) without naming
  specific automation, alerting tools, or maintainer-specific
  infrastructure. The plan describes WHAT happens, not WHICH systems
  participate.
- **D/E:** Document-only; not applicable.

## Acceptance criteria

- [ ] **AC-1.** `docs/governance/incident-response.md` exists with the six
      sections above.
- [ ] **AC-2.** Severity rubric (P0/P1/P2/P3) defined with timing
      commitments that align with SECURITY.md's existing 30-day high/critical
      target.
- [ ] **AC-3.** Sole-maintainer role-stacking documented; references
      GOVERNANCE.md succession plan.
- [ ] **AC-4.** Post-incident review template at
      `docs/audit-log/incident-NNN-template.md` — empty template only;
      filled-in versions land per-incident as they happen.
- [ ] **AC-5.** SECURITY.md cross-references the new IR plan (one line in
      the existing "scope" section or a new "see also" section).
- [ ] **AC-6.** CHANGELOG.md Unreleased `### Documentation` bullet records
      the new policy.
- [ ] **AC-7.** No code modified — diff = governance doc files only.
- [ ] **AC-8.** `pre-commit run --files <touched paths>` passes.

## Constitutional invariants honored

- **AI-assist boundary (hard).** IR plan content is human-authored;
  AI-assisted drafts marked `human_approved` per the canvas's AI-assist
  schema-level enforcement.
- **Survive third-party security review (canvas §6).** Direct closure of
  this invariant — IR plan is one of the first three artifacts an auditor
  asks for.
- **Document discipline.** Governance docs live at `docs/governance/`,
  matching the existing `docs/governance/board-narrative-tone-anti-patterns.md`
  precedent.

## Canvas references

- `Plans/canvas/01-vision.md §6` — survive third-party review
- `Plans/canvas/08-audit-workflow.md` — auditor expectations

## Dependencies

- **#329** (compliance meta-audit) — `merged` at this slice's spawn time.
  Source of the finding.

## Anti-criteria (P0 — block merge)

- **P0-372-1.** Does NOT include exploitation-aware detail (e.g., named
  incident-response automation tools, named alerting infrastructure,
  specific monitoring stacks the maintainer relies on).
- **P0-372-2.** Does NOT modify SECURITY.md's existing intake process — it
  cross-references, it does NOT replace.
- **P0-372-3.** Does NOT modify code.
- **P0-372-4.** Does NOT auto-merge.
- **P0-372-5.** Does NOT promise capabilities the maintainer cannot
  unilaterally deliver (e.g., "24/7 on-call rotation" — false for a sole
  maintainer; the doc explicitly says "best-effort during business hours,
  P0 only outside business hours").

## Notes for the implementing agent

**Tone discipline.** This is a security document for an OSS project, not a
SaaS company. The voice is measured and honest — "the maintainer attempts
to ack P0 within 24 hours," not "we are committed to 24/7 incident response."
The slice 337 tone-anti-pattern list applies: no "industry-leading," no
"best-in-class," no marketing-y framing.

**Length target.** ~200-400 lines of Markdown. Long enough to cover the six
sections; short enough that the maintainer can read it under stress during
a real incident.

**Templating.** The post-incident review template should be re-usable —
slug-based filenames `docs/audit-log/incident-NNN-<slug>.md`, frontmatter
with `severity`, `discovered_at`, `resolved_at`, `root_cause`,
`action_items`, body sections for timeline + analysis + lessons.

**Cross-slice coordination.** Sibling slices 373 (BCP/DR), 374 (access
review), 375 (retention), 376 (asset inventory) can land in parallel. This
one is sequenced first per H-1 most-load-bearing finding.

**Decision log.** File `docs/audit-log/372-incident-response-plan-decisions.md`
recording the severity-rubric calibration (why P0/P1/P2/P3 instead of S0/S1
or otherwise), the sole-maintainer role-stacking acknowledgment language,
and the comms-playbook choices.
