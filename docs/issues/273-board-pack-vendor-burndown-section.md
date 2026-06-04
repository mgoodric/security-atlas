# 273 — Board-pack: vendor-burndown section

**Cluster:** board
**Estimate:** 2.5d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Spillover from slice 221. Slice 221 chose Option A (update the mockup
to match the live `BOARD_PACK_SECTION_KEYS` set) and explicitly
deferred Option B — shipping a `vendor_burndown` section in the backend
pack generator — to this slice for a future maintainer call. See
`docs/audit-log/221-board-pack-section-divergence-decisions.md` D1
(rationale for deferral) + D5 (rationale for the split) for the
context.

The vendor module already exposes the data: slice 122's
`/v1/vendors/burndown` endpoint returns high-criticality-vendor
counts (total / reviewed-on-time / past-due) per tenant. This slice
is wire-up work, not data modeling.

The maintainer call is JUDGMENT-typed because expanding the canonical
seven-section set from 7 to 8 ripples through:

- `internal/board/pack.go SectionKeys` + `isKnownSection` + `sectionTitles`
- The pack generator (`pack_generator.go`)
- The narrative template (`pack_narrative.go`) — a new templated
  paragraph for the section
- The PDF renderer (`pack_pdf.go`)
- The publish gate (`canPublish` / per-section approval rows)
- The frontend `BOARD_PACK_SECTION_KEYS` mirror in `web/lib/api.ts`
- A new frontend component matching `findings-list.tsx` density
- Mockup: re-insert a vendor-burndown panel at the canonical position
  in `Plans/mockups/board-pack.html` and refresh the SECTION ORDER NOTE
- A migration story for in-flight DRAFT packs that would need a new
  approval row

## Threat model

**Verdict.** **no-mitigations-needed.** The vendor module's
`/v1/vendors/burndown` endpoint is already tenant-RLS-scoped (slice 122
ships it on the standard tenant context); this slice reads through
that existing surface. No new external IO, no new RLS surface, no new
auth context. Pack generation already runs in tenant context; the new
read joins the existing tenant-scoped flow.

## Acceptance criteria

- **AC-1.** `internal/board/pack.go SectionKeys` adds
  `SectionVendorBurndown = "vendor_burndown"` in the canonical
  position (TBD by maintainer: post-`open_findings`, post-`asks`,
  or pre-`investment` — the position decision is captured in this
  slice's decisions log).
- **AC-2.** `sectionTitles` adds the section title
  ("Vendor risk burndown" — mirroring the prior mockup text).
- **AC-3.** Pack generator populates the section's `Data` from
  `/v1/vendors/burndown` for the period.
- **AC-4.** Pack narrative template produces a sane default
  paragraph (3 scalars worth of templated text).
- **AC-5.** PDF renderer walks the new key.
- **AC-6.** Frontend `BOARD_PACK_SECTION_KEYS` mirror updates.
- **AC-7.** New `<VendorBurndown />` component renders the three
  scalars in the established board-pack card density.
- **AC-8.** Page-level `SectionStructured` switch case dispatches.
- **AC-9.** Mockup `Plans/mockups/board-pack.html` re-inserts a
  vendor-burndown panel at the chosen canonical position, with the
  SECTION ORDER NOTE comment refreshed to drop the "filable as
  follow-on" line.
- **AC-10.** Existing draft packs gain a vendor-burndown section on
  next re-generate (the slice ships a backfill story OR explicitly
  documents that operators re-generate to pick up the new section).
- **AC-11.** Unit + integration coverage for the new section (Go
  unit floor honored per `cmd/scripts/coverage-thresholds.json`;
  generator integration test covers the new data path).

## Constitutional invariants honored

- **Invariant 6 (tenant isolation).** Reads through the existing
  tenant-RLS-scoped `/v1/vendors/burndown` surface; no new RLS table
  introduced.
- **Invariant 9 (manual evidence is first-class).** Vendor burndown
  is sourced from the vendor module which mixes connector + manual
  evidence per existing tenant pattern; no new evidence kind.
- **Anti-pattern check.** The 3-scalar shape is on the edge of the
  canvas §1.6 "low-information chrome" critique — the slice's
  decisions log MUST justify that the section earns its keep in the
  canonical seven (now eight) set vs. being a §05 Operational metrics
  tile (which already includes "Vendor reviews on time: N/M").

## Canvas references

- `Plans/canvas/07-metrics.md` — board reporting first-class
- `docs/audit-log/032-quarterly-board-pack-decisions.md` D6 — the
  canonical SectionKeys decision being expanded
- `docs/audit-log/221-board-pack-section-divergence-decisions.md` D1
  - D5 — the spawning context

## Dependencies

- **#221** (board-pack section divergence — spawning slice) — `ready`
  (the spillover trigger)
- **#032** (quarterly board pack — section-set authority) — `merged`
- **#043** (board-pack detail view — frontend section components) — `merged`
- **#122** (vendor burndown endpoint) — `merged` (the data source)

## Anti-criteria (P0 — block merge)

- **P0-273-1.** Does NOT add a new `/v1/vendors/*` endpoint. Wire-up
  reads through the existing `/v1/vendors/burndown` surface.
- **P0-273-2.** Does NOT silently break existing DRAFT packs. The
  slice ships a backfill story OR explicitly documents the
  re-generate path (and the publish-gate UX shows the new unapproved
  section).
- **P0-273-3.** Does NOT duplicate the §05 Operational metrics
  "Vendor reviews on time: N/M" tile. The new section either
  replaces / absorbs that tile OR the decisions log justifies the
  two-surface redundancy as intentional.

## Skill mix (3-5)

1. Go: `internal/board` SectionKeys expansion + generator wiring
2. Go: pack narrative template + PDF renderer walk
3. TS / React: `BOARD_PACK_SECTION_KEYS` mirror + new component
4. Mockup edit + cross-reference comment refresh
5. Integration test discipline (touch the publish-gate path with a
   real Postgres + real vendor data)
