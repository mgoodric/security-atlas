# 244 — Risks list: extend filter pills to Category, Methodology, Org unit

**Cluster:** Frontend
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 204 audit fleet (`/risks` page), captured as a
follow-up per continuous-batch policy. The mockup at
`Plans/mockups/risks.html` (lines 126-173) shows five filter pills on
the risk register: **Category** (Operational / Compliance /
Third-party / Strategic), **Treatment** (mitigate / transfer / accept
/ avoid), **Methodology** (nist_800_30 / fair / five_by_five),
**Org unit** (Platform / Customer success / Corporate IT), and
**Owner**.

The live `/risks` page ships three: Treatment, Severity, Owner. The
Severity pill is a net addition not present in the mockup — that's a
positive and should stay. The gap is the three missing pills.

Slice 100's own filter module documents the gap:

> `web/app/(authed)/risks/filters.ts` lines 11-14: "Filter set per
> AC-3: treatment + severity band + owner. The mockup shows
> category/methodology/org_unit pills too, but the AC narrows the
> shipped pill set to three. Adding the rest is a future extension;
> the data is on `riskWire` already so the cost is purely UI."

This slice is that future extension. The `Risk` type at
`web/lib/api.ts` lines 1952-1973 carries `category: string`,
`methodology: string`, `org_unit_id?: string` on the wire — so the
work is purely client-side:

1. Extend `RiskFilters` type in `filters.ts` to add the three keys.
2. Add three pill definitions to the `pills` array in `page.tsx`.
3. Add three corresponding entries to `applyFilters`.
4. Extend `FILTER_KEYS` / `clearAll` plumbing.

The Methodology pill is load-bearing per canvas §6 (risk methodology
default is an open question — surfacing the choice in the filter
set is how an operator slices a multi-methodology register). The
Category pill is the natural top-level cut. The Org unit pill is
how a security leader scopes to a team or BU; it joins on `OrgUnit`
which is already populated by slice 053.

## Threat model

**Verdict.** **no-mitigations-needed.** Pure UI extension over data
that already flows through the existing RLS-enforced
`GET /v1/risks` path. No new authz surface, no new write path.

## Acceptance criteria

- **AC-1.** `RiskFilters` type extended with `category: string`,
  `methodology: string`, `org_unit: string` (URL-friendly key naming
  matches `treatment`/`severity`/`owner`).
- **AC-2.** Three new pill definitions in `page.tsx` `pills` array.
  Category options: `All categories` + the four mockup values.
  Methodology options: `All methodologies` + `nist_800_30` + `fair`
  - `five_by_five`. Org unit options: `All units` + the unique
    `org_unit_id` set derived from the loaded rows (same pattern as
    `ownerOptions`).
- **AC-3.** `applyFilters` in `filters.ts` extended with three new
  branches: exact-string match on `category`, exact-string match on
  `methodology`, exact-string match on `org_unit_id`.
- **AC-4.** Org unit filter renders the org unit's `name` (not the
  `id`) in the pill options. The page fetches `GET /v1/org_units`
  (slice 053) once on mount and joins client-side.
- **AC-5.** URL query string round-trips all six filter keys
  (`?category=Operational&methodology=fair&org_unit=<id>` etc.).
  Refresh restores the active filter set.
- **AC-6.** vitest coverage in `filters.test.ts` extends the
  existing `applyFilters` table-driven tests with cases for each new
  filter key (positive match + negative match + ALL-passthrough).
- **AC-7.** Existing Playwright spec `risks-list.spec.ts` is
  extended with one assertion per new pill (pill is visible and
  reads the expected default label).
- **AC-8.** CHANGELOG entry: "Risks list: extend filter pills to
  Category, Methodology, Org unit (#244; slice 100 follow-on)".

## Constitutional invariants honored

- **Mockup-truth.** This slice closes a documented gap between the
  shipped UI and the mockup. The live page's Severity pill (not in
  the mockup) is retained — it is a meaningful addition.
- **Data already on the wire.** No backend changes; no new RLS
  surface. The `Risk` type carries all three fields today.

## Canvas references

- `Plans/canvas/06-risk.md` — risk register linkage, methodology
- `Plans/canvas/05-scopes.md` — org-unit dimension

## Dependencies

- **#019** Risk CRUD backend — `merged`. Carries category +
  methodology + org_unit_id on the wire.
- **#053** Org unit schema — `merged`. Provides
  `GET /v1/org_units`.
- **#100** Risks list view — `merged`. The page this slice extends.

## Anti-criteria (P0 — block merge)

- **P0-244-1.** Does NOT remove the Severity pill. Severity is a
  net positive over the mockup; it stays.
- **P0-244-2.** Does NOT add a sixth pill not in the mockup or in
  this slice's scope. Six pills total: the existing three + the
  three new.
- **P0-244-3.** Does NOT introduce a new BFF route. The data is on
  the existing `GET /v1/risks` + `GET /v1/org_units` payloads.
- **P0-244-4.** Does NOT extend `applyFilters` with a
  case-insensitive match. The wire values are normalized strings;
  exact match keeps the URL query string stable.

## Skill mix (3-5)

1. Next.js App Router — page filter extension
2. shadcn/ui Popover/Select primitives
3. vitest table-driven tests
4. Playwright spec extension
