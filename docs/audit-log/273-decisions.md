# Slice 273 — Board-pack vendor-burndown section (build-time decisions)

> JUDGMENT-type slice. The engineer made the build-time calls per the
> slice-development workflow. This slice ships a new GENERATED board-pack
> section, not an operator-entered one — the data source (slice-122
> `/v1/vendors/burndown`) is already tenant-RLS-scoped, no new external
> IO, no new RLS surface. The product's AI-assist boundary is untouched
> (the narrative is pure `text/template` — NO LLM; the P0 anti-criterion
> from slice 032 carries forward).

**Slice spec:** [`docs/issues/273-board-pack-vendor-burndown-section.md`](../issues/273-board-pack-vendor-burndown-section.md)
**Branch:** `backend/273-board-pack-vendor-burndown`
**Spawning context:** [`docs/audit-log/221-board-pack-section-divergence-decisions.md`](221-board-pack-section-divergence-decisions.md) D1 (Option A chosen, Option B deferred) + D5 (filing 273 as the spillover).

---

## D1 — Position of `vendor_burndown` in `SectionKeys`: **slot §05** (after `open_findings`, before `operational_metrics`)

The spec's AC-1 line offered three candidate positions: "post-`open_findings`, post-`asks`, or pre-`investment` — the position decision is captured in this slice's decisions log." Slot §05 (immediately after `open_findings` and before `operational_metrics`) is chosen.

**Why:**

1. **Sibling to `open_findings`.** Vendor burndown ("how many high-criticality vendor reviews are past due?") is a "what's wrong right now" panel — the same panel-class as `open_findings` ("how many controls are failing right now?"). Slice 221 D2 put `open_findings` at slot §04 precisely because it's a "what's wrong" panel adjacent to §03 coverage trend. The same reasoning lands `vendor_burndown` at slot §05, immediately after.
2. **Generated sections precede operator-entered ones in canonical order.** The slice-032 ordering is: program posture (§01) → top risks (§02) → coverage trend (§03) → open findings (§04) → operational metrics (§05, OPERATOR-ENTERED) → investment (§06, OPERATOR-ENTERED) → asks (§07, OPERATOR-AUTHORED). Inserting a GENERATED section into the operator-entered tail would interrupt the canonical pattern; inserting at the boundary preserves it. The new ordering is: §01–§04 generated → §05 vendor_burndown generated → §06–§08 operator-entered.
3. **Slice 221 D2 precedent.** Slice 221 D2 placed `open_findings` between coverage_trend and operational_metrics with the explicit argument that "Open findings is a consequence-of-§03 (failing evaluations are the bottom of the coverage stack) and a sibling of §02 (top risks + open findings are the two 'what's wrong right now' panels)." Vendor burndown is the third member of that "what's wrong right now" cluster — past-due vendor reviews are operational risk in the same shape.
4. **Avoids a long readability jump.** Slot §08 (post-`asks`) would make the section feel like a footnote after the freeform "asks of the board" close-out. Slot §06 (pre-`investment`) would interrupt the cost-of-coverage flow (operational metrics → investment is a deliberate sequence — operational state then operational spend). Slot §05 keeps both the cause-and-effect flow (§04 findings → §05 vendor gaps → §06 metrics) and the readability story.

**Alternative rejected — slot §08 (post-`asks`).** The spec's secondary suggestion. Rejected because (a) it puts a GENERATED panel after the OPERATOR-AUTHORED `asks`, breaking the canonical "generated first, operator second" order; (b) it makes vendor burndown look like a footnote rather than a posture concern.

**Alternative rejected — slot §06 (pre-`investment`).** Rejected because it interrupts the §05 operational metrics → §06 investment flow. The operator-entered "Vendor reviews on time: N/M" tile in §05 operational metrics has its own narrative shape (manual roster outside the vendor module); inserting vendor_burndown between metrics and investment would force a reader to context-switch between "operational state" and "operational spend" by re-reading vendor-related content.

**Confidence:** high. The canonical-position arguments (generated-vs-operator-entered + sibling-to-open_findings) line up; the alternative positions break recognized patterns.

---

## D2 — Criticality filter: **`high` only** (pinned at the adapter layer)

The slice-122 `/v1/vendors/burndown` surface accepts a `criticality` query parameter and defaults to "all bands." The new board-pack adapter pins the filter to `high` instead of pulling the all-bands aggregate.

**Why:**

