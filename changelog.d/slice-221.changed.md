- **docs:** slice 221 — board-pack mockup now matches the live
  `BOARD_PACK_SECTION_KEYS` ordering (D1=A, mockup-only edit). The
  iteration-1 mockup at `Plans/mockups/board-pack.html` shipped a §06
  "Vendor risk burndown" panel that the v1 product does not render
  AND was missing the "Open findings" section that the v1 product
  does render — the divergence was surfaced by the slice 204 audit
  fleet and captured as slice 221 follow-up per continuous-batch
  policy. This slice drops §06 Vendor risk burndown, inserts a new
  "Open findings" panel in the canonical position between §03
  coverage trend and operational metrics (mirroring
  `web/components/board-pack/findings-list.tsx` density with three
  sample rows using real SCF anchor IDs — IAC-06, CRY-04, VPM-02 —
  to preview the freshness_status taxonomy), and renumbers §04-§07
  to keep the chain contiguous. A new HTML comment block names
  `internal/board/pack.go SectionKeys` + `web/lib/api.ts
BOARD_PACK_SECTION_KEYS` as the authoritative source list and
  references slice 032's decision D6. Pure mockup edit — no backend,
  no wire shape, no `web/` source touched (P0-221-1). Option B
  (ship a `vendor_burndown` backend section) was deferred per
  Amendment 2 (spillovers as slices) and filed as slice 273 for a
  future maintainer call. Decisions log at
  [`docs/audit-log/221-board-pack-section-divergence-decisions.md`](docs/audit-log/221-board-pack-section-divergence-decisions.md).
