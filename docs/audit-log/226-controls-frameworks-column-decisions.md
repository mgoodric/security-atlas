# 226 — controls list Frameworks-per-row column · decisions log

Slice 226 is a `JUDGMENT` slice. Per the slice-development workflow
(`Plans/prompts/04-per-slice-template.md` "Slice types"), the subjective
build-time calls are made by Claude with best-reasoned pattern-matched
judgment and recorded here; the maintainer iterates post-deployment from
the "Revisit once in use" list. This log does NOT block merge.

The product-runtime AI-assist boundary is unchanged — slice 226 is a
read-only UI surface (no AI inference, no audit-binding artifact).

---

## Decisions made

### D1 · Display-abbreviation authority lives at `internal/catalog/framework_codes.go`

**Options considered.**

| Option                         | Where the slug→display map lives                                                                                              | Trade-off                                                                                                                                                                           |
| ------------------------------ | ----------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A · backend authority (chosen) | Go file `internal/catalog/framework_codes.go`; wire ships display abbreviations (`SOC2`, `ISO`, …); frontend renders verbatim | Single source of truth; frontend stays a pure renderer (P0-226-2); the map is co-located with where new frameworks are imported, so adding a framework + its abbreviation is one PR |
| B · frontend authority         | Map in `web/app/(authed)/controls/page.tsx` keyed off the FRAMEWORK_OPTIONS pill values                                       | Direct conflict with P0-226-2 ("Does NOT hardcode the framework short-code map in the frontend"); rejected                                                                          |
| C · DB column                  | New `display_code TEXT` on the `frameworks` table                                                                             | Heavier than the problem warrants; would require a migration + a backfill + an import-tool change for every framework; the map is small (6 entries today) and rarely-changing       |

**Chosen: A.** The constitutional positioning is in the file comment —
slug (stable identifier) → display abbreviation (rendering concern).
Unknown slugs fall back to the upper-cased slug verbatim (catalog-drift
transparency).

**Rationale.** P0-226-2 dictates A directly. Option C is reserved as the
escape hatch if the abbreviation set ever needs per-tenant overrides
(which is not on any roadmap today — abbreviations are global catalog
concerns).

**Confidence:** `high`.

### D2 · Single CTE with array_agg vs application-side join

**Options considered.**

| Option                                    | Shape                                                                                               | Cost                                                                                                                                                                      |
| ----------------------------------------- | --------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A · separate CTE in the same SQL (chosen) | `anchor_frameworks CTE → array_agg(DISTINCT slug) GROUP BY anchor_id → LEFT JOIN`                   | Single round trip; the planner inlines the CTE; ~1,400 anchors × ~5 framework rows ≈ 7K joined rows, well under any latency budget                                        |
| B · correlated subquery per outer row     | `(SELECT array_agg(...) FROM ... WHERE scf_anchor_id = a.id) AS slugs`                              | Per-row execution (technically still one SQL statement); the planner usually rewrites this to a hash-aggregate, but the explicit CTE makes the intent obvious to a reader |
| C · application-side join after fetch     | Fetch anchors + state in one query; fetch `(anchor_id, slug)` pairs in a sidecar query; merge in Go | Two round trips; harder to keep RLS context straight; rejected as needless complexity                                                                                     |

