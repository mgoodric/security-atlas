# Slice 662 — board-pack §05 vendor-burndown render + human-label publish blocker — decisions log

**Type:** JUDGMENT
**Branch:** `feat/662-board-vendor-burndown-render`
**Scope:** FE (`web/`) + minimal `internal/demoseed` (AC-4 seed fix). No proto / migration / schema-registry change.

## Summary

§05 (`vendor_burndown`) was not rendered on `/board-packs/{id}` (sections jumped §04 → §06) and
the publish blocker showed the raw internal key `vendor_burndown` among human labels. Root cause
was twofold:

1. **FE gap.** `SectionStructured` had no `case "vendor_burndown"` (fell through to
   `default: return null` → blank §05 visual), the publish-gate built blocker labels with
   `section.title || key` (raw key when the title was empty or the section was missing), and the
   section loop did `if (!section) return null` (silently dropped a slot).
2. **Data root cause (AC-4).** The slice-205 demo seed wrote `board_packs.content.sections` as a
   **JSON array** of five untyped objects (no `key` field), not the keyed
   `map[string]Section` shape `board.Pack` marshals to. This is also the root cause of
   **slice 673** (`GET /api/board-packs` 500 in the seeded tenant) — see D4.

## Decisions

### D1 — vendor_burndown visual shape

A minimal three-tile structured panel (`web/components/board-pack/vendor-burndown-panel.tsx`)
styled to match the existing simple panels (operational tiles / investment panel): **high-criticality
vendors total**, **reviews on time (`onTime/total` + on-time %)**, and **past due**. Consumes the
wire fields `vendor_burndown_total` / `_on_time` / `_past_due` / `_on_time_pct` (already on the
backend wire — `internal/board/pack.go` SectionData, slice 273). No new wire fields, no fabrication:
an empty/zero-vendor tenant renders honest `—` / zeros (consistent with the ATLAS-009 empty-state
honesty sibling). Rejected: a full burndown chart (over-engineered for a board summary tile row;
the board concern is the on-time fraction, not a time series).

### D2 — FE label-map approach + always-human-label rule (AC-2, load-bearing)

Added `SECTION_TITLES: Record<string,string>` (all eight keys) + `sectionLabel(key, section)` to
`web/lib/api/board.ts`, mirroring backend `sectionTitles`. Resolution order is
`SECTION_TITLES[key] ?? (section?.title || key)`. The canonical FE map is preferred over the served
title so a label renders **even when the served `title` is empty OR the section is missing entirely**.
The raw key is only ever the last-resort floor for an unknown key, which the fixed set never
contains. Applied to (a) the publish blocker builder (`approvalState`) and (b) the section header
(the page resolves the label before handing the section to `SectionCard`, so an empty served title
still shows a human header). This is the AC-2 invariant: **the UI never shows a raw key.**

### D3 — missing-section graceful state (AC-3)

A missing `sections[key]` now renders a `MissingSection` component (dashed-border card with the §NN
marker + the human label + a muted "not generated" note) rather than `return null`. This keeps the
numbering contiguous (no §04 → §06 jump) and is robust to an incomplete stored pack. The publish-gate
treats a missing section as **unapproved using its human label** (`!section || !section.approved →
push(sectionLabel(key, section))`) — no crash on `section.approved`/`section.title`, no raw key.

### D4 — demo-seed finding (complete vs incomplete) + slice 673 linkage

**Finding: the seed was INCOMPLETE and structurally wrong.** `internal/demoseed/fixtures.go` wrote
`content.sections` as a 5-element **array** of untyped maps (missing `coverage_trend`,
`vendor_burndown`, `operational_metrics`, and the `key` field on every entry). `board.Pack.Sections`
is `map[string]Section`; `json.Unmarshal` of a JSON array into a Go map **errors**, so
`storedPackFromRow` returned an error and both `GET /api/board-packs/{id}` and the list endpoint
500'd on the seeded tenant.