1. **Board concern is the high-criticality tier.** The board does not need to know that a low-criticality vendor's review is 17 days late. The board needs to know "of the vendors handling our customer data, who is past due." Slice 122's surface is also used by the dashboard (where the breakdown by criticality is useful); the board-pack consumer is the narrower one.
2. **Signal-to-noise.** A 50–80-vendor portfolio is typically ~5–15 high-criticality vendors and the rest low/medium. Including the long tail of low-criticality vendors in the "past due" count produces a noise floor that obscures the load-bearing number.
3. **Reversibility.** The adapter is a one-line filter pin in `internal/api/httpserver.go::vendorBurndownAdapter.ReadHighCriticalityBurndown`. A future call to also surface the medium tier is a follow-up slice's ADD, not a remove.
4. **Slice 221 D5 risk note alignment.** Slice 221's spawning context noted that vendor burndown's three scalars are "on the edge of the canvas §1.6 'low-information chrome' critique." Pinning to the high-criticality tier maximizes the information density per scalar — a 3/5 high-criticality past-due number IS load-bearing for a board read; a 12/80 all-bands number is not.

**Confidence:** high. The customer (the security leader presenting to the board) wants the high-criticality cut.

---

## D3 — Section data shape: **three int64 counts + a derived integer percentage + a derived float fraction**

The new section's `SectionData` fields:

```go
VendorBurndownTotal          int64
VendorBurndownOnTime         int64
VendorBurndownPastDue        int64
VendorBurndownOnTimePct      int     // half-up rounded, 0-100
VendorBurndownOnTimeFraction float64 // 0.0-1.0
```

**Why:**

1. **Mirror the slice-122 surface.** `vendor.Store.Burndown` returns `int64` counts; passing them through unchanged avoids a lossy down-cast. (Vendor counts will always fit in an int — but the symmetry with the upstream surface is more important than the small gain.)
2. **Both percentage representations.** The percentage as an `int` drives the narrative ("70% on-time"); the fraction as a `float64` is the friendly form for a future chart axis (slice 273 D6 documents that no chart ships in this slice). Storing both at generation time means the FE renderer doesn't need to re-derive (and risk a different rounding choice).
3. **`omitempty` on all five.** Zero scalars omit cleanly from the JSONB stored content — keeps the serialized pack small for empty-state tenants. The integration tests assert this: an empty-state tenant's `vendor_burndown.data` doesn't have `vendor_burndown_total: 0`; it has nothing. The narrative template handles both forms via `{{ if eq .Data.VendorBurndownTotal 0 }}`.
4. **No on-time fraction in the JSON when zero total.** Both percentage forms zero-out when `Total == 0` — the honest read for "no high-criticality vendors registered yet." `vendorOnTimeFraction` returns 0.0 (not NaN, not -1, not an error) — see the helper's unit tests.

**Alternative rejected — single derived field, compute on render.** Storing only the raw counts and deriving both percentages at render time was considered. Rejected because (a) the JSONB stored content is the system-of-record for a published pack — re-derivation at render time risks drift if the helper is ever changed; (b) the slice-031 precedent is to store the derived figures next to the raw inputs (see `coverage_trend.coverage_delta` next to `coverage_pct` + `baseline_coverage_pct`).

**Alternative rejected — match `vendor.BurndownBand` exactly.** The upstream shape uses `OnTimeFraction float64` only (no percentage int). Mirroring exactly would force every consumer to round itself, multiplying the rounding-choice risk. We store our own rounding once (half-up).

**Confidence:** high.

---

## D4 — Backfill story for in-flight DRAFT packs (AC-10): **explicit re-generate path, NOT a silent migration**

Slice 221 D5 flagged this open question: "There's no migration path on record." The slice 273 spec's AC-10 names two options: ship a backfill, OR document the re-generate path. The choice is the **documented re-generate path**.

**Why:**

1. **DRAFT pack content is JSONB; the publish gate is a Go-side iteration over `SectionKeys`.** Once `SectionKeys` adds `vendor_burndown`, the gate (`allSectionsApproved` in pack.go) walks the new 8-element list and fails closed on any DRAFT that lacks a `vendor_burndown` section. That's the behavior we want — a pack pre-dating the section MUST be re-generated to pick up the load-bearing section before publish.
2. **Backfill would silently mutate a draft mid-cycle.** A draft pack is the operator's working draft, with operator overrides + per-section approvals already entered. Silently inserting a new section would either:
   - Add it unapproved → the operator clicks publish, gets 409 (no UX gain over the re-generate path), OR
   - Add it auto-approved → violates the AI-assist boundary's "human approver" requirement (no operator has actually approved the new section).
     Neither is acceptable.
