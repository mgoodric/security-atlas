# Slice 204 — Per-page UI parity audit: `/evidence`

**Parent slice:** #204 (comprehensive page-by-page UI parity audit fleet)
**Audited:** 2026-05-23
**Auditor:** parallel-batch audit agent (4 of 11 in fleet)

## Surfaces

| Surface          | Value                                                                                                       |
| ---------------- | ----------------------------------------------------------------------------------------------------------- |
| Live URL         | `https://atlas-edge.home.gmoney.sh/evidence`                                                                |
| Mockup HTML      | `Plans/mockups/evidence.html`                                                                               |
| Live page source | `web/app/(authed)/evidence/page.tsx`                                                                        |
| BFF route        | `web/app/api/evidence/route.ts`                                                                             |
| Upstream         | `/v1/evidence` (returns `{control_id, count, evidence, next_cursor}`)                                       |
| Screenshot       | not captured (dev-seed dataset on remote atlas-edge returned zero records; DOM-level inspection sufficient) |

The audit ran against the deployed `atlas-edge.home.gmoney.sh`
build (admin JWT seed dataset). The tenant's evidence ledger
returned zero records (`/v1/evidence` → `{"count":0,"evidence":[]}`),
so the audit focused on the four parity axes against rendered
chrome + filter row + empty-state, not against table-row
content.

## Findings table

