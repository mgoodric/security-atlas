# Slice 221 — board-pack section divergence (build-time decisions)

> AFK-type slice. Engineer made the build-time calls per the
> slice-development workflow. The product still never publishes
> audit-binding artifacts without one-click human approval — this slice
> is mockup-only (pure documentation / design-artifact change; no auth,
> no data-fetch, no RLS surface — matches the slice spec's threat-model
> verdict of "no-mitigations-needed").

---

## D1 — Pick Option A (update the mockup) over Option B (ship a backend section)

**Decision:** **Option A**. `Plans/mockups/board-pack.html` is updated
to match the live `BOARD_PACK_SECTION_KEYS` ordering: §06 "Vendor risk
burndown" is dropped, "Open findings" is inserted in the canonical
position between §03 coverage trend and the operational tiles, and
§04–§07 are renumbered to keep the chain contiguous. Option B (ship a
`vendor_burndown` section in the backend pack generator) is filed as
spillover slice **273** for a future maintainer call.

**Why:**

- **The spec defaults AC shape to Option A.** Lines 65-66 of the slice
  spec read: "Defaulting AC shape to Option A — Option B is filable as
  a follow-on if the maintainer decides vendor burndown belongs in the
  board pack." There is no customer-demand evidence on record (no
  surfacing slice, no Mempalace note, no audit-log entry) that justifies
  the 5x larger Option B budget today.
- **The drift is asymmetric in cost.** Closing the mockup-vs-live gap
  via mockup edit is 0.5d of HTML; closing it via backend section is
  2.5d AND adds runtime surface (a new section in every pack ever
  generated, a new templated narrative, a new component, spec coverage,
  a publish-gate approval row per pack). Mockup edit is reversible by
  a single PR if the maintainer flips to Option B later; the reverse
  (ship the section, then yank it) is the kind of irreversible-decision
  trap the canvas warns about. (`Plans/canvas/01-vision.md` anti-pattern
  "ship features, then yank them".)
- **The mockup is a design artifact, not a contract.** Slices 218, 219,
  220 all set the precedent that when the mockup and the live UI
  diverge, the mockup yields to the live shape (the iteration-1 mockup
  was a design hypothesis; the live UI is a tested commitment). The
  "honesty > parity" framing from slice 219 D2 applies directly: the
  mockup describes a panel that v1 does NOT ship, and continuing to
  preview a non-shipping panel is the mockup-stale anti-pattern that
  surfaced slice 221 in the first place.
- **The amendment-2 split rule applies cleanly.** The prompt's hard
  rule reads: "If you pick Option B, you must split — file Option B as
  a separate spillover slice and ship Option A here." Since the rule
  forces a split anyway, picking A here keeps the spillover (273)
  isolatable and lets the maintainer make the Option-B call later
  on its own merits.

**Alternative rejected — Ship Option B in this PR.** Five reasons not
to:

1. The `vendor_burndown` data source (`/v1/vendors/burndown`) per slice
   122 exists but ships only three scalar tiles (high-criticality
   vendors / reviewed-on-time / past-due). Adding a 4th board-pack
   section that's just "three scalars + a templated paragraph" is the
   kind of low-information chrome the canvas anti-pattern §1.6
   penalizes ("trust-center vanity").
2. The publish-gate (`internal/board/pack.go` `isKnownSection` /
   `SectionKeys`) is the canonical source of "what sections must
   approve before publish". Adding `vendor_burndown` would require
   reconsiderating which existing customer's draft packs are mid-flight
   and would need re-approval to publish. There's no migration path on
   record.
3. Slice 122's `/v1/vendors/burndown` data IS already surfaced in §05
   Operational metrics ("Vendor reviews on time: 12/14"). Adding a
   dedicated section partially duplicates an existing tile.
4. The 2.5d budget would cross into batches 100+ engineering capacity
   currently allocated to auth-substrate-v2 spillovers (slices 197–202,
   208–210) — that's the load-bearing critical path.
