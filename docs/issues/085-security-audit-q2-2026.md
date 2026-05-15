# 085 — Security audit Q2 2026

**Cluster:** Infra
**Estimate:** 0.5d (audit already complete; this slice ships the report + tracks remediation)
**Type:** JUDGMENT

## Narrative

Maintainer-requested security audit of the application beyond the existing pipeline tooling (CodeQL static analysis + GitGuardian secrets detection). Scope was source-code review looking for logic-level vulnerabilities, anti-patterns, and design flaws that automated scanners miss.

The audit report is the load-bearing deliverable: `docs/audits/2026-Q2-security-audit.md`. This slice's job is to ship that report + file follow-on remediation slices (086 / 087 / 088 / 089) for each actionable finding. The audit itself was performed at commit `ac52834` (72/81 slices merged) by an orchestrator with deep codebase familiarity, using structured `grep` passes over the highest-yield attack surfaces.

**Findings:**

| Sev         | Finding                                                                                         | Filed as      |
| ----------- | ----------------------------------------------------------------------------------------------- | ------------- |
| HIGH        | Open redirect on `signIn` `from` parameter                                                      | slice **086** |
| MEDIUM-HIGH | Missing security HTTP headers (HSTS/CSP/X-Frame-Options/X-Content-Type-Options/Referrer-Policy) | slice **087** |
| MEDIUM      | CLI `http.DefaultClient.Do(req)` without timeout (2 call sites)                                 | slice **088** |
| MEDIUM      | No dependency vulnerability scanning beyond Dependabot (no govulncheck / npm audit / Trivy)     | slice **089** |
| LOW         | AI-assist boundary not yet schema-enforced (deferred — no AI-assist surface in code today)      | not filed     |
| LOW         | No login brute-force rate-limiting (accepted under current threat model)                        | not filed     |

The audit also catalogs **strong points verified** (no action needed) — argon2id with constant-time compare, API-key HMAC storage, cookie flags, tenant GUC on every request, four-policy RLS, no exec.Command, etc.

**This slice is NOT the remediation work.** That's 086 / 087 / 088 / 089, each independently fixable, each filed as its own `ready` slice.

## Acceptance criteria

- [ ] AC-1: `docs/audits/2026-Q2-security-audit.md` exists with sections: Methodology, Findings summary, Detail (per finding), Strong points, Notes for maintainer.
- [ ] AC-2: Four follow-on remediation slices exist in `docs/issues/`, status `ready`: 086 (open redirect), 087 (security headers), 088 (CLI timeout), 089 (dependency scanning).
- [ ] AC-3: `_STATUS.md` updated. Counts reconciled. Drift section explains the audit + the 5 new slices (085 + 086 + 087 + 088 + 089).
- [ ] AC-4: README.md "Security" section (create one if absent) links to `docs/audits/` and explains the audit cadence (quarterly + after major auth/middleware changes).
- [ ] AC-5: Pre-commit clean. CI green.

## Constitutional invariants honored

- **AI-assist boundary**: the audit itself is human-orchestrator work; no LLM auto-generation of findings. Each finding cites the specific file path + line that surfaced it.
- **Working norms — Cite sources**: every finding in the report cites the file and the specific code pattern it relies on.

## Canvas references

- _(none — operational security hygiene; canvas doesn't speak to audit cadence)_

## Dependencies

- All currently-merged slices on `main` (the audit operates on the merged state)

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT auto-execute any of the remediations (086 / 087 / 088 / 089). They're separate slices for review + scheduling.
- **P0-A2**: Does NOT replace third-party penetration testing. The audit report explicitly notes it's a first-pass review.
- **P0-A3**: Does NOT silence the LOW findings without recording them. AI-assist schema + brute-force rate-limiting are documented as accepted-risk, with re-trigger conditions noted.
- **P0-A4**: Does NOT batch this slice with the remediation slices (086-089). They land separately so each remediation has a clean diff + clean review boundary.

## Skill mix (3–5)

- `security-review` (the audit IS the skill applied to the entire codebase)
- `engineering-advanced-skills:runbook-generator` (the report's per-finding format mirrors a remediation runbook)
- `simplify` (the report is long but tight — every section has a purpose)

## Notes for the implementing agent

- The audit work is already done. This slice's deliverable is mostly the file moves + the README.md "Security" section + the status-tracker reconciliation. No source-code investigation needed.
- The four remediation slices (086-089) ship as separate ready-state slices, NOT auto-batched. The maintainer chooses when to staff each.
