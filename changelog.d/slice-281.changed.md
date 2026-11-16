- **web:** slice 281 — `<ListTable>` primitive gains an opt-in
  `mobileMode?: "table" | "cards"` prop. At `< md` with
  `mobileMode="cards"`, each row collapses to a card stack (`<dl>`
  with `<dt>` / `<dd>` per column) so dense list tables become
  scannable on mobile instead of horizontal-scrolling. The default
  `"table"` preserves every existing caller's rendering byte-identical
  at every viewport. Three priority pages opt in:
  `/controls`, `/risks`, `/evidence` — the three rows flagged `no` by
  slice 277's [`docs/responsive-audit.md`](docs/responsive-audit.md)
  audit at 375px. The audit doc is re-verdicted from `no` → `yes` for
  the same three rows in this slice. At `≥ md` the legacy table is
  byte-identical to pre-281 (P0-281-1 — no desktop UX regression). The
  `<ListTable>` continues to be a single primitive — no separate
  `<MobileListTable>` tree (P0-281-2), no new top-level dependency
  (P0-281-3), no consumer wire-shape change (P0-281-5). The pure-logic
  branch helper `listTableBranchClasses` is covered by vitest at
  `web/components/list/list-table.test.ts`; the JSX shape is covered
  by an extension to slice 277's `web/e2e/mobile-baseline.spec.ts`
  (one assertion per page + a desktop-no-regression assertion).
  Audit doc spillover row 281 flipped from `ready` to `merged`. Other
  list-table consumers (`/policies`, `/vendors`, `/exceptions`,
  audit-log, etc.) stay on the default `"table"` mode and get per-page
  follow-on slices as the audit's `partial` rows clarify.
