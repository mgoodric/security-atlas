# #2301 — ISO A.5.9 asset inventory edge moves from AST-22 to AST-04: decisions log

Follow-up to #1973 D3 (`1973-scf-ast-bcd-remap-decisions.md`). ISO 27001:2022
A.5.9 Inventory of information and other associated assets sat on `AST-22`
Secure Disposal, Destruction or Re-Use of Equipment. It sat there only because
the sample once had nothing closer (it was `AST-09` Asset Disposal before the
2026.3 remap). The sample now carries `AST-04` Asset Inventories.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: unit

## D1 — Anchor: AST-04

**Source.** The SCF 2026.3 workbook (`secure-controls-framework-scf-2026-3.xlsx`,
the upstream `securecontrolsframework` repo, sheet `SCF 2026.3`) lists `5.9`
in both its ISO 27001 2022 and ISO 27002 2022 columns for `AST-04` Asset
Inventories. It also lists 5.9 for several narrower controls (`AST-06`,
`AST-07`, `AST-17`, `DCH-02`, `DCH-04`, `PRI-07`, `PRM-09`). For `AST-22`,
SCF lists only `7.14` and `8.10`, not 5.9. `AST-04` is the only one of those
in the bundled sample whose subject is the inventory itself. No ISO text is
quoted here or in the crosswalk.

## D2 — Relationship and strength: `intersects_with` 0.8

**Decision.** `intersects_with` at 0.8, the top of the file's "high overlap"
band.

**Why.** The SCF workbook gives set membership only. It carries no STRM
relationship type, so the type is our call under the file's rubric. A.5.9
asks for an inventory and an owner for each entry. `AST-04` covers the
inventory, while ownership sits in other SCF controls, so the relationship
is not `equal`. `subset_of` would claim AST-04 fully covers A.5.9 and is
broader, which is not true. The NIST CSF crosswalk scores the
inventory-only ID.AM-01 and ID.AM-02 as `equal` 0.9 on the same anchor;
A.5.9 sits one step below them because of the ownership half.

**Confidence:** MEDIUM. The anchor is SCF's own; the type and the strength
are judgment.

## D3 — Upgrade: retire through convergence, no migration

**Decision.** Change only the bundled YAML. No data migration.

**Why.** Boot crosswalk convergence (#2014, `soc2import.ConvergeImported`)
re-imports every bundled crosswalk the install has already imported. The
import's per-source retire pass sets `retired_at` on the A.5.9 to AST-22
edge, because the bundled file no longer lists it. It never deletes: the row
and its tier history stay (the #1999 pattern), and convergence logs it as
`edge retired A.5.9->AST-22`. The same import creates A.5.9 to AST-04. A fresh
install imports the new file and never has the old edge.

**Overlays.** Tenant overlay edges live in `fw_to_scf_overlay_edges`, which
the importer never reads. An active overlay on A.5.9 to AST-22 stays active,
and it is the tenant's own assertion, so it keeps working. Nothing is
deleted, so the #2474 delete guards on anchors and requirements are not
involved.

**Tests.** `iso_a59_repoint_integration_test.go`:
`TestISOA59_FreshInstallHasOnlyAST04` and
`TestISOA59_UpgradeRetiresAST22AndKeepsOverlay`. Both were run against the
old YAML first and failed there, then passed with the new one.

## D4 — Which D3 the ticket meant

The ticket asks to update "the 1717 decisions log D3". In
`1717-scf-pin-ratchet-decisions.md`, D3 is about licensing. The A.5.9 note is
D3 of `1973-scf-ast-bcd-remap-decisions.md`, a #1717 family-group log, so
that is the one updated to point here.
