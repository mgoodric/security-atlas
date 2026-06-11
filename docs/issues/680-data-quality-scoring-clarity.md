# 680 — Data-quality + scoring clarity: audit-period labels, residual/severity headers, new-risk residual

**Cluster:** Data-quality / Risks
**Estimate:** S (0.5-1d)
**Type:** JUDGMENT (scoring-column copy + seed-label correctness)
**Status:** `ready` — clusters three low-severity clarity/data findings (ATLAS-033 + 038 + 029).

## Narrative

Three low-severity clarity/data items, re-verified on `main` build `2a3805b`.

| Sub           | Finding                                                                                                                                                                                                                                                                                                                           |
| ------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **ATLAS-033** | Audit-period quarter labels contradict their date ranges and order oddly: "SOC 2 2026 Q3" = 2026-02-06→05-06, "Q1" = 2025-06→09, "Q2" = 2025-10→2026-01; row order Q3,Q1,Q2. Also FRAMEWORK VERSION shows a truncated **hash** (`e443f4b1…`) rather than a readable version. Demo-seed label correctness + a version-display fix. |
| **ATLAS-038** | Across seeded risks the same residual maps to different severities (residual 0.36 → severities 12/16/20; 0.16 → 9/12/15). **Likely NOT a bug** — inherent severity and residual-after-controls are independent axes — but the columns read as inconsistent. Clarify the column headers so the two aren't mis-read.                |
| **ATLAS-029** | A freshly created risk shows RESIDUAL = "—" and REVIEW DUE = "—" until an evaluator backfills (inherent severity 3×3=9 computes immediately). Correct behavior, but looks broken; surface a clear "pending evaluation" state instead of bare "—".                                                                                 |

## Threat model

None — seed-label correctness, column copy, and an empty-state affordance. No data/scope/wire change.

## Acceptance criteria

- [ ] **AC-1 (033).** Audit-period quarter labels match their date ranges (fix the seed's
      label↔range mapping); list orders sensibly (by date or status). FRAMEWORK VERSION renders
      a **readable version** (e.g. `SCF 2025.2` / a date), not a truncated content hash.
- [ ] **AC-2 (038).** JUDGMENT (decisions log): confirm residual vs severity are independent
      axes (expected) and **clarify the column headers / add a tooltip** so they don't read as a
      scoring inconsistency. If a genuine scoring bug is found, fix it instead and note it.
- [ ] **AC-3 (029).** A newly-created risk shows a clear **"pending evaluation"** state for
      residual / review-due (not a bare "—" that reads as broken) until the evaluator backfills.
- [ ] **AC-4.** Tests pin the audit-period label↔range correctness and the new-risk pending state.

## Anti-criteria

- Does NOT change the risk scoring model (only clarifies copy/headers unless AC-2 finds a real bug).
- Does NOT change evaluator timing (only the user-facing pending affordance — see slice 671 for the demo eval-run).

## Dependencies

- The audit-period demo seed + framework-version display (`internal/demoseed`, `web/app/(authed)/audits`); the risk list columns (`web/app/(authed)/risks`).

## Notes

Source: 2026-06-10 demo-tenant audit, items **ATLAS-033 (medium/minor), ATLAS-038 (low/minor,
likely-not-a-bug), ATLAS-029 (low/minor)**. Re-tested on `2a3805b`.
