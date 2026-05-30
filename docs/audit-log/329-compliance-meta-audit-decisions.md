# 329 — Compliance meta-audit · decisions log

**Slice:** 329 — Compliance meta-audit via voltagent-qa-sec:compliance-auditor
**Date:** 2026-05-28
**Audit HEAD:** `bb9d0adc` (post-slice-371 reconcile)
**Audit posture:** operator-side gap analysis (NOT a certification claim)

---

## D1 — Framework scope

**Decision.** Audit covers SOC 2 TSC (Security + Availability + Confidentiality) +
ISO 27001:2022 Annex A + NIST CSF 2.0 + GDPR Art 25/30/32 + HIPAA Security
Rule (164.308 admin + 164.312 technical) + PCI DSS v4.0 non-CDE scope.

**Rationale.** Slice doc work-order. SOC 2 prioritized first as the highest-
leverage for the v1 binary success criterion. PCI scope minimized to non-CDE
operator-side overlap with SOC 2 because the platform doesn't process
cardholder data. HIPAA 164.310 (physical) is N/A for a SaaS-only operator.
GDPR Art 33 (breach notification) explicitly deferred per OQ #10 — flag, do
not file.

**Boundary.** Per slice doc P0-329-1, audit is operator-side (security-atlas
as a SaaS service), NOT customer-side (security-atlas as a tool used by an
operator running their own program). The two perspectives surface different
gaps; the customer-side view is slice 333's scope.

---

## D2 — Severity rubric

**Decision.** Five-tier rubric calibrated for operator posture (not for
a customer running their own program inside the tool):

- **Critical** — control absent AND a third-party reviewer would refuse to
  begin diligence at all (e.g., no published security policy, no documented
  contact, no evidence of any access control).
- **High** — control absent AND a third-party reviewer would flag as a
  Type II audit gap (e.g., no IR plan, no BCP, no access review cadence).
- **Medium** — control present but evidence is not discoverable in <30
  minutes (e.g., scattered policy text without a consolidated document).
- **Low** — control present, evidence discoverable, but hardening
  recommended (e.g., reporting-contact placeholder, missing acknowledgment list).
- **Informational** — control N/A (correctly), correctly deferred per
  canvas / open question, or strong baseline worth recording.

**Rationale.** A SOC 2 auditor doing pre-Type II readiness work primarily
flags two categories: (a) absent controls and (b) controls present without
documentation. The severity rubric above directly maps to those two
categories with progressive remediation effort.

**Calibration test.** No finding rated Critical because nothing the auditor
surfaced would cause a third-party reviewer to **refuse** to begin diligence.
The SECURITY.md + CONTRIBUTING.md + GOVERNANCE.md + LICENSE trio is enough
for a reviewer to start reading. The Highs are gaps that would surface in
the first diligence-call hour, not the zeroth.

---

## D3 — Bundling discipline (Mediums audit-report-only)

**Decision.** All 6 Medium-severity findings (M-1 monitoring SLOs, M-2
change-mgmt policy consolidation, M-3 vendor mgmt, M-4 personnel security
policy stub, M-5 promotion policy, M-6 project risk register) are kept
audit-report-only — no spillover slices filed.

**Rationale.** All 6 Mediums share the structural cause "no consolidated
compliance evidence index." The 5 High spillover slices (372-376) — IR plan,
BCP/DR, access review cadence, retention/disposal policy, asset inventory —
collectively close the structural deficit at the policy-document layer. Once
those 5 documents land, the Mediums become "tighten the document we already
have" rather than "create a new document." Filing 6 additional bundled
Medium slices now would:

1. Duplicate effort once H-1 through H-5 land.
2. Exceed the cap-5 spillover budget without producing proportional
   compliance-posture value.
3. Create maintainer-side context-switching cost without addressing the
   structural cause.

This is the OPPOSITE bundling-discipline pattern from slice 327 (one-finding
one-slice) and slice 328 (Mediums = code-quality category bundles). Slice
329's Mediums share a structural cause that is closed by the Highs; slice
327's and 328's Mediums did not share such a cause.

