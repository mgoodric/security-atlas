- **backend:** slice 273 — board-pack `vendor_burndown` section. Closes the
  spillover from slice 221 D1=A: the deferred Option B (ship a backend
  `vendor_burndown` section) now ships. The canonical board-pack section
  set expands from 7 to 8 — `internal/board.SectionKeys` adds
  `SectionVendorBurndown = "vendor_burndown"` in slot §05, between
  `open_findings` and `operational_metrics` (slice 273 D1). The new
  section is GENERATED (not operator-entered) from the existing
  slice-122 `vendor.Store.Burndown` surface via a tiny adapter at the
  httpserver wiring layer (`vendorBurndownAdapter`); the adapter pins
  the criticality filter to `high` per slice 273 D2 — the board concern
  is overdue reviews on the vendors that matter, not the entire vendor
  portfolio. Three scalars + a derived on-time percentage drive the
  section: `vendor_burndown_total`, `vendor_burndown_on_time`,
  `vendor_burndown_past_due`, `vendor_burndown_on_time_pct`. The
  templated narrative renders three shapes — empty, all-on-time, and
  partial-overdue — pure `text/template` (NO LLM, P0 anti-criterion
  preserved). The PDF renderer walks the new key. The publish gate
  (`allSectionsApproved`) requires the new section approved before
  publish — legacy DRAFT packs from before slice 273 fail the gate and
  must be re-generated to pick up the new section (slice 273 D4
  backfill story; AC-10). The constant
  `BOARD_PACK_SECTION_KEYS` in `web/lib/api.ts` is updated to mirror
  the backend set so the FE publish-gate math stays correct; the FE
  renders the new section's chrome via the existing default fallback
  in `SectionStructured` — a dedicated `<VendorBurndown />` component
  - mockup re-insert are deferred to a follow-on FE slice (slice 273
    D6). The existing operator-entered "Vendor reviews on time" tile in
    §06 Operational metrics is NOT replaced — it remains for vendors
    outside the vendor module's tracked roster (slice 273 D5 — intentional
    two-surface). RLS cross-tenant isolation flows through the
    vendor.Store's existing tenant GUC (constitutional invariant 6); no
    new RLS surface. New unit tests cover the burndown computation
    (`vendorOnTimeFraction`, `vendorOnTimePct`), the publish-gate
    participation, the legacy-pack-missing-section guard, and the
    three narrative shapes. New integration tests cover the GENERATED
    data path end-to-end across three vendor scenarios + a
    cross-tenant RLS leak test. See
    [`docs/audit-log/273-decisions.md`](docs/audit-log/273-decisions.md)
    (D1 position · D2 criticality pin · D3 data shape · D4 backfill
    story · D5 two-surface vs §06 vendor-reviews tile · D6 minimal-FE
    scope).