5. The slice spec's threat model (Verdict: no-mitigations-needed) was
   written assuming Option A's mockup-only surface. Option B opens new
   RLS read paths through the vendor module and would need its own
   pass — not a blocker, but more spec discipline than this slice
   buys.

**Alternative rejected — Do nothing (leave the mockup divergent).** The
mockup is currently the iteration-1 design artifact referenced from
`Plans/canvas/07-metrics.md` (board reporting first-class) and is
loaded by the slice-178 audit harness for mockup-vs-live drift
detection. Leaving the divergence means slice 178's `mockup-diff` step
keeps flagging the section-order delta on every run, costing future
engineers grep time. Closing the loop in one mockup-only PR is the
cheapest possible resolution.

**Confidence:** high. Spec-defaulted, prompt-recommended, precedent-aligned.

---

## D2 — Insert "Open findings" between §03 coverage trend and operational metrics

**Decision:** The new "Open findings" panel lands in slot §04 — between
§03 control coverage trend and what was §04 (now §05) operational
metrics. The spec offered two options at AC-2: "after §03 coverage
trend, before §04 operational metrics" OR "after §07, whichever flows
better." The slot-§04 position is chosen.

**Why:**

- **Canonical order.** `internal/board/pack.go SectionKeys` orders
  `open_findings` between `coverage_trend` and `operational_metrics`
  (lines 97-105). Inserting after §07 would render the mockup in a
  different order than the live page renders, which is exactly the
  drift the slice is closing.
- **Reader flow.** The board pack reads top-to-bottom posture (§01) →
  risks (§02) → trend (§03) → open findings (§04) → operational metrics
  (§05) → investment (§06) → asks (§07). Open findings is a
  consequence-of-§03 (failing evaluations are the bottom of the
  coverage stack) and a sibling of §02 (top risks + open findings are
  the two "what's wrong right now" panels). Burying it after asks
  would make it look like a footnote.
- **AC-2 explicit.** The slice spec AC-2 line reads: "inserts a new
  'Open findings' section in the canonical position (after §03
  coverage trend, before §04 operational metrics) so the mockup order
  matches `BOARD_PACK_SECTION_KEYS`." The "after §07" path the spec
  offered earlier in the narrative is the secondary fallback — AC-2
  pins the primary path.

**Confidence:** high. Spec AC-2 is unambiguous; the only call here was
which of the two positions the spec offered to take, and AC-2 names
the right one.

---

## D3 — Findings panel content: render three sample rows, not an empty state

**Decision:** The mockup's new §04 Open findings panel shows three
sample finding rows — SCF:IAC-06 stale (2026-03-12), SCF:CRY-04 expired
(2026-02-28), SCF:VPM-02 missing (2026-03-22) — plus the live
component's "N open findings as of period end" caption. The
alternative was an empty-state ("No open findings as of period end")
or zero data.

**Why:**

- **Mockup intent is "preview the v1 product"** (CLAUDE.md anti-pattern
  list — mockup-stale text). An empty-state preview tells a future
  reader nothing about how the panel renders when it has data. Three
  rows cover three of the four `freshness_status` tones (fresh / stale
  / expired / missing) so the reader sees the visual taxonomy.
- **Sample data uses real SCF anchor IDs**, not fabricated control IDs.
  IAC-06 (identification and authentication), CRY-04 (cryptographic
  protection), VPM-02 (vulnerability program) are all real SCF anchors
  in the catalog (slice 006). Using real anchor IDs prevents the
  mockup from inventing controls that don't exist in the SCF — which
  would be the canvas §3.5 "SCF is the canonical control catalog"
  violation in design-artifact form.
- **The "fresh" tone is excluded from the sample rows on purpose.** A
  fresh evaluation is by definition NOT an open finding — the
  freshness_status of a finding can only be stale, expired, or missing
  (per `web/components/board-pack/findings-list.tsx` line 16-21
  freshnessTone map: fresh tones rendered but the panel filters to
  open findings only). Showing a "fresh" row in the mockup would be
  inconsistent with the live filter.
