# #1800 — overlay edges shown as "org mapping" in the web UI

Parent: #1735 (tenant overlay edges). Builds on #1797 (the overlay table
and API) and #1798 (`origin` on the coverage and traversal wires).

detection_tier_actual: manual_review
detection_tier_target: contract

## What changed

- The five UCF traversal queries LEFT JOIN `fw_to_scf_overlay_edges` on an
  overlay row's id and project its `source_ref`, `created_by` and
  `created_at`. The coverage wires (`anchors[]` on requirement coverage,
  `requirements[]` on control coverage and anchor traversal) carry them as
  an `overlay` object, set only when `origin` is `overlay`. No migration.
- Web: an "org mapping" badge (the existing `org-mapping` `<Term>`) and a
  details list (rationale, source, author, date) on the requirement detail
  page, the SCF anchor detail page and the control coverage table.
- A requirement detail page at `/requirements/[id]`, on the existing
  `/api/requirements/{id}/coverage` BFF route. Requirement codes in the
  anchor detail and control coverage tables link to it.

## Decisions

**D1 — Provenance rides on the traversal wire, not a second fetch.** The
page could join `GET /v1/mapping-overlays` by edge id, but that is a second
round trip on three pages, and the list route needs its own permission
check. The traversal reads already run in the tenant transaction (#1798),
so the LEFT JOIN is under the same RLS as the view's overlay arm. A catalog
row joins nothing and carries no `overlay`.

**D2 — The mark is `origin` only.** The UI calls `isOrgMapping(row)`, which
reads `origin === "overlay"`. It never reads `source_attribution` (a catalog
edge can carry `org_internal`) or `mapping_tier` (#2240 drops the synthetic
tier on overlay rows). No surface shows `mapping_tier` for an overlay row.

**D3 — Organisation-entered text renders as text.** Rationale and source
are React text children. Nothing uses `dangerouslySetInnerHTML`.

**D4 — Author is the stored `created_by`, verbatim.** It is the credential
id the create API recorded, the same value the overlay list and CSV export
carry. Resolving it to a display name needs a user lookup across the
tenant's users. That belongs with the other actor-display work, not this
ticket.

**D5 — The ticket assumed a requirement detail page; there was none.** The
BFF route for requirement coverage existed with no page using it. The
smallest page that meets the acceptance criteria lists the requirement's
coverage score, its catalog mappings, its org mappings (a separate card)
and the tenant's controls behind them. A control whose anchor is reached
only by an overlay edge is marked "credited through your org mapping only"
(user story 14).

**D6 — The anchor detail page read a shape the platform never served.**
`AnchorDetail` typed rows as `{requirement, framework_version, strm_type}`.
`GET /v1/anchors/{id}/requirements` serves the flat
`requirementForAnchorWire` (the control-coverage row). Its only e2e spec
route-mocks the nested shape, so the mismatch never failed a test. The page
and `buildVersionOptions` now read the real fields. The version pin uses the
row's `framework_slug` and no longer guesses it from the display name. The
hermetic spec's mock is now the real shape. This was caught by reading the
handler while placing the org mapping rows, which is why the actual
detection tier is `manual_review`. The target is `contract`: a golden for
this route, like `control-coverage.golden.json`, would have caught it.

**D7 — Overlay rows sort after catalog rows on the anchor page.** Each
framework-version card lists catalog edges first, then org mappings, each
labelled. The requirement page separates them into two cards.

**D8 — The isolation e2e uses a minted tenant-B JWT.** It follows
tenant-switch-rls.spec.ts: `/v1/test/issue-jwt` with `super_admin=true`.
super_admin clears OPA authz for the synthetic user and does not bypass RLS
(that spec's decisions log D4). The fixture inserts the overlay row directly
so its author and date are fixed values the spec can assert. The create API
is covered by `internal/api/mappingoverlays`' integration tests.
