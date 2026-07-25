# 666 — Controls page count is inconsistent (header "53 of 53" vs footer "1–50 of 53")

**Cluster:** Controls
**Estimate:** XS (<0.5d)
**Type:** JUDGMENT (counter semantics / copy)
**Status:** `ready` — surfaced by the 2026-06-10 empty-tenant UI audit (ATLAS-007).

## Narrative

The Controls header reads **"Showing 53 of 53 SCF anchors"** while the table footer reads
**"Showing 1–50 of 53"** with Previous/Next pagination — a contradiction (header implies all
53 are shown; footer paginates 50/page). The total of 53 is correct. This is a copy/semantics
fix, not a data bug. Re-verified on `main` build `2a3805b`.

## Threat model

None — UI copy/consistency.

## Acceptance criteria

- [ ] **AC-1.** Header and footer use **consistent, non-contradictory semantics** — e.g.
      header = total filtered count ("53 SCF anchors"), footer = current page range
      ("Showing 1–50 of 53"). Decide the wording (decisions log) and apply it.
- [ ] **AC-2.** The header no longer implies "all 53 shown" when pagination is showing only
      the first 50.
- [ ] **AC-3.** Counts remain correct under filtering (the filtered total drives the header).

## Anti-criteria

- Does NOT change pagination page size or the underlying counts — copy/semantics only.

## Dependencies

- The Controls list page (`web/app/(authed)/controls`).

## Notes

Source: 2026-06-10 empty-tenant browser audit, item **ATLAS-007** (priority medium /
severity minor). 53 total confirmed against Evidence + Catalog dropdowns. Re-tested open on `2a3805b`.
