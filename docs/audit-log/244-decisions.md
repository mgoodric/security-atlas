# 244 — Risks list: extend filter pills · decisions log

**Slice:** `docs/issues/244-risks-list-extend-filter-pills.md`
**Branch:** `frontend/244-risks-filter-pills`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-23

This slice extends the `/risks` filter pill row from 3 pills (Treatment + Severity + Owner) to 6 (adding Category + Methodology + Org unit) per the `Plans/mockups/risks.html` mockup. Three judgment calls landed inside the build that are worth capturing — one for each new pill — plus two operational choices (BFF reuse and pill ordering).

---

## Decisions made

### D1 — Category pill options bind to the wire enum, not the mockup labels

**Decision:** **Surface the seven `risk_category` enum values verbatim** (`confidentiality | integrity | availability | privacy | regulatory | operational | financial`) as the Category pill option set. The mockup's four labels (Operational / Compliance / Third-party / Strategic) are non-canonical and were not adopted.

**Options considered:**

| Option                                                                                                                             | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| ---------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Surface the seven wire-enum values verbatim** — _chosen_                                                                     | The acceptance criteria explicitly say "exact-string match on `category`" (AC-3) against the wire. The only strings that can match the wire are the wire values. Anti-criterion P0-244-4 forbids case-insensitive match, which would otherwise have been the obvious bridge between the mockup labels and the wire enum.                                                                                                                                                                                                           |
| (b) **Use the mockup's four labels as pill VALUES**                                                                                | The pill values feed an exact-string filter against `row.category`. A pill value of "Third-party" would never match any wire row (no risk in any tenant carries `category = "Third-party"` — the wire enum has no such variant). The filter would be silently broken. Rejected.                                                                                                                                                                                                                                                    |
| (c) **Use the mockup labels as display labels with the wire values as keys** (e.g., `value="operational"` → `label="Operational"`) | This would require a four-to-seven mapping that does not exist. `operational` maps to "Operational" cleanly, but the other six wire values (`confidentiality`, `integrity`, `availability`, `privacy`, `regulatory`, `financial`) have no mockup equivalent, and the four mockup labels (`Compliance`, `Third-party`, `Strategic`, and `Operational`) only partially overlap. Forcing a mapping would either hide six real wire values from the filter (option fragility) or invent labels for them (option dishonesty). Rejected. |

**Rationale.** The mockup at `Plans/mockups/risks.html` lines 130-135 uses a risk-tier-style category set (operational/compliance/third-party/strategic). The wire schema at `migrations/sql/20260511000000_init.sql` (slice 002 / slice 019) uses a CIA-Privacy-axis category set (confidentiality/integrity/availability/privacy/regulatory/operational/financial). The two taxonomies overlap only at "operational". The conservative path is to bind the pill to the wire (the source of truth at runtime) and treat the mockup discrepancy as a separate decision (mockup walk-back or wire-schema extension) outside the scope of this slice.

**Confidence:** **high.** The exact-string filter contract makes any other choice silently broken.

**Follow-up:** the Category-label mismatch between the mockup and the wire enum is a real walk-back candidate for a future mockup-truth slice — either change the mockup to surface the wire enum or extend the wire enum to include the mockup taxonomy. The choice is a product decision, not an engineering call, and is not in this slice's scope.

### D2 — Methodology pill exposes all five wire-enum values

**Decision:** **Surface all five `risk_methodology` enum values** (`nist_800_30 | fair | cis_ram | iso_27005 | qualitative_5x5`) — superset of the mockup's three.

**Options considered:**

| Option                                                                                                            | Why rejected / why chosen                                                                                                                                                                                                                                                   |
| ----------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **All five wire-enum values** — _chosen_                                                                      | Same source-of-truth argument as D1. The wire enum is the authoritative range; the pill should expose it.                                                                                                                                                                   |
| (b) **Mockup's three values verbatim** (the mockup at line 154 carries `nist_800_30`, `fair`, and `five_by_five`) | `five_by_five` does not exist on the wire — the wire value for that concept is `qualitative_5x5`. Using the mockup string would silently break the filter for any risk in that bucket. Rejected.                                                                            |
| (c) **Mockup's three with `qualitative_5x5` substituted for `five_by_five`**                                      | Hides two real wire values (`cis_ram`, `iso_27005`) from the user. A risk register configured with `cis_ram` would have no way to filter to it. Rejected — the slice is a "purely UI" extension over data already on the wire, so the UI should expose the full data range. |

**Rationale.** The wire-enum-is-authoritative reasoning from D1 applies, plus a pragmatic observation: the methodology enum is small (5 values) and stable, so exposing the full set has zero discoverability cost.

**Confidence:** **high.**

**Follow-up:** the mockup at `Plans/mockups/risks.html` line 154 carries `five_by_five` as a string. That's a mockup typo; should be corrected to `qualitative_5x5` in a follow-up mockup walk-back. Out of scope here.

### D3 — Org unit options derived from loaded rows' `org_unit_id` set, no "unassigned" bucket

**Decision:** **Build the Org unit pill option set from the unique `org_unit_id` values present on the loaded rows**, joined client-side to `OrgUnit.name` via the existing `fetchHierarchyOrgUnits()` fetcher. Rows with no `org_unit_id` are skipped entirely — no "unassigned" or "—" option is added to the pill.

**Options considered:**

