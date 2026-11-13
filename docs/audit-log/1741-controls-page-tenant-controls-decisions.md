# Issue #1741 — Controls page lists the tenant's controls

Build-time calls made while fixing
[#1741](https://github.com/mgoodric/security-atlas/issues/1741): `/controls`
listed the first 100 SCF anchors instead of the tenant's controls, and linked
each row by anchor id, which the control detail page cannot resolve.

`detection_tier_actual = manual_review` (first-run adopter setup, 2026-11-12) ·
`detection_tier_target = playwright` (a spec seeding controls whose ids differ
from their anchor ids would have caught the link bug; the existing
`controls-list` fixture gives each control its anchor's id, which hid it).

## D1 — The default view reads a per-control endpoint, not the anchor list

**Decision.** `GET /v1/controls?include=state` returns one row per active
tenant control with that control's own worst-state-wins rollup, its anchor's
framework set, and an unpaged `total`. Without `include=state` the slice-151
flat response is unchanged, so the risk-form picker is unaffected.

**Why.** Reading `/v1/anchors?has_control=true` would have been fewer moving
parts, but it lists anchors, not controls: two controls on one anchor collapse
to one row and share a state cell, and a control without an anchor disappears.
The ticket asks for the tenant's controls, so the row is the control.

## D2 — The catalog keeps a server-side `has_control` filter and control links

**Decision.** `/v1/anchors?include=state` gains `has_control=true|false`,
applied in SQL ahead of `LIMIT`/`OFFSET`, plus per-row `control_id` and
`control_count`, and response totals (`total`, `catalog_total`,
`with_control_total`). When several controls anchor to one SCF control the
row links to the lowest `bundle_id`, which is deterministic. The filter is a
400 without `include=state` or with `framework_version_id`, rather than being
silently ignored on a read that does not join controls.

## D3 — Catalog paging is left to #1740

**Decision.** The catalog view still shows the platform's first page (100
anchors); its header reports the true totals from the server. Paging the
catalog view is #1740's scope. The "My controls" view walks the
per-control endpoint page by page (500 per request) until `total`, so the
client-side family, state and freshness filters see every control.

## D4 — Catalog anchors without a control are not links and not selectable

**Decision.** A catalog row with no tenant control renders its SCF code as
plain text and has no selection checkbox. There is no control page to open,
and bulk owner-assignment posts control ids, so an anchor id in the
selection would only fail per item upstream. Selection now always holds
control ids.

## D5 — The sidebar badge counts tenant controls

**Decision.** The Controls badge reads `/api/controls-list` (the tenant's
active controls), the same set the default view lists, instead of the length
of the first anchor page.
