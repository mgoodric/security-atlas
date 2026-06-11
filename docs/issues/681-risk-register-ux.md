# 681 — Risk register UX: column sorting + per-risk detail; sidebar "Risks N" badge clarity

**Cluster:** Risks / Navigation
**Estimate:** S-M (0.5-1.5d)
**Type:** JUDGMENT (per-risk detail scope + badge affordance)
**Status:** `ready` — clusters two risk-area UX findings (ATLAS-039 + 036).

## Narrative

Two risk-surface UX gaps, re-verified on `main` build `2a3805b`.

| Sub           | Finding                                                                                                                                                                                                                                                                                                    |
| ------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **ATLAS-039** | With 20+ rows, the register has **no sortable columns** (RESIDUAL/SEVERITY/REVIEW DUE headers non-interactive) and risk **titles aren't links** — only "View in hierarchy". The page says per-risk detail is a "future slice". Hard to triage by severity/residual at scale; no drill-in to a single risk. |
| **ATLAS-036** | The sidebar shows "**Risks 10**" while the register has 20 (now 21) risks. The badge's `aria-label` is "10 high-severity risks" (correct), but it visually reads as a total count. It also didn't update after creating a 21st risk (verify whether the high-sev count should have changed / is live).     |

## Threat model

None — list interactivity, a read-only detail surface (if built), and a nav-badge label. No
data/scope/wire change beyond a sort param.

## Acceptance criteria

- [ ] **AC-1 (039).** The register's RESIDUAL / SEVERITY / REVIEW-DUE columns are **sortable**
      (header click toggles asc/desc; default a sensible order). Server- or client-side per the
      existing list pattern.
- [ ] **AC-2 (039).** JUDGMENT (decisions log): provide a per-risk drill-in — either make risk
      titles link to a read-only `/risks/{id}` detail, or (if detail is genuinely deferred) make
      that explicit and remove the misleading "future slice" framing. Default lean: a minimal
      read-only detail (the data exists). Coordinate with the "View in hierarchy" affordance.
- [ ] **AC-3 (036).** Disambiguate the sidebar "Risks N" badge: a tooltip/label or distinct
      styling conveying "high-severity" (not total). Confirm the count is live (updates when a
      high-severity risk is added) or document the refresh cadence.
- [ ] **AC-4.** Tests: sorting changes row order; the badge label/affordance reads as
      high-severity; (if built) a risk title navigates to its detail.

## Anti-criteria

- Does NOT add risk editing on a detail page unless explicitly scoped (read-only first).
- Does NOT change the high-severity threshold that drives the badge — only its presentation.

## Dependencies

- `web/app/(authed)/risks` (list + columns) + the risks API; the sidebar nav badge (`web/components/shell/*`).

## Notes

Source: 2026-06-10 demo-tenant audit, items **ATLAS-039 (low/minor, enhancement), ATLAS-036
(low/minor, enhancement)**. Re-tested on `2a3805b`.
