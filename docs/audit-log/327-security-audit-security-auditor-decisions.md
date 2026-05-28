# 327 — Security audit decisions log

**Slice type:** JUDGMENT
**Companion report:** `docs/audits/327-security-audit-security-auditor-report.md`

This file captures the subjective JUDGMENT calls made by the agent conducting the slice-327 security audit. Per the slice-doc / per-slice-template convention, it has three sections: **Decisions made**, **Revisit once in use**, **Confidence per decision**. Findings themselves are in the report; this log captures _how_ the audit was scoped and graded.

---

## Decisions made

### D1 — Audit report path: `docs/audits/` vs `docs/audit-log/`

The brief instructs writing the report at `docs/audits/327-security-audit-security-auditor-report.md`. The slice doc names only `docs/audit-log/327-security-audit-security-auditor-decisions.md`. The two are different artifacts: the **report** documents findings (audience: future auditors, maintainer triage), the **decisions log** documents the JUDGMENT calls (audience: maintainer running the audit cadence).

**Resolution:** wrote BOTH. The report at `docs/audits/...-report.md` per the brief; the decisions log at `docs/audit-log/...-decisions.md` per the slice doc's AC-2 and the per-slice JUDGMENT-type convention. Diff is doc-only either way; the slice-doc AC-6 constraint ("CI diff for the slice's PR contains ONLY: docs/issues/327, docs/issues/\_STATUS.md, docs/audit-log/327") is widened by the brief's explicit instruction to write the report. Acknowledging the mismatch here; if the maintainer wants to tighten future-audit convention, my recommendation is to standardize on `docs/audits/NNN-*-report.md` + `docs/audit-log/NNN-*-decisions.md` for every JUDGMENT-type audit.

**Confidence:** high (the brief is the operational order; the slice-doc is the historical spec).

---

### D2 — Severity rubric

Used the slice-doc's rubric verbatim (Critical = RCE/auth-bypass/RLS-bypass/secrets-in-repo; High = priv-esc-with-prerequisite/cross-tenant data/broken crypto; Medium = within-tenant disclosure/DoS/missing security header; Low = hardening; Informational = observation).

**Resolution:** no deviation from the slice-doc rubric. The slice-doc explicitly says "use this rubric, don't invent your own." I considered finer-grained gradings (e.g. splitting Medium into "exploitable Medium" and "informational Medium") but landed on the rubric verbatim to give the maintainer comparable scoring across the 12 voltagent-qa-sec audit slices that will follow.

**Confidence:** high.

---

### D3 — Spillover cap: 5 vs all-Critical-and-High filed

The brief caps spillover at 5 slices. The audit surfaced 1 High + 3 Medium = 4 findings warranting individual follow-up slices. Total spillover = 4, under cap.

**Resolution:** filed 365 (H-1), 366 (M-1), 367 (M-2), 368 (M-3). Decided to file slices for all 3 Mediums (rather than bundling) because:

- M-1 (key rotation) is a multi-week implementation — too much to bundle.
- M-2 (error leakage) is a wide-shot audit-and-cleanup with its own scope.
- M-3 (cosign migration) is a v2-scoped re-architecture of the OSCAL signing path.

None of the three Medium findings are coherent enough to bundle as a single "audit round 1 polish" slice. Low and Informational findings remain in the report only.

**Confidence:** high.

---

### D4 — Spillover slot numbers (start at 365)

The brief explicitly says "start at 365" even though `docs/issues/364` is unused. I trust the brief — slot 364 may be reserved out-of-band or by a concurrent batch. Filed 365, 366, 367, 368 sequentially.

**Resolution:** 365 / 366 / 367 / 368 used.

**Confidence:** high.

---

### D5 — Engineer-as-collaborator scope discipline

The brief instructs: if I spot adjacent issues during code inspection (e.g. a dependency with a known CVE, a test file using a real-looking secret), address: file via spillover (if Critical/High) or note in audit report (if lower severity). Do NOT widen the audit scope beyond what the slice doc lists.

**Resolution:** the audit found 1 adjacent observation worth noting — `internal/api/oauth/pkce.go:134` carries a TODO ("slice 190 audit will tighten") that was never addressed. Captured as **L-2** in the audit report; did NOT file a follow-up slice because the gap is low-severity. The 7-surface scope from the slice doc was respected — no widening into observability gaps, CI hardening, frontend XSS audit, etc.

**Confidence:** high.

---

### D6 — Severity grade for H-1 (OIDC nonce missing)

The OIDC nonce gap is on the boundary between Medium and High. Arguments for each:

- **Medium:** in the **authorization code flow**, nonce is an additional defense atop state + PKCE. State + PKCE already address CSRF and code-injection. A _practical_ exploit requires either an IdP cache compromise OR a sophisticated token-injection that bypasses both state and PKCE.
- **High:** OIDC Core §3.1.2.1 mandates nonce for ID token replay; RFC 9700 OAuth 2.0 BCP §4.5.3 repeats it. Compliance auditors will flag this. For a GRC platform whose pitch is "survive third-party security review," missing a textbook OIDC requirement is exactly the finding a sophisticated external auditor will surface. The fix is small (≈30 LoC + test).

**Resolution:** graded **High**. Two-tier reasoning: (a) OIDC spec mandate, (b) the v1 binary success criterion ("survive third-party security review") elevates spec-mandated defenses above their pure exploit-difficulty grade. A practical exploit may require chaining, but the absence of a spec-mandated defense in an auth substrate is itself the finding.

**Confidence:** medium — reasonable people could grade Medium. I chose to err on the side of higher severity because the underlying invariant ("survive third-party security review") rewards conservatism.