3. **Re-generate is the safe path.** `POST /v1/board-packs` always creates a NEW pack with a NEW id (slice 032's design — multiple packs per period are explicitly supported). Operators who have a mid-flight DRAFT can either (a) ignore it and re-generate to pick up the new section, or (b) continue editing the old one, knowing publish will reject it as missing the canonical section. Both paths are honest.
4. **Migration story documented in:** this decision log (D4) + the CHANGELOG entry. The slice-spec AC-10 ("ships a backfill story OR explicitly documents that operators re-generate to pick up the new section") is satisfied by the latter.

**Anti-criterion check — P0-273-2** ("Does NOT silently break existing DRAFT packs"). The publish gate's rejection IS the failure mode here, and it surfaces to the operator with a clear "first unapproved: Vendor risk burndown" error. The operator's path forward is `POST /v1/board-packs` again — exactly the same surface they use every quarter. Not silent.

**Test coverage:** `pack_test.go` adds `TestAllSectionsApproved_PreSlice273DraftMissingVendorBurndown` — a pack with the legacy seven sections all approved fails the gate and names `vendor_burndown` as the first unapproved.

**Confidence:** high. The "fail closed with a clear error" path is the right one; auto-injecting a section into a draft would violate the human-approver invariant.

---

## D5 — Two-surface vs §06 Operational metrics "Vendor reviews on time": **intentional, distinct shapes**

Slice 221 D5 raised this concern explicitly: "Slice 122's `/v1/vendors/burndown` data IS already surfaced in §05 Operational metrics ('Vendor reviews on time: 12/14'). Adding a dedicated section partially duplicates an existing tile." The slice 273 spec's P0-273-3 codifies this as a block-merge: "Does NOT duplicate the §05 Operational metrics 'Vendor reviews on time: N/M' tile. The new section either replaces / absorbs that tile OR the decisions log justifies the two-surface redundancy as intentional."

The decision: **keep both surfaces. They are intentionally distinct.**

**Why:**

1. **Different data sources.** `vendor_reviews_on_time` / `vendor_reviews_total` in §06 Operational metrics are **OPERATOR-ENTERED** scalars (slice 032 D3). They exist for a security program tracking vendor reviews in a spreadsheet, an external TPRM tool, or a roster that doesn't fit into security-atlas's vendor module. `vendor_burndown_*` in §05 is **GENERATED** from the vendor module's tracked vendors via the slice-122 surface.
2. **A program migrating from spreadsheet → vendor module gets coverage on both surfaces.** Phase 1 (spreadsheet): operator types numbers into §06. Phase 2 (partial migration): some vendors in the module, some in the spreadsheet; both numbers are useful. Phase 3 (full migration): §06's tile may be retired in a future slice if the operator's entire roster lives in the vendor module. A blanket "replace the §06 tile" today would force every phase-1 / phase-2 program to backfill the vendor module before the next board cycle.
3. **The shapes communicate different facts.** §06's `vendor_reviews_on_time` is a pure point count (N/M) with no derived percentage; it's a tile in a row of small operational stats. §05's `vendor_burndown` is a section with a templated narrative naming the past-due delta. They are different information densities for different reader needs.
4. **The two-surface fact is now documented.** This decisions log + the CHANGELOG entry name the distinction explicitly. A future maintainer reviewing the §06 tile will find the "why" here.

**Anti-criterion check — P0-273-3.** The spec offered three resolutions: replace, absorb, or justify. We justify; the justification is recorded above.

**Revisit-once-in-use (R1):** if the §06 `vendor_reviews_on_time` tile sees zero operator entries across multiple tenants across multiple quarters, that's the signal to retire it (phase-3 migration). Today there's no evidence on record either way; the safe path is to keep both.

**Confidence:** high. The migration-friendliness argument is the load-bearing one.

---

## D6 — Frontend scope: **mirror constant ONLY, no component, no mockup re-insert**

The orchestrating prompt's hard rule reads: "Do NOT add the FE rendering yet — this slice is backend-only per slice 221 D1=A spillover; the mockup will pick it up in a future slice." The slice spec's AC-6 through AC-9 cover the FE work in full. The engineer's interpretation: ship **only** the `BOARD_PACK_SECTION_KEYS` mirror update in `web/lib/api.ts`; defer everything else.

**Why the mirror update is in-scope:**

1. **Load-bearing for the publish gate UX.** `web/app/(authed)/board-packs/[id]/page.tsx` line 113 computes `approvedCount` by iterating `BOARD_PACK_SECTION_KEYS`. Line 132 sets `totalSections={BOARD_PACK_SECTION_KEYS.length}`. Line 242's `approvalState` checks every section in the mirror. If the mirror stays at 7 entries while the backend's `SectionKeys` is at 8:
   - `approvedCount` would max at 7 — the FE thinks the pack is fully approved while the backend requires 8.
   - The operator clicks publish; the backend returns 409 ("first unapproved: Vendor risk burndown"); the FE has no UX for this state because its `unapprovedTitles` array is empty.
   - This is a hard UX regression — exactly the "silently break existing DRAFT packs" anti-pattern P0-273-2 calls out.
2. **The mirror update is data, not rendering.** It's a 1-element array entry, not a new component, not a mockup change, not a SectionStructured switch case. The existing `SectionStructured` switch has a `default: return null` fallback that renders nothing inside the section card — which means the new section will render its chrome (title, approve button, templated narrative as Markdown text) via the existing `SectionCard` component without any new visual code.
3. **Slice spec AC-6 explicitly names the mirror update.** The AC reads "Frontend `BOARD_PACK_SECTION_KEYS` mirror updates." It is a load-bearing piece of the section's wire contract — the FE's source-of-truth for "what sections exist."

**Why the dedicated `<VendorBurndown />` component is OUT of scope:**

1. **Orchestrator's explicit rule.** Honoring it.
2. **AC-7 (new `<VendorBurndown />` component) + AC-8 (page-level `SectionStructured` switch case) are FE rendering work** — a styled card with three stat tiles + a percentage badge. That's a slice of its own (component + tests + e2e + design polish), not a one-line additive change.

**Why the mockup re-insert is OUT of scope:**

1. **Orchestrator's explicit rule.** Honoring it.
2. **AC-9 (mockup re-insert) is design work.** Slice 221 D3 set the precedent: mockup changes are paired with FE component changes — the mockup is a design preview of what ships. Re-inserting a mockup panel for a section without a styled FE component would be the mockup-stale-text anti-pattern that surfaced slice 221 in the first place.

**Acceptance criteria coverage:**

- **AC-1 through AC-5** — IN scope, shipped: SectionKeys / sectionTitles / generator / narrative / PDF.
- **AC-6** — IN scope (the load-bearing mirror constant), shipped.
- **AC-7, AC-8, AC-9** — OUT of scope, deferred to a follow-on FE slice. The follow-on can be filed today or later; the backend ships independently.
- **AC-10 (backfill story)** — IN scope, shipped as the documented re-generate path (D4 above).
- **AC-11 (unit + integration coverage)** — IN scope, shipped.

**Follow-on slice (to be filed by the orchestrator or a future maintainer):**

- Add `<VendorBurndown />` component to `web/components/board-pack/`.
- Add the `SectionStructured` switch case in `web/app/(authed)/board-packs/[id]/page.tsx` to dispatch to it.
- Add a Playwright spec covering the new section's render.
- Re-insert a `vendor-burndown` panel in `Plans/mockups/board-pack.html` at slot §05.
- Refresh the SECTION ORDER NOTE comment in the mockup to drop the "filable as follow-on" line.

**Confidence:** high. The mirror update is the minimum change that keeps the publish-gate math correct; the dedicated component is a separable concern.

---

## D7 — Anti-criteria scan

- **P0-273-1** ("Does NOT add a new `/v1/vendors/*` endpoint. Wire-up reads through the existing `/v1/vendors/burndown` surface."). Verified: no new route added to `internal/api/httpserver.go`'s vendor block; the new `vendorBurndownAdapter` reads through `vendor.Store.Burndown` (slice 122), the same method `/v1/vendors/burndown` calls. `grep -rn "v1/vendors" internal/api/` shows the same five routes as before. ✓
- **P0-273-2** ("Does NOT silently break existing DRAFT packs. The slice ships a backfill story OR explicitly documents the re-generate path"). Verified: D4 above documents the re-generate path. `TestAllSectionsApproved_PreSlice273DraftMissingVendorBurndown` asserts the publish gate fails closed on a legacy 7-section pack and names `vendor_burndown` in the error. The HTTP layer's 409 carries the same message body. ✓
- **P0-273-3** ("Does NOT duplicate the §06 Operational metrics 'Vendor reviews on time' tile"). Verified: D5 above justifies the two-surface as intentional. The §06 tile remains untouched in `SectionData` (`VendorReviewsOnTime *int`, `VendorReviewsTotal *int`) — those fields are OPERATOR-ENTERED, distinct from the new GENERATED `vendor_burndown_*` fields. The two-surface fact is documented. ✓

**Confidence:** high.

---

## Verification

- **AC-1 (SectionKeys adds `vendor_burndown` at slot §05).** Verified: `pack.go` line 95-103 has the new constant and the new array entry between `SectionOpenFindings` and `SectionOperational`. `TestSectionKeys_AllHaveTitlesAndAreKnown` asserts `len(SectionKeys) == 8` and the positions of the three load-bearing keys. ✓
- **AC-2 (sectionTitles adds "Vendor risk burndown").** Verified: `pack.go` line 110-118 maps `SectionVendorBurndown -> "Vendor risk burndown"`. ✓
- **AC-3 (Pack generator populates Data from `/v1/vendors/burndown` for the period).** Verified: `pack_generator.go::assemble` calls `g.vendors.ReadHighCriticalityBurndown(ctx, periodEnd)` and populates `SectionData.VendorBurndown*` from the readout. The integration test `TestPackVendorBurndown_PopulatedFromHighCriticalitySurface` exercises three scenarios (empty / all-on-time / partial-overdue) against a real Postgres + the assembled platform router. ✓
- **AC-4 (Pack narrative template produces a sane default paragraph; 3 scalars worth of templated text).** Verified: `pack_narrative.go::sectionSources[SectionVendorBurndown]` produces three distinct shapes (empty / all-on-time / partial-overdue). `TestRenderSectionNarrative_VendorBurndownShapes` covers all three plus the pluralization edge cases (1 vendor on time; 1 vendor past due). ✓
- **AC-5 (PDF renderer walks the new key).** Verified: `pack_pdf.go::writeSectionData` adds a `case SectionVendorBurndown:` branch that renders the three scalars + on-time percentage as a stat-card grid. ✓
- **AC-6 (Frontend `BOARD_PACK_SECTION_KEYS` mirror).** Verified: `web/lib/api.ts` line 1932-1941 has the 8-entry array with `vendor_burndown` in slot §05. ✓
- **AC-7, AC-8, AC-9** — deferred per D6.
- **AC-10 (backfill story).** Verified: D4 above documents the re-generate path. `TestAllSectionsApproved_PreSlice273DraftMissingVendorBurndown` asserts the publish-gate behavior on legacy drafts. ✓
- **AC-11 (unit + integration coverage).** Verified: new unit tests in `pack_test.go` (`TestVendorOnTimeFraction`, `TestVendorOnTimePct`, `TestAllSectionsApproved_VendorBurndownParticipatesInPublishGate`, `TestAllSectionsApproved_PreSlice273DraftMissingVendorBurndown`) + new tests in `pack_narrative_test.go` (`TestRenderSectionNarrative_VendorBurndownShapes`, `TestRenderSectionNarrative_VendorBurndownIsGenerated`). New integration tests in `pack_integration_test.go` (`TestPackVendorBurndown_PopulatedFromHighCriticalitySurface` with three subtests + `TestPackVendorBurndown_RLSCrossTenantIsolation`). All pass against real Postgres. ✓
- **Local CI parity** (per `feedback_local_ci_parity.md`):
  - `go build ./...` — clean.
  - `go vet ./...` — clean.
  - `go vet -tags=integration ./...` — clean.
  - `go test ./internal/...` — all pass.
  - `go test -tags=integration -count=1 ./internal/board/... ./internal/api/board/... ./internal/vendor/... ./internal/api/vendors/...` — all pass.
  - `golangci-lint run --timeout 5m ./internal/board/... ./internal/api/...` — zero issues in changed paths (warnings about adjacent worktree paths are unrelated).
  - `pre-commit run --files <changed files>` — all hooks pass (gofmt, prettier, trailing-whitespace, EOF, large-files, secrets).
  - Frontend `npm run typecheck` — pre-existing unrelated errors (NODE_ENV in `scripts/capture-readme-screenshots.test.ts`); zero errors in `web/lib/api.ts` or anything touching `BOARD_PACK_SECTION_KEYS`.
  - Frontend vitest (board-pack) — `npx vitest run board-pack` shows 16/16 tests passing.
  - CHANGELOG bullet landed under `## Unreleased / ### Added`.