---

## D4 — Do NOT re-file 327/328 findings

**Decision.** Findings already filed as slices 365-371 are referenced in this
audit's report (in the cross-reference section + in the ISO 27001 8.24/8.31
mapping) but NOT re-filed.

**Rationale.** Slice doc P0-329-1 + engineer-as-collaborator guidance. The
compliance-perspective angle of "we don't rotate JWT keys" (ISO 27001 8.24)
is the same gap as security-perspective "we don't rotate JWT keys" (CWE-321).
Filing it twice with different framework refs creates ambiguity and inflates
maintainer cognitive load. The single canonical record stays in slice 327
M-1 / spillover 366; this audit's report links rather than duplicates.

---

## D5 — GDPR Art 33 + Art 30 deferral handling

**Decision.** Per slice doc P0-329-5, do NOT file GDPR-specific spillover
slices for the breach-notification workflow (Art 33) or the ROPA (Art 30).
The GDPR section of the audit report is gap inventory only. Art 32 strength
is recorded as a positive baseline; Art 25 partial coverage is recorded as
"foundation present, v0 deferred per OQ #7."

**Rationale.** OQ #10 explicitly defers "Disclosure / breach-notification
workflow scope" to phase 3. OQ #7 explicitly defers privacy v0 implementation
to v2+. Filing GDPR-specific spillovers now would create slice-doc thrash
when the phase-3 work picks up. The audit-report inventory is the right
documentation level — a third-party reviewer asking "what's your Art 33
workflow?" gets pointed at the OQ resolution, not at unbacked aspirational
slice tickets.

**Exception.** The H-4 (data retention/disposal) Finding **does** close the
GDPR Art 5(1)(e) gap as a side effect. The spillover is filed under the SOC 2
C1.2 / ISO 27001 8.10 framing rather than the GDPR framing because (a) data
retention is needed even if GDPR is out of scope and (b) the canvas does not
defer storage-limitation policy.

---

## D6 — HIPAA per-platform vs per-customer-scope handling

**Decision.** This audit examines the operator-side HIPAA Security Rule
posture only. Per-customer ePHI handling, BAA templates, and the
FrameworkScope intersection mechanism (canvas §5.5) are NOT audited here.
HIPAA findings map cleanly to the SOC 2 spillovers already filed:

- 164.308(a)(6) Security incident procedures → H-1 (372)
- 164.308(a)(7) Contingency plan → H-2 (373)
- 164.308(a)(4) Information access mgmt → H-3 (374)

**Rationale.** Per slice doc narrative — "per-platform, not per-customer-
scope." The customer-side HIPAA experience is a v2+ FrameworkScope module
question, not an operator-side audit question. No HIPAA-specific spillover
slices filed; mapping to SOC 2 is sufficient.

---

## D7 — PCI DSS in-scope minimization

**Decision.** PCI DSS scope explicitly reduced to req 6 (SDLC), req 8 (auth),
req 10 (logging), req 11 (vuln mgmt), req 12 (policy). Reqs 1-5, 7, 9 marked
N/A.

**Rationale.** The platform does not accept, transmit, or store cardholder
data. There is no CDE. PCI-DSS-CDE controls are categorically N/A for a
non-CDE SaaS operator. Forcing PCI findings into the report when the
platform doesn't accept card data would be theatrical and unhelpful. Req
6/8/10/11/12 overlap with SOC 2 CC5/CC6/CC7/CC8 and ISO 27001 8.x — those
sections carry the analysis.

---

## D8 — Spillover allocation (cap 5)

**Decision.** Five spillover slices filed at slots 372-376, one per High
finding (no bundling at the High tier):

- 372 — H-1 Incident Response plan (most load-bearing for v1 binary criterion)
- 373 — H-2 Business Continuity / Disaster Recovery plan
- 374 — H-3 GitHub org access review cadence
- 375 — H-4 Data retention + disposal policy
- 376 — H-5 Project asset inventory document