| Option                                                                                   | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                             |
| ---------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Derive options from `org_unit_id` set on loaded rows; skip unassigned** — _chosen_ | Matches the AC-2 pattern verbatim ("same pattern as `ownerOptions`"). Avoids a sentinel value that the wire doesn't carry — `org_unit_id` is an optional UUID column; the wire has no string sentinel like `treatment_owner = ""`.                                                                                                                                                    |
| (b) **Add an "unassigned" pill option that matches rows with no `org_unit_id`**          | The spec doesn't ask for this. Owner has an "unassigned" sentinel because the wire shape (`treatment_owner: ""`) genuinely needs one — the field is a non-null TEXT. Org unit is a nullable UUID — `undefined` is the natural absence shape. Pinning an "unassigned" UUID-shaped sentinel into the URL state would create an inconsistency with the rest of the filter set. Deferred. |
| (c) **List EVERY org unit known to the tenant** (not just ones with risks)               | The owner pill derives from the row set, not from a separate "all owners" list — for consistency the org unit pill follows the same pattern. Also avoids surfacing org units that have no risks (visually empty filter result).                                                                                                                                                       |

**Rationale.** Pattern parity with `ownerOptions` is the load-bearing call here. The filter would not be wrong with (c), but it would be inconsistent with its sibling pill — and the AC explicitly cites the owner pattern.

**Confidence:** **medium.** Option (b) is a reasonable future addition if operators ask for it; the spec just doesn't ask for it today.

### D4 — Reuse `fetchHierarchyOrgUnits()` rather than adding a new BFF route

**Decision:** **Reuse the existing `fetchHierarchyOrgUnits()` fetcher** at `web/lib/api.ts:1664` (slice 056) for the Org unit pill's name lookup. It hits the existing BFF route at `/api/risks-hierarchy/org-units` which is wired to the slice-053 `GET /v1/org_units` upstream endpoint.

**Options considered:**

| Option                                                                            | Why rejected / why chosen                                                                                                                                                                                                               |
| --------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Reuse `fetchHierarchyOrgUnits()`** — _chosen_                               | Anti-criterion P0-244-3 forbids new BFF routes. The function name carries `Hierarchy` in it (it was named for slice 056), but the upstream endpoint is the generic `/v1/org_units` list — the function is generic enough to reuse here. |
| (b) **Add a new `fetchRisksOrgUnits()` alias for naming hygiene**                 | Would add an indirection layer for purely cosmetic reasons. The existing function works; renaming it is a follow-up if it bothers anyone.                                                                                               |
| (c) **Add a new BFF route `/api/risks/org-units` that mirrors the hierarchy one** | Direct anti-criterion violation. Rejected.                                                                                                                                                                                              |

**Rationale.** P0-244-3 is unambiguous, and the existing fetcher already covers the use case. The naming nit is real but not load-bearing.

**Confidence:** **high.**

### D5 — Pill ordering preserves mockup order, with Severity inserted next to Owner

**Decision:** **Final pill ordering**: Category → Treatment → Methodology → Org unit → Severity → Owner.

**Options considered:**

| Option                                                                                                        | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                          |
| ------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| (a) **Category → Treatment → Methodology → Org unit → Severity → Owner** — _chosen_                           | Preserves the mockup's pill ordering for the four mockup pills and inserts the slice-100 Severity pill (a net positive over the mockup, retained per anti-criterion P0-244-1) directly before Owner. Severity is conceptually a risk-scoring control like Methodology, so placing it just before the people-axis pill (Owner) keeps the related controls together. |
| (b) **Treatment → Severity → Owner → Category → Methodology → Org unit** (existing-pills-first)               | Less visually consistent with the mockup. The new pills are not "second-class citizens"; they're peers. Rejected.                                                                                                                                                                                                                                                  |
| (c) **Category → Treatment → Severity → Methodology → Org unit → Owner** (severity in its slice-100 position) | The slice-100 ordering has Severity sandwiched between Treatment and Owner. Adopting (c) would scatter the risk-scoring pills (Severity + Methodology) across the row. Rejected for the same logic as (a) chose.                                                                                                                                                   |

**Confidence:** **medium.** This is a UX call with no hard right answer; (a) is the most-mockup-faithful while still keeping the slice-100 net-positive Severity pill in a natural spot.

---

## What this slice did NOT do

- Did NOT modify `CHANGELOG.md` (orchestrator anti-criterion).
- Did NOT modify `docs/issues/_STATUS.md` (orchestrator anti-criterion).
- Did NOT add a new BFF route (`P0-244-3`).
- Did NOT remove the Severity pill (`P0-244-1`).
- Did NOT add a seventh pill (`P0-244-2`).
- Did NOT modify any SQL, sqlc-generated code, or migrations (slice is purely UI plumbing over data already on the wire).
- Did NOT touch `Plans/mockups/risks.html` — the mockup discrepancies surfaced in D1/D2 are documented here as follow-up candidates rather than fixed inline.

---

## Files touched

- `web/app/(authed)/risks/filters.ts` — extend `RiskFilters` + `DEFAULT_FILTERS` + `isDefault` + `applyFilters` with 3 new keys.
- `web/app/(authed)/risks/page.tsx` — add `CATEGORY_OPTIONS` / `METHODOLOGY_OPTIONS` constants; add `orgUnitsQ` useQuery + `orgUnitOptions` memo; extend `FILTER_KEYS` and `pills` array.
- `web/app/(authed)/risks/filters.test.ts` — extend vitest coverage with positive/negative/ALL-passthrough cases per new filter key + compose-with-others.
- `web/e2e/risks-list.spec.ts` — extend Playwright spec with four new (quarantined-behind-seed-harness) assertions: per-new-pill visibility + URL round-trip.
- `docs/audit-log/244-decisions.md` — this file.
