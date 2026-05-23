# Slice 204 — Per-page audit: /audits

- **Live URL audited**: https://atlas-edge.home.gmoney.sh/audits
- **Mockup**: Plans/mockups/audits.html
- **Audit date**: 2026-05-23
- **Auditor**: slice 204 fleet agent (audits-page)
- **Live page status**: 200
- **Findings count**: 5

## Approach

Cross-checked the mockup file against the live page HTML (fetched
with admin JWT via edge), then against the API the page consumes
(`GET /v1/audit-periods` — returns `{"audit_periods":[],"count":0}`
in the audited tenant), then against the slice-102 source at
`web/app/(authed)/audits/page.tsx` to disambiguate "implementation
chose to omit" from "implementation missed."

The audited tenant has zero audit periods, so the only directly
verifiable list-state was empty-state. Mockup-vs-live data-claim
findings are derived from comparing the mockup's hard-coded six
periods against the live empty render plus the source comments that
explain intentional omissions.

## Findings

| #   | Category                                    | Severity guess | Spillover slice | Brief                                                                                                                          |
| --- | ------------------------------------------- | -------------- | --------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| 1   | (i) layout-parity                           | medium         | #213            | Header chrome missing: breadcrumb, in-progress-audit pill, global search box, user avatar                                      |
| 2   | (i) layout-parity                           | low            | #214            | Sidebar item-count badges missing (Controls "82", Risks "3" in rose)                                                           |
| 3   | (iii) data-bound                            | low            | #215            | Title row missing per-status tally ("1 in progress · 4 frozen · 1 closed")                                                     |
| 4   | (iv) mockup-stale                           | medium         | #216            | Mockup shows `Sample size` column but slice-102 intentionally omits — needs resolution (drop from mockup OR extend periodWire) |
| 5   | (iv) mockup-stale + (ii) broken-interaction | medium         | #217            | "Export OSCAL bundle" button permanently disabled on live; mockup shows it as a primary affordance                             |

## Detailed findings

### Finding 1 — #213 (layout-parity, medium)

The mockup header (`Plans/mockups/audits.html` lines 23-53) carries
four elements absent from the live shared-shell header:

- Breadcrumb (`Sentinel Labs › Audits` at lines 32-36)
- Amber in-progress audit pill (`SOC 2 Type II · Q2 2026 in progress` at lines 38-41)
- Global ⌘K search box with placeholder `Search controls, evidence, risks…` (lines 42-46)
- User avatar + display name (`MG` initial circle + `Sam` text at lines 47-50)

Live header (verified in fetched HTML) shows only logo + product
name + `v0 · self-host` tag + `Sign out` form. None of the four
mockup elements render. Selector references for reproduction: the
live shell uses `header.flex.h-14.shrink-0.items-center.justify-between`;
the mockup uses `header.sticky.top-0.z-20.bg-white`.

Scope is global (the chrome is shared via authed layout), but the
in-progress-audit pill is audits-context-specific, which is why the
divergence is most acute on this page.

### Finding 2 — #214 (layout-parity, low)

Mockup sidebar (lines 63-76) renders right-aligned count badges:

- `Controls` row: mono `82` badge
- `Risks` row: mono `3` badge in rose color

Live sidebar (verified in fetched HTML) renders the same nine
links but with no count badges, no per-item metadata, all in
muted-foreground. Selector references: mockup uses
`span.ml-auto.mono.text-[10px]`; live sidebar links have no inner
count `<span>`.

Both data points (controls count, open-critical risks count) are
derivable from existing list endpoints (no new platform API
needed).

### Finding 3 — #215 (data-bound, low)

The mockup title row (lines 107-122) renders the H1 plus an inline
status tally:

```
<h1>Audit periods</h1>
<span class="text-sm text-slate-500">
  1 in progress · 4 frozen · 1 closed
</span>
```

The live page (slice-102 source at lines 388-390 / 405-407 / 482-484)
renders the H1 plus a one-line subtitle ("Period-level index — open
a period for the per-control walk-through") but no status tally.
With the audited tenant carrying zero periods today the absence is
invisible — but with realistic data the absence is a real navigational
loss.

The tally is derivable from the already-fetched periods list (no
new query).

### Finding 4 — #216 (mockup-stale, medium)

Mockup table renders a `Sample size` column (header at line 169,
six TD cells at lines 180, 189, 198, 207, 216, 225 with values like
`1,847 records` / `2,104 records`).

Live page (slice-102 source line 228+, the `columns` array) does
NOT include a sample-size column. The slice-102 author's comment at
file lines 43-46 is explicit about the intentional omission:

> "P0-A4: NO invented columns — every column is derived from
> periodWire (name, framework_version_id, period_start, period_end,
> status, frozen_at, frozen_by, created_by). Mockup shows a 'Sample
> size' column but periodWire does NOT carry it — we OMIT the
> column rather than invent."

This needs maintainer JUDGMENT resolution: either drop the column
from the mockup OR extend periodWire + the database row's
denormalized rollup to carry the count. Slice 216 documents both
paths.

### Finding 5 — #217 (mockup-stale + broken-interaction, medium)

Mockup renders an "Export OSCAL bundle" button as a primary
action-area affordance (line 116, no disabled attribute).

Live page (`web/app/(authed)/audits/page.tsx` line 357) renders
the same button as PERMANENTLY disabled, no conditional logic, no
tooltip, no future-tense copy:

```tsx
<Button variant="outline" size="sm" disabled>
  Export OSCAL bundle
</Button>
```

Adjacent buttons (CSV / JSON / XLSX export of audit periods data
via slice 139; CSV / JSON / XLSX of samples via slice 138) are
fully working. The permanently-disabled OSCAL button alongside
working sibling buttons is a clean instance of the slice-178
HONESTY-GAP class.

Slice 217 proposes path A (label-honesty disclosure replacing the
dead button) as the default — the per-period detail view (slice
184 follow-on) is the right home for the per-period OSCAL export,
so the list-page button was a mockup-stage layout choice that
doesn't survive contact with the per-period detail design.