**Chosen: A.** Honors AC-3 directly ("single SQL statement … no per-row
fan-out") and matches the existing slice-104 worst_per_anchor CTE
pattern — the SQL stays a one-statement, two-CTE, one-LEFT-JOIN shape.

**Rationale.** The slice 098 (`Plans/canvas/12-ui-fill-in-design-
decisions.md`) precedent is "1,400 queries vs 1 query" — keep it 1.
The CTE makes the intent surface in the SQL itself rather than relying
on planner heuristics.

**Confidence:** `high`.

### D3 · Empty framework set renders `—`, not empty string

**Options considered.**

| Option                        | UI                             | Reasoning                                                                                                                                          |
| ----------------------------- | ------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| A · em-dash (chosen)          | `—` in `text-muted-foreground` | Mirrors the State / Freshness / Last-observed empty placeholders on the same page; visually consistent; no ambiguity with the populated chip strip |
| B · empty string / blank cell | (nothing rendered)             | Ambiguous — reader can't tell if data is missing or genuinely empty                                                                                |
| C · "No frameworks" label     | `<span>No frameworks</span>`   | Verbose; out of character with the page's information density                                                                                      |

**Chosen: A** per AC-6 + page convention.

**Confidence:** `high`.

### D4 · Wire ships `frameworks: []` (empty array) instead of `null` / omitted

**Options considered.**

| Option                            | Wire shape                                                  | Frontend impact                                                                                           |
| --------------------------------- | ----------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| A · always-present array (chosen) | `"frameworks":[]` for empty, `["SOC2","ISO"]` for populated | Type narrowing is trivial — field is always `string[]`; AC-6 dash fallback keys on `.length === 0`        |
| B · nullable                      | `"frameworks": null` for empty                              | Adds a null branch to the type; the dash branch keys on `null \|\| empty` — two predicates instead of one |
| C · omitted on empty              | (no `frameworks` key at all)                                | Drops the type into `string[] \| undefined`; more branches in the renderer                                |

**Chosen: A.** The wire layer (`frameworksWire` in
`internal/api/anchors/handlers.go`) coerces nil/empty input from sqlc to
a non-nil empty slice. Frontend `AnchorWithState.frameworks: string[]`
is non-optional.

**Confidence:** `high`.

### D5 · Slice-226 leaves the Framework filter pill as a no-op

**Options considered.**

| Option                  | Behavior                                                 | Scope                                                                                                            |
| ----------------------- | -------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| A · keep no-op (chosen) | Pill renders, selecting a framework does not narrow rows | In scope: slice 226 ACs are column-only                                                                          |
| B · wire the filter     | Pill narrows visible rows on slug match                  | Out of scope; needs slug-vs-display reconciliation on the row (the row carries display abbreviations, not slugs) |

**Chosen: A.** The slice 226 spec is explicit: "A seventh column lands".
Wiring the filter is a natural extension but requires either (1) shipping
slugs alongside display abbreviations on the wire, or (2) introducing a
display→slug reverse map in the frontend (which P0-226-2 forbids). Both
expand the slice's scope. Spillover slice should wire the filter when
the spec adds it.

**Rationale.** The existing `filters.test.ts` already pins
"framework filter is a no-op for v1" — slice 226 maintains that contract.

**Confidence:** `medium`. The pill rendering but not working is a minor
UX wart; an immediate follow-on is warranted.

### D6 · Exclude `no_relationship` edges + the SCF spine itself from the framework set

**Options considered.**

| Option                                         | What appears in the set                                                     | Reasoning                                                                                                                                                                                  |
| ---------------------------------------------- | --------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| A · exclude `no_relationship` + `scf` (chosen) | The frameworks an anchor SATISFIES (via `equal`, `subset_of`, `intersects`) | Matches user intent — column header reads "Frameworks", which the reader takes to mean "which audit reports does this anchor contribute to?"                                               |
| B · include `no_relationship`                  | Frameworks explicitly mapped as non-satisfaction                            | Useful for the slice-174 catalog export (already includes them) but noise on the controls list — users browsing the list want satisfaction, not "this anchor explicitly does NOT map to X" |
| C · include `scf`                              | Every anchor lists `SCF` first                                              | Tautological — every SCF anchor is in the SCF framework by construction; adds noise to every row                                                                                           |

**Chosen: A.** The `e.relationship_type <> 'no_relationship'` and
`af.slug <> 'scf'` predicates are both inside the
`anchor_frameworks` CTE in `internal/db/queries/scf_anchors.sql`.

**Confidence:** `high`.

### D7 · `array_agg(DISTINCT slug)::text[]` cast for sqlc type stability

**Context.** sqlc v1.31.1 emits `interface{}` for unannotated array
aggregates (the planner cannot infer the element type from `array_agg`
alone when the aggregate is over an enum-free text column inside a CTE).
The `::text[]` cast on the outer SELECT pins the type so sqlc emits
`[]string`.

**Pattern match.** The slice-104 `wpa.last_observed_at::timestamptz`
cast does the same thing for the same reason — sqlc type inference
across LEFT JOIN + CTE chains needs an explicit nudge.

**Confidence:** `high`. Pattern-matched to an established slice.

---

## CI-delta scan (D8)

Touched files and the CI surfaces they affect:

| File                                             | Surface                                                                                         |
| ------------------------------------------------ | ----------------------------------------------------------------------------------------------- |
| `internal/db/queries/scf_anchors.sql`            | `Go · build + test` (sqlc regen)                                                                |
| `internal/db/dbx/scf_anchors.sql.go`             | regenerated; tracked under sqlc-version pin (slice 109)                                         |
| `internal/db/dbx/querier.go`                     | regenerated; same pin                                                                           |
| `internal/catalog/framework_codes.go`            | `Go · build + test` + golangci-lint                                                             |
| `internal/catalog/framework_codes_test.go`       | `Go · build + test` (Go unit) + Codecov                                                         |
| `internal/api/anchors/handlers.go`               | `Go · build + test` + golangci-lint + `Go · integration (Postgres RLS)`                         |
| `internal/api/anchors/state_unit_test.go`        | `Go · build + test`                                                                             |
| `internal/api/anchors/state_integration_test.go` | `Go · integration (Postgres RLS)`                                                               |
| `web/lib/api.ts`                                 | `Frontend · vitest` (type narrowing) + `Frontend · Playwright e2e` (consumed by /controls page) |
| `web/app/(authed)/controls/filters.ts`           | `Frontend · vitest`                                                                             |
| `web/app/(authed)/controls/filters.test.ts`      | `Frontend · vitest` (Codecov)                                                                   |
| `web/app/(authed)/controls/page.tsx`             | `Frontend · Playwright e2e` (assertions still quarantined behind seed harness)                  |
| `web/app/api/controls/route.test.ts`             | `Frontend · vitest`                                                                             |
| `web/e2e/controls-list.spec.ts`                  | quarantined; runs but assertions are commented                                                  |

Verified locally:

- `go build ./...` clean
- `go test ./internal/catalog/ ./internal/api/anchors/` — both `ok`
- `go vet ./...` clean
- `golangci-lint run ./internal/catalog/... ./internal/api/anchors/...` — 0 issues
- `npm run test` (web) — 779 tests pass
- `npx tsc --noEmit` for touched files — clean (pre-existing TS errors in unrelated files only)

No new CI scanner jobs needed. The sqlc regen is committed alongside
the SQL change (per slice 109's pin). No migration ships in this slice
(the schema was sufficient — `fw_to_scf_edges` already exists).

---

## Revisit once in use

Concrete items the maintainer should re-evaluate once the product runs
against real catalog data + real users.

1. **Wire the Framework filter pill (D5 spillover).** Today the pill
   renders but selecting an option doesn't narrow the visible rows. The
   most natural fix: add `framework_slugs: string[]` (raw slugs)
   alongside the display abbreviations on the `AnchorWithState` wire,
   then filter `applyFilters` on substring match. Or — extend the SQL
   to accept an optional `?framework=<slug>` predicate and narrow the
   row set server-side (mirrors the slice 224 scope-cell precedent).
   File as a follow-on slice with the SQL extension preferred.

2. **Framework column on `/controls/[id]` detail page.** The detail
   page already has a "Coverage" section (slice 008) listing
   framework requirements per anchor. The list-page column duplicates
   information that's available a click away — check with users
   whether the column adds enough signal to justify the column width,
   or whether a "3 frameworks" chip with hover popover would carry the
   same information more densely.

3. **Display abbreviations review.** The six v1 abbreviations
   (`SOC2`, `ISO`, `CSF`, `PCI`, `HIPAA`, `GDPR`) match the mockup. If
   the user community proposes alternatives (e.g. `ISO27K` for
   ISO 27001 to disambiguate from ISO 9001 if/when that's imported),
   add the alternatives to `framework_codes.go` — that file is the
   authority.

4. **Empty-set framework anchors in the SCF catalog.** Once the catalog
   is imported in production and we observe how many SCF anchors have
   NO satisfaction edges (anchors in families like `MON`, `MNT`,
   `OPS` that may not have shipped crosswalks yet), revisit whether
   `—` is the right placeholder or if a more informative "no
   crosswalks yet" tooltip is warranted.

5. **Sort order of frameworks within a row.** Today the backend sorts
   by display abbreviation alphabetically (`CSF · GDPR · ISO · SOC2`).
   The mockup shows `SOC2 · ISO · CSF · GDPR` — a popularity / audit-
   frequency ordering, NOT alphabetical. The alphabetical sort was
   chosen for determinism; reconsider once we see how the column
   reads in practice against real catalog data. If we switch, the
   sort logic lives in `internal/catalog.SortedFrameworkDisplayCodes`
   (one place to change).

6. **Per-tenant framework subset.** A solo security leader doesn't
   pursue all six frameworks at once. Today the column shows
   every framework an anchor satisfies, globally. A v2 enhancement
   could narrow to "frameworks this tenant is actively pursuing"
   (driven by `framework_versions` rows the tenant has installed via
   the audit module). Track via the v2 backlog.

---

## Confidence summary

| Decision                           | Confidence |
| ---------------------------------- | ---------- |
| D1 — display map authority         | high       |
| D2 — single CTE                    | high       |
| D3 — em-dash placeholder           | high       |
| D4 — `[]` not `null`               | high       |
| D5 — filter pill stays no-op       | medium     |
| D6 — exclude no_relationship + scf | high       |
| D7 — `::text[]` cast               | high       |

`medium` confidence items go to the top of the revisit list (D5 → item 1).
