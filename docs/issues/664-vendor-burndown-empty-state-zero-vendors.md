# 664 — Vendor "Review burndown" shows 100% on-time with 0 vendors (divide-by-zero display)

**Cluster:** Vendors
**Estimate:** XS-S (<0.5d)
**Type:** AFK
**Status:** `ready` — surfaced by the 2026-06-10 empty-tenant UI audit (ATLAS-009).

## Narrative

With zero vendors, the Vendors "Review burndown" widget reports **"100% ON-TIME / 0
vendors"**, which is misleading (an empty population is not 100% compliant). Re-verified on
`main` build `2a3805b`. Classic empty-population display bug: the on-time-rate numerator/
denominator is 0/0 and renders as 100% rather than N/A.

## Threat model

No security surface — display-only empty-state correctness.

## Acceptance criteria

- [ ] **AC-1.** When the vendor count is 0, the on-time rate renders **"—" / "N/A"** (not
      "100%"). The "0 vendors" count stays accurate.
- [ ] **AC-2.** With ≥1 vendor the rate computes as before (no regression).
- [ ] **AC-3.** Unit/Playwright assertion pins the zero-vendor empty state.

## Anti-criteria

- Does NOT change the burndown computation for populated tenants.

## Dependencies

- The Vendors burndown widget (`web/app/(authed)/vendors`, the burndown component).
- Sibling to slice 662 (board-pack vendor-burndown section) — keep the empty-state framing consistent.

## Notes

Source: 2026-06-10 empty-tenant browser audit, item **ATLAS-009** (priority medium /
severity minor). Re-tested open on build `2a3805b`.