- **Three rows fit in the existing card density.** §02 Top risks ships
  four rows; §04 Open findings shipping three keeps the page visual
  weight balanced without artificially padding the panel.

**Alternative rejected — render the live component's empty state.**
The empty-state path (`<p>No open findings as of period end.</p>`)
is the right behavior at runtime but doesn't preview the panel's
information density. Mockups exist to communicate shape; an empty
mockup defeats the purpose.

**Alternative rejected — copy the live React component's exact JSX
into the mockup via `<template>` blocks or build pipeline.** Out of
scope. The mockup is plain HTML + Tailwind via CDN (no build step)
per `Plans/mockups/` convention. Hand-translating the component
shape into HTML is the standing pattern (every other section in
the mockup is hand-translated; see §02 Top risks vs the live
`top-risks-table.tsx`).

**Confidence:** high.

---

## D4 — HTML comment block documents the source-of-truth pointer (AC-4)

**Decision:** A new HTML comment block lands between §03 and §04 (the
position the new content was inserted at) that names:

- `internal/board/pack.go SectionKeys` as the canonical section list.
- `web/lib/api.ts BOARD_PACK_SECTION_KEYS` as the frontend mirror.
- `docs/audit-log/032-quarterly-board-pack-decisions.md` decision D6 as
  the source-of-truth pin (the spec's `Canvas references` line 99).
- Slice 221 itself as the trigger, with the divergence reason ("the
  mockup previously shipped a §06 'Vendor risk burndown' panel and had
  no 'Open findings' section") and the deferred-Option-B follow-on.

The comment block sits at the boundary where the new content was
inserted because that's where a future reader looking at the diff or
opening the file in iteration-2 will land.

**Why:**

- **AC-4 explicit.** The spec requires "a new comment block in the
  mockup HTML notes that section order is the authoritative list from
  `BOARD_PACK_SECTION_KEYS` and references slice 032 as the source of
  truth." Both pointers land in the block.
- **Cross-reference hygiene** (skill mix item #2 in the spec). Three
  surfaces stay in sync: Go SectionKeys, TS BOARD_PACK_SECTION_KEYS,
  the mockup. The comment is the lightweight glue that signals the
  invariant to a future reader.
- **The slice 218 D3 precedent.** Slice 218 also added an HTML comment
  to the mockup explaining the divergence reason (Sentinel Labs +
  Board reports segments removed). Same pattern here — a brief
  in-file annotation rather than a separate doc file.

**Confidence:** high.

---

## D5 — File Option B as spillover slice 273, do not ship it now

**Decision:** A new slice spec lands at
`docs/issues/273-board-pack-vendor-burndown-section.md`, type
`JUDGMENT`, status `ready`, estimate 2.5d. It captures Option B's
shape: ship a `vendor_burndown` section in `internal/board/pack.go
SectionKeys`, wire it into the generator + publish gate + narrative
templates + PDF + frontend component, all built on the existing
`/v1/vendors/burndown` endpoint per slice 122. The spec names slice
221 as the spawning context.

**Why:**

- **Amendment 2 (spillovers as slices).** The prompt's hard rule reads:
  "If you pick Option B, you must split — file Option B as a separate
  spillover slice and ship Option A here." I picked A, but Option B is
  still a real future call the maintainer might want to make. Without
  a spillover slice on file, the option B work would lose its
  surfacing context.
- **Slice 273 is the next free issue number** (highest existing slice
  in `docs/issues/` is 272 per `ls docs/issues/`). Numbers are
  monotonic, no renumbering required.
- **The spec is JUDGMENT-typed, not AFK** because the call to expand
  the canonical seven-section set IS a maintainer judgment call —
  expanding `SectionKeys` from 7 to 8 ripples through the publish gate
  (`internal/board/pack.go isKnownSection`), the PDF renderer
  (`pack_pdf.go`), the narrative templates (`pack_narrative.go`),
  every existing draft pack's content shape, and a migration story
  for in-flight packs that would need a vendor_burndown approval to
  publish. None of those is a code-only call.

**Confidence:** high.

---

## D6 — Anti-criteria scan

- **P0-221-1 (Option A does NOT modify the live `BOARD_PACK_SECTION_KEYS`
  set or backend pack generator).** Verified: no Go file touched
  (`git diff main -- internal/board/ pkg/ cmd/`), no TS source touched
  (`git diff main -- web/lib/api.ts web/components/board-pack/`), no
  `proto/` touched, no `migrations/` touched. The only edit is to
  `Plans/mockups/board-pack.html` plus a new audit-log file and a new
  spillover-slice spec. ✓
- **P0-221-2 (does NOT add new sections beyond the seven canonical
  keys).** Verified: the new §04 Open findings section IS one of the
  seven canonical keys (`open_findings` is in `SectionKeys`); the
  removed §06 Vendor risk burndown was NOT in the canonical keys (it
  was a mockup-only artifact). Net result: mockup now has exactly
  seven sections, matching the seven keys in `SectionKeys`. ✓
- **P0-221-3 (does NOT delete the §07 "Asks of the board" section).**
  Verified: §07 Asks of the board renders intact at file lines
  406-435; only §06 Vendor risk burndown was removed. ✓

**Confidence:** high.

---

## Revisit once in use

- **R1 — Vendor burndown spillover (slice 273).** If the maintainer
  picks Option B in a future batch, slice 273 ships the backend
  `vendor_burndown` section. When that lands, the mockup gains a §08
  panel (or §05, depending on the canonical position decided at that
  time) and the SECTION ORDER NOTE in the mockup updates to drop the
  "filable as follow-on" line.
- **R2 — Iteration-2 mockups.** When iteration-2 mockups land, the
  in-file SECTION ORDER NOTE comment becomes obsolete and the file
  should be rewritten from scratch. The 221 comment is a stopgap —
  the same status as the 218 breadcrumb comment.
- **R3 — Slice 178 audit harness `mockup-diff` baseline.** If slice
  178's harness keeps a record of "expected mockup section count
  per page", that record should be refreshed to expect 7 sections on
  `board-pack.html` (was 7 sections that happened to include the
  divergent `vendor_burndown`; now 7 sections that match the live
  canonical order). Not a blocker for slice 221.

---

## Verification

- **AC-1.** `Plans/mockups/board-pack.html` removes §06 "Vendor risk
  burndown" entirely. Verified by `grep -n "Vendor risk burndown"
Plans/mockups/board-pack.html` (zero matches post-edit). ✓
- **AC-2.** A new "Open findings" section is inserted between §03
  coverage trend and the operational metrics section, mirroring
  `BOARD_PACK_SECTION_KEYS` order. Verified by `grep -n "§ 0\|SECTION"
Plans/mockups/board-pack.html` showing §01-§07 contiguous with
  Open findings at §04. ✓
- **AC-3.** Section heading numbers renumbered §01-§07 contiguous.
  Verified by the same grep — §05 is Operational metrics (was §04),
  §06 is Investment vs coverage (was §05), §07 unchanged. ✓
- **AC-4.** A new HTML comment block (D4) names
  `BOARD_PACK_SECTION_KEYS` as the authoritative list and references
  slice 032's decision D6. Verified by inspecting the file. ✓
- **P0-221-1, P0-221-2, P0-221-3.** All clean per D6 above. ✓
- **Local CI parity** (per `feedback_local_ci_parity.md`):
  - `pre-commit run --all-files` — clean (mockup is HTML; no
    formatter or linter touches it on the touched paths).
  - Web frontend checks (`npm run lint && npm run test && npx tsc
--noEmit && npm run build`) — not applicable (no `web/` source
    touched in this slice).
  - CHANGELOG bullet — landed under `## Unreleased / ### Changed`.
