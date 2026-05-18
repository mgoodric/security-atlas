# 136 — Risk register data export (CSV / JSON / XLSX)

**Cluster:** Backend / Frontend
**Estimate:** 0.5d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 135 (data-export library + audit-log reference impl). The maintainer's intent is export functionality "everywhere that makes sense" — this slice wires the data-export library into the risk register surface so an operator can dump the full register to CSV / JSON / XLSX for quarterly-report use.

**What this slice ships:** `GET /v1/admin/risks/export?format=<csv|json|xlsx>` reusing the slice 135 library; the BFF + Export button on the risk-register page; canonical column set (risk_id, title, severity, likelihood, status, owner_id, accepted_at, target_date, treatment_plan_id, last_review_at, decision_log_ref, framework_satisfactions_count). Reuses slice 135's row cap, OPA gate parity, meta-audit pattern, cross-tenant isolation test, audit-period freezing.

**Scope discipline (what is OUT):** risk-aggregation rules export (separate concern — file as follow-on if needed); treatment-plan PDF export (existing; not touched); risk relationships graph export (out of scope — would warrant its own slice).

## Threat model

Inherits slice 135's threat model verbatim. Risk-register-specific addendum:

| STRIDE                       | Risk-register-specific concern                                                                                                                   | Mitigation                                                                                                                 |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------- |
| **I** Information disclosure | Risk titles + treatment narratives are sensitive; an accidentally-leaked risk register reveals the org's internal-threat assessment to attackers | Inherits slice 135 RLS enforcement. Add: column set excludes `treatment_narrative` field at v1 (separate column-select v3) |

## Acceptance criteria (stub — expand at pickup)

- [ ] AC-1: `GET /v1/admin/risks/export?format=...` reuses slice 135 library.
- [ ] AC-2: BFF route `/api/risks/export` + Export button on risk-register page.
- [ ] AC-3: Canonical column set documented in `docs/audit-log/136-risk-export-decisions.md` D1.
- [ ] AC-4: Cross-tenant isolation integration test (slice 135 AC-11 pattern).
- [ ] AC-5: OPA admit-set parity test against the risk-register read endpoint's gate.
- [ ] AC-6: Meta-audit row written on every export (action = `risk_export`).
- [ ] AC-7: Playwright e2e for the Export button.
- [ ] AC-8: CHANGELOG entry.

## Constitutional invariants honored

Inherits slice 135 (#6 RLS, #10 audit-period freezing). Adds: **#1 risk hierarchy first-class** — exports include aggregation parent-child links via `parent_risk_id` column.

## Canvas references

- `Plans/canvas/06-risk.md` — the risk register; exports include `decision_log_ref` column to preserve the "decisions appear in audit narrative as context" linkage.

## Dependencies

- **#135** Data-export library + audit-log reference impl. **Gate: 135 must be `merged` before 136 flips to `ready`.**
- Slice 056 (risk hierarchy dashboard, merged) — the risk-register read endpoint this slice's exporter parallels.

## Anti-criteria (P0 — block merge)

- Inherits slice 135 P0-A1 through P0-A14.
- **P0-A-Risk-1:** column set excludes `treatment_narrative` at v1 (defer to v3 column-selection).
- **P0-A-Risk-2:** include `parent_risk_id` so the aggregation hierarchy is preserved across export.

## Skill mix

- slice 135's `internal/export/` library — consume only.
- Go integration tests + Playwright e2e — same patterns as slice 135.

## Notes for the implementing agent

Slice 135 establishes everything; this slice is a thin wire-up. Pickup time: ~3-4 hours.

Provenance: filed 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 135.