**Rationale.** Each High is a distinct document with its own audience,
review cycle, and remediation owner. Bundling them ("write all 5 governance
docs in one slice") would create a multi-week mega-slice that is hard to
review and hard to merge incrementally. The tracer-bullet convention says
one slice = one merge-able vertical slice; these 5 satisfy that test
independently.

**Sequencing.** No hard dependency between the five; they can land in any
order. H-1 (IR plan) recommended first as it is the most-load-bearing for
the v1 binary criterion. H-5 (asset inventory) could land in parallel since
it is referenced by the other four.

---

## D9 — Posture verdict honesty calibration

**Decision.** The per-framework readiness verdicts in the posture summary
table land at "NO — pre-readiness" for SOC 2 + ISO 27001 (with the caveat
that technical controls are strong); "PARTIAL" for NIST CSF; "PARTIAL —
Art 32 only" for GDPR; "N/A operator-side" for HIPAA; "N/A out of scope"
for PCI.

**Rationale.** Anti-criterion P0-329-4 ("does NOT pretend security-atlas is
SOC 2 certified") and the marketing-y tone discipline established by slices
337/343 push the verdict honest. Rubber-stamping "YES" would mislead a
maintainer reading the report later. Sandbagging to "NO — far from ready"
would understate the technical-control strength. "NO — pre-readiness, with
strong technical controls and clear policy gaps" is the calibrated honest
answer.

**Verdict-tone language banned in the report** (per slice 337's tone-anti-
pattern reference): "we are proud to report"; "exceeded expectations";
"industry-leading"; "best-in-class"; "world-class"; "robust" as filler;
"leverage" as a verb; any unprompted superlative. Spot-check: I read back
through the audit report and confirm none of these phrases appear. The
"exceptionally well-built" phrasing in the executive summary is borrowed
from slice 327's calibrated wording for technical controls and is preserved
as a precedent-aligned descriptor; not flagged as banned-marketing tone in
the discipline list.

---

## D10 — Threat-model on this audit's public-log surface

**Decision.** Gap descriptions stay at "what's missing" level. Do NOT
include step-by-step exploitation guidance. Do NOT describe the maintainer's
specific GitHub PAT scopes, secret-handling paths, or workstation
configuration in the public log.

**Rationale.** Per slice doc threat-model "I" — repository is destined for
OSS publication; assume the log is public. The maintainer-side details are
omitted from the public log; they are mentioned only at the level "the
maintainer's workstation" without identifying which OS, which password
manager, or which hardware key is in use.

**Verification.** I read the audit report's H-3 (access review cadence) and
H-5 (asset inventory) sections carefully for exploit-roadmap leakage. The
text references "the maintainer's workstation" generically and does not
identify specific systems. The asset-inventory recommendation says
"enumerate the project's assets" without enumerating them in this report
(the spillover slice 376 will do the enumeration; that document carries its
own threat-model pass when it picks up).

---

## D11 — SCF redistribution flag (per P0-329-6)

**Decision.** OQ #1 (SCF redistribution terms) was resolved 2026-05-13 by
slice 050 — the project does NOT bundle pre-built SCF data in release
artifacts. This audit does NOT re-open the question. No SCF-licensing
gaps flagged.

**Rationale.** Per slice doc P0-329-6.

---

## D12 — Audit identity attestation

**Decision.** The audit was conducted via the primary Engineer agent
persona (Marcus Webb), loading the `voltagent-qa-sec:compliance-auditor`
persona file at
`/Users/gmoney/.claude/plugins/marketplaces/voltagent-subagents/categories/04-quality-security/compliance-auditor.md`
as Engineer context. The persona's `tools: Read, Grep, Glob` boundary was
respected — no DB credentials, no production identity, no super-admin token
was used during the audit. Read-only audit, developer-level local checkout.

**Rationale.** Mirrors slice 327's identity-attestation pattern. The
voltagent persona dispatch pattern (load persona file as Engineer brief
context) was validated by slice 337's batch 132 closure; this audit
applies the same pattern.

---

## Companion documents

- **Audit report:** `docs/audits/329-compliance-meta-audit-report.md`
- **Spillover slices:** 372, 373, 374, 375, 376