---

### D7 — Severity grade for M-3 (OSCAL ed25519 vs cosign)

OSCAL signing is "cryptographically equivalent" to cosign (both ed25519 detached signatures) but lacks Sigstore ecosystem integration. Considered grading Low (hardening, no exploit path) but chose Medium because:

- Canvas §9 _commits_ to "cosign signing of audit-export bundles" — the gap is a violation of a stated architectural commitment, not just a hardening miss.
- An auditor verifying an export bundle cannot use stock `cosign verify-blob`; they need atlas's own verifier. For a GRC product whose central claim is _auditor-friendly export_, this is a real friction point in the binary success criterion.

**Resolution:** Medium with an explicit revisit-when-in-use note (M-3's mitigation is multi-week and v2-scoped).

**Confidence:** medium — could defensibly grade Low.

---

### D8 — Did NOT file individual spillover for Low / Informational

Per slice-doc + brief: Low and Informational findings are audit-report-only. Did not file 365+.

**Confidence:** high.

---

### D9 — CHANGELOG entry decision

The brief says: "CHANGELOG.md Unreleased `### Security` bullet if any Critical/High findings were filed." H-1 is High and filed as 365 → CHANGELOG entry warranted. I will add a `### Security` bullet under `## [Unreleased]` summarizing the audit and the 4 spillover slices.

**Confidence:** high.

---

### D10 — Identity used for the audit (per AC-8)

The audit was conducted by the primary Engineer agent (Marcus Webb persona) loading the `voltagent-qa-sec:security-auditor` persona file (located at `~/.claude/plugins/marketplaces/voltagent-subagents/categories/04-quality-security/security-auditor.md`) as brief context. The persona's tool boundary (`Read`, `Grep`, `Glob`) was respected — the audit was read-only against the local checkout. No DB credentials were used, no super-admin token, no production identity. The demo-seed dataset (slice 205) is the only data context referenced; in practice, no DB queries were run because the audit operates on source code, not running state.

**Confidence:** high.

---

## Revisit once in use

These are the things the maintainer should re-evaluate once the platform has been audited by a real external party:

1. **Is the audit's severity rubric calibrated against what external auditors flag?** I graded H-1 (OIDC nonce) High using the v1 binary success criterion as the elevation. If external auditors don't flag it, the rubric should be calibrated down. If they flag _additional_ things in the same neighborhood (PKCE downgrade attacks, state binding strength, etc.) the rubric needs more sub-categories.

2. **Did the 7-surface scope cover what external auditors actually look at?** The slice doc lists 7 surfaces. If external audits surface gaps in unlisted surfaces (e.g. supply-chain provenance of dependencies, secret-rotation runbook quality, incident-response readiness), the scope should be expanded in the next audit cadence slice.

3. **Are the spillover slices being prioritized correctly?** I filed 4 (1 High + 3 Medium). If the maintainer ships H-1 within a batch but defers M-1/M-2/M-3 indefinitely, that's a signal the Medium-bar is too low (or the cap-5 should bias toward Critical/High only).

4. **Do the Informational findings (I-1, I-2, I-3) need promotion?** I-1 (board-narrative schema) is documented as v2+. If the v2 board-narrative work starts before this constraint is added, the gap becomes a real risk. Track in the v2 planning loop.

5. **Is the "engineer-as-collaborator" discipline holding?** I noted 1 adjacent issue (L-2 / IP-in-auth-code-audit gap) without filing a spillover. The rationale: low-severity, documented in code, narrow scope. If the maintainer wants tighter coverage, the rule could be: file spillover for any code TODO that exceeds N months without resolution.

6. **Should the audit cadence itself be encoded in a quarterly slice?** The 2026-Q2 audit campaign (slices 085-089) shipped quarterly hardening informational scanners. A complementary "quarterly read-only audit" cadence (like slice 327) would make the security posture review a first-class operational practice — possibly auto-filed.

---

## Confidence summary

| Decision                               | Confidence |
| -------------------------------------- | ---------- |
| D1 — report path                       | high       |
| D2 — severity rubric verbatim          | high       |
| D3 — spillover allocation (4 of 5 cap) | high       |
| D4 — slot numbers 365-368              | high       |
| D5 — scope discipline                  | high       |
| D6 — H-1 graded High not Medium        | **medium** |
| D7 — M-3 graded Medium not Low         | **medium** |
| D8 — Low/Informational report-only     | high       |
| D9 — CHANGELOG entry                   | high       |
| D10 — audit identity per AC-8          | high       |

The two medium-confidence decisions (D6, D7) are the calibration-sensitive ones. Re-evaluating them based on real external-auditor feedback is the top of the revisit list.

---

## Audit footprint

- **Files touched (added):**
  - `docs/audits/327-security-audit-security-auditor-report.md` (audit report)
  - `docs/audit-log/327-security-audit-security-auditor-decisions.md` (this file)
  - `docs/issues/365-oidc-nonce-validation.md` (spillover H-1)
  - `docs/issues/366-jwt-key-rotation.md` (spillover M-1)
  - `docs/issues/367-error-detail-leakage-audit.md` (spillover M-2)
  - `docs/issues/368-cosign-signing-migration.md` (spillover M-3)
  - `CHANGELOG.md` (Unreleased `### Security` bullet)
  - `docs/issues/_STATUS.md` (slice 327 row flipped to `in-review`)
- **Code modified:** zero (P0-327-3 / P0-327-4 / P0-327-7 honored).
- **Production tenant data:** zero (P0-327-4 honored).
- **PoCs included:** zero exploitable PoCs (P0-327-5 honored).