**Fix:** `demoBoardPackContent()` now builds the exact `board.Pack` JSON envelope — a key→section
**map** carrying **all eight `SectionKeys`** with titles, every section approved (the demo pack ships
published), and the §05 `vendor_burndown` data populated. Kept within slice-205 demo conventions
(BYPASSRLS writer, correct `tenant_id`, `demo_seed_v` forensic mark preserved, fictional content).

**Slice 673 linkage: FIXED-BY-662.** Slice 673 is "`GET /api/board-packs` 500 in the seeded tenant
— the demo seed writes `board_packs` rows the list handler chokes on." That is the same malformed
`sections` array this slice fixes. Verified against a real Postgres (migrations applied, seed run):

- `TestApply_BoardPackSectionShape` (new) seeds the demo pack and asserts its `content` deserializes
  into `board.Pack` with all 8 sections + non-empty titles — the exact `storedPackFromRow` operation
  the list endpoint performs. **Green.**
- The `internal/board` board-pack integration suite (`go test -tags=integration -run TestPack`)
  exercises the real `List`/`GetByID` path. **Green.**
  The orchestrator may close **673 as fixed-by-662**. (If a residual 500 cause survives in 673's
  tenant that is NOT this seed shape, it would need its own slice — none was found; the seed array
  shape fully accounts for the reported 500.)

## Tests

- **vitest** — `web/lib/api/board.test.ts` (12 tests): every fixed key maps to a human label;
  `sectionLabel` returns the human label for a MISSING and an EMPTY-title `vendor_burndown` (never
  the raw key); the publish-blocker label logic yields "Vendor risk burndown" (not `vendor_burndown`)
  for a missing/empty-title unapproved §05; no blocker is ever a raw snake_case key.
- **Playwright** — `web/e2e/board-pack-vendor-burndown.spec.ts` (2 tests, route-mocked hermetic per
  the slice-594 lesson): a complete 8-section pack renders §05 with its title + visual and contiguous
  numbering (no §04→§06 jump, no raw key on the page); a pack missing §05 renders the graceful
  "not generated" state and the blocker shows the human label, never the raw key.
- **Go integration** — `TestApply_BoardPackSectionShape` (new, `internal/demoseed/integration_test.go`):
  the seeded board pack deserializes into `board.Pack` with all 8 SectionKeys + titles + populated
  vendor_burndown data (the 673 cross-check). Verified green against a real Postgres locally.

## Coverage

- `web/lib/api/board.ts` was in the omitted-zero set (no per-file floor); this slice adds the first
  direct test (`board.test.ts`) exercising `sectionLabel` + `SECTION_TITLES`. No floor was lifted
  (the ratchet is monotonic — a previously-omitted file gaining coverage does not regress any floor;
  promoting it into the threshold map is a follow-up bookkeeping step, not a gate this PR trips).
  Full vitest suite: 1439/1439 pass with the per-file ratchet enforced.
- `internal/demoseed` carries an integration tier (no Go unit-coverage floor change); the new
  assertion enrols in the existing `internal/demoseed/...` integration job.

## Detection-tier classification

- `detection_tier_actual`: `manual_review` (the 2026-06-10 empty-/demo-tenant UI audit, ATLAS-005 /
  ATLAS-025).
- `detection_tier_target`: `integration` — the malformed seed shape is a data bug a board-pack
  integration assertion (now `TestApply_BoardPackSectionShape`) should have caught; the FE blank-§05
  / raw-key gaps are `playwright`-tier (now covered by the new spec). The seed wrote an array that
  never deserialized, but no test asserted the seeded pack was listable until this slice — an
  integration-enrolment gap (Q-7) for the demo-seed board-pack shape, now closed.

## Constraints honored

- No proto / migration / schema-registry change (`git diff --stat origin/main...HEAD -- proto/
migrations/ schemaregistry/` empty).
- Invariant #6 (RLS): no tenant-scoping change; the FE reads via the BFF/cookie session; the seed
  writer stays BYPASSRLS with the correct `tenant_id` (existing slice-205 pattern).
- No raw NUL / control bytes in source. No Go log sink touched in the seed change (no `%q`-log
  surface added).