| #         | Category                                | Severity (guess) | Spillover slice                                                                        | Brief                                                                                             |
| --------- | --------------------------------------- | ---------------- | -------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| F-204-E-1 | (ii) broken interaction                 | medium           | [#233](../issues/233-ui-honesty-evidence-push-button-disabled.md)                      | `Push evidence` CTA permanently `disabled`, no signposting to CLI/SDK path                        |
| F-204-E-2 | (i) layout-parity + (iv) mockup-stale   | medium           | [#234](../issues/234-ui-honesty-evidence-filter-row-missing-three-pills.md)            | Filter row missing three pills (Source, Scope, Since) — backend already supports two of them      |
| F-204-E-3 | (i) layout-parity (cross-cutting shell) | high             | [#235](../issues/235-ui-honesty-evidence-header-chrome-missing-audit-banner-search.md) | Shell header missing audit-period banner pill + tenant breadcrumb + `⌘K` global search            |
| F-204-E-4 | (iii) data-bound honesty                | low              | [#236](../issues/236-ui-honesty-evidence-ledger-total-record-count.md)                 | Record-count meta `Showing 0 records` cannot distinguish "ledger empty" from "filters too narrow" |
| F-204-E-5 | (ii) broken interaction                 | medium           | [#237](../issues/237-ui-honesty-evidence-pagination-footer-missing.md)                 | Backend returns `next_cursor` but UI has no pagination footer — silent truncation                 |

Severity guesses are preliminary; the maintainer triages
post-merge per slice 204 AC-5.

## Detailed findings

### F-204-E-1 — `Push evidence` CTA permanently disabled

**Category.** (ii) Broken interaction.

**Mockup claim** (`Plans/mockups/evidence.html` lines 117-121).
A live brand-primary button styled `bg-brand-600 hover:bg-brand-700`
with an upload-arrow icon and the label `Push evidence`. The
mockup implies the click opens an inline push affordance.

**Live state** (`web/app/(authed)/evidence/page.tsx` lines 333-335).
The button is rendered with `disabled` as a literal prop. No
hover text, no tooltip, no link to the CLI quickstart, no link
to `/admin/credentials`. The button is dead. The mockup-vs-live
copy mismatch is total: a primary CTA on the page does nothing.

**User impact.** A solo security leader landing on `/evidence`
expecting to push their first record will hover the CTA, see it
greyed out, and abandon the path. Slice 178 HONESTY-GAP class.

**Recommendation.** Path A (cheap): link the button to the CLI
quickstart or `/admin/credentials`. Path B (heavy): ship the
inline push dialog. Slice #233 defaults to Path A.

### F-204-E-2 — Filter row missing three pills

**Category.** Layout-parity (i) + Mockup-stale (iv) hybrid.

**Mockup claim** (`Plans/mockups/evidence.html` lines 125-184).
Six filter pills above the table: Control, Kind, Result, Source,
Scope, Since.

**Live state** (`web/app/(authed)/evidence/page.tsx` lines
198-217). Only the first three pills (Control, Kind, Result)
are rendered.

**Backend reality.** The `/v1/evidence` handler (slice 106)
already accepts `source_actor_type`, `source_actor_id`, and
`observed_after` query params — so two of the three missing
pills are blocked only by frontend wiring, not backend gap.
Scope filtering needs a new SQL predicate but the data is
present.

**User impact.** v1 user running their first SOC 2 cannot scope
the ledger to the active audit period via UI; will reach for
CSV export + spreadsheet — the v1 success-test failure mode.

**Recommendation.** Ship all three pills behind the existing
URL-state binding pattern (slice 098 + 106). Slice #234 details.

### F-204-E-3 — Shell header missing chrome elements

**Category.** Layout-parity (i), cross-cutting (shell-level).

**Mockup claim** (`Plans/mockups/evidence.html` lines 23-53).
Header contains: brand mark + tenant-breadcrumb (`Sentinel Labs

> Evidence`), an amber audit-period banner pill (`SOC 2 Type II
> · Q2 2026 in progress`), and a `⌘K` global search input.

**Live state.** None of the three elements exist anywhere on
the live page. The shell layout renders only brand mark + the
existing `TenantSwitcher` component (which is itself a separate
control, not a breadcrumb).

**Cross-cutting note.** This is a shell-level gap surfacing in
the per-page audit. Other audit-fleet members are expected to
surface the same finding on their pages. Slice #235 is filed at
shell scope so the per-page audits can reference it rather than
duplicate.

**Recommendation.** Path A + B (banner + breadcrumb). Defer Path
C (global search) — that is v1.5, not v1 polish. Slice #235
details.

### F-204-E-4 — Record-count meta lacks ledger-total context

**Category.** (iii) Data-bound honesty.

**Mockup claim** (`Plans/mockups/evidence.html` lines 111 + 181-
183). Page subtitle includes the ledger total
(`append-only · 14,712 records · 7 connectors`); filter-meta
shows the ratio `Showing 12 of 14,712 records`.

**Live state.** Page subtitle is abstract narrative; filter-meta
shows only `Showing N records`. On the tenant under audit (zero
records), the meta line reads `Showing 0 records` with no
distinguishing signal between "ledger is empty" and "filters
narrow the result set to zero".

**Backend reality.** `/v1/evidence` does not currently return a
tenant-wide ledger total. The fix needs a `total` field in the
response + a small `COUNT(*)` query (RLS-bound).

**User impact.** Operator confusion on first visit; cannot tell
whether to push their first record or to widen filters. The
mockup-stale `7 connectors` count is dropped from scope (a
separate read against connector inventory).

**Recommendation.** Add `total` to the wire + surface as
`Showing N of M records`. Slice #236 details.

### F-204-E-5 — Pagination footer missing; cursor unwired in UI

**Category.** (ii) Broken interaction.

**Mockup claim** (`Plans/mockups/evidence.html` lines 266-272).
Footer below the table: `Showing 1-7 of 12 · 14,712 total in
ledger` + `[Previous] [Next]` buttons.

**Live state.** No footer of any kind. The `<ListTable>` is
mounted without a `pagination` prop or sibling footer
(`web/app/(authed)/evidence/page.tsx` lines 476-483).

**Backend reality.** `/v1/evidence` returns `next_cursor` as
a top-level field (visible in the API probe). The
`EvidenceListResponse` type in `web/lib/api.ts` already exposes
it. The UI silently truncates after the default limit.

**User impact.** Operators with >50 evidence records see only
the first page. Cursor data is present end-to-end through the
wire shape; the missing piece is a 1-component UI footer (the
pattern is already in use on `/controls` per slice 098).

**Recommendation.** Reuse the `/controls` pagination component.
Slice #237 details.

## Out-of-scope observations

- **Loading skeleton** matches the mockup well (mockup lines
  277-302 vs live skeleton). No finding.
- **Empty-state component** mostly matches the mockup (mockup
  lines 308-318 vs live `noRecordsEmptyState` lines 343-380).
  Live includes the icon, title, body text, and both CTAs. No
  finding.
- **Export buttons (CSV / JSON / XLSX)** — present in the live
  page (slice 138) but absent from the mockup. This is a real
  improvement over the mockup, NOT a parity gap. No finding.
- **`v1.14.0 deployment 500-error class`** — `/evidence` rendered
  on the remote atlas-edge build without 500s; this audit did
  not encounter the deployment-config gap referenced by slice
  204's operational blocker section. The audit ran clean against
  the live build at audit time.

## Anti-criteria honored

- **P0-A1 (no inline fixes).** Every finding files as its own
  spillover slice (#233-#237).
- **P0-A2 (no production-code touches).** Audit modified only
  `docs/audit-log/204-page-audit-evidence.md` and
  `docs/issues/2{33..37}-*.md`.
- **P0-A5 (no Bearer tokens / cookie values / real-tenant
  screenshots).** The audit's JWT was sourced from
  `/tmp/atlas-edge-admin-jwt` and never committed; no DOM dump
  or screenshot is included.
- **P0-A7 (one finding = one slice).** Five findings, five
  spillover slices.
- **Slice number range 233-237 — all within the 233-237 ceiling.**

## Provenance

- Spawned by slice 204's parallel-batch audit dispatcher
- 5 findings, 5 spillover slices, finding density: **moderate**
- Time on task: ~30 min wall-clock (under the slice 204 budget)
