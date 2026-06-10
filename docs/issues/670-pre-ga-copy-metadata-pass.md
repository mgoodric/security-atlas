# 670 — Pre-GA copy & metadata pass (titles, breadcrumbs, raw IDs, typo, internal jargon)

**Cluster:** Platform / copy
**Estimate:** M (1-2d)
**Type:** JUDGMENT (user-facing copy authorship)
**Status:** `ready` — clusters five copy/metadata findings from the 2026-06-10 UI audit
(ATLAS-010/011/012/016/017). Follows the slice-337/343 copy-pass precedent.

## Narrative

A grab-bag of user-facing copy + metadata defects surfaced in the empty-tenant audit. They
are bundled because each is a small, low-risk copy/label fix and a single coordinated pass
is cleaner than five micro-PRs. All re-verified on `main` build `2a3805b`.

| Sub           | Finding                                                                                                                                                                                                                                                                                              |
| ------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **ATLAS-016** | Typo "**prior- answer** suggestions" (stray space after hyphen) at `web/app/(authed)/questionnaires/page.tsx:152` → "prior-answer".                                                                                                                                                                  |
| **ATLAS-012** | Control empty state ("This SCF anchor has no control instantiated") prints the full **UUID** in user-facing copy → reference the SCF anchor **code** (e.g. "AAA-01").                                                                                                                                |
| **ATLAS-011** | Breadcrumbs: Vendor Claims shows raw segment "**Oscal**" (→ "Vendor Claims"); control-detail breadcrumb ends at "Controls ›" with **no trailing leaf crumb** (→ "Controls › AAA-01").                                                                                                                |
| **ATLAS-010** | Per-page `<title>` inconsistent: `/audits` and `/oscal/component-definitions` lack the "**\<Page\> · security-atlas**" prefix that `/settings` has. (Partially improved on `2a3805b` — the raw-URL-title regression is gone, but the per-page prefix is still missing.)                              |
| **ATLAS-017** | User-facing copy leaks **build internals** — "slice 005/006", "future slice", "canvas §6.2", raw API paths ("Upload a control bundle via `/v1/controls:upload-bundle`"), "TEMPLATED V1" — across Policies, Audits, Catalog, Risks/new empty states + helper text. Replace with user-facing language. |

## Threat model

None — user-facing copy + document metadata. No data/scope/wire change.

## Acceptance criteria

- [ ] **AC-1 (016).** Fix "prior- answer" → "prior-answer".
- [ ] **AC-2 (012).** Control empty state references the SCF anchor **code**, not the raw UUID.
- [ ] **AC-3 (011).** Vendor Claims breadcrumb reads "Vendor Claims" (no raw "Oscal"); the
      control-detail breadcrumb includes the leaf control/anchor crumb.
- [ ] **AC-4 (010).** `/audits` and `/oscal/component-definitions` set a consistent
      "\<Page\> · security-atlas" document title (matching `/settings`).
- [ ] **AC-5 (017).** Sweep user-facing empty-state + helper copy for build internals (slice
      refs, "canvas §", raw `/v1/...` paths, "future slice", "TEMPLATED V1") and replace with
      user-facing language. JUDGMENT (decisions log): list the surfaces touched + the
      replacement phrasing; honor the CLAUDE.md repetition/tone discipline.

## Anti-criteria

- Does NOT change any functional behavior, route, or data — copy/labels/metadata only.
- Does NOT rewrite the project's literal canonical jargon where it is NOT user-facing (e.g.
  the "harness" reference; per CLAUDE.md project-specific exceptions) — scope is the **user-facing** UI copy only.

## Dependencies

- Web app copy + metadata across `web/app/(authed)/{questionnaires,controls,audits,oscal,policies,catalog,risks}` and the breadcrumb component (`web/components/shell/breadcrumb.tsx`).

## Notes

Source: 2026-06-10 empty-tenant browser audit, items **ATLAS-010 (partially_fixed), 011,
012, 016, 017**. Re-tested on build `2a3805b`. Clustered per the slice-337/343 copy-pass
precedent.
