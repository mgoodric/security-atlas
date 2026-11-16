# #1778 — SCF remap review page: decisions log

detection_tier_actual: none
detection_tier_target: none

Web only, on top of the control remap API (#1775, #1776) and the edge remap API (#1777). No migration, no new platform route.

## D1 — Split: anchor history and the legacy-anchor flag moved to #2392

The ticket also asks for anchor history on the control detail page and a legacy-anchor flag on the controls list. Nothing reads `scf_remap_decisions` over the API yet, so the history needs a new tenant-scoped read (query, handler, route goldens, OPA mapping, integration test) on top of the page's BFFs and spec. That took the change past the ~25-file line the dispatcher set, so both moved to #2392 (a sub-issue of #1718) and this PR covers the review page: the ticket's first, second and fourth acceptance checks.

## D2 — What a tab counts

A tab counts the proposals a reviewer can still decide: `open`, and `rejected` (which #1776 keeps revisitable). `rebound`, `approved` and `kept_legacy` are settled and drop off the page. So bulk-approving unchanged proposals empties the unchanged tab, and a rejected proposal stays in its tab with a "Rejected earlier" badge and no second Reject button.

## D3 — Edge proposals: hide on 403, the platform is the gate

The edge remap API is super-admin only, in the handler and in OPA. The BFF (`/api/admin/crosswalk-edge-remap-proposals`) forwards the caller's own bearer and passes the platform's status through, so a tenant admin gets the same 403 from the BFF. The page reads that 403 as "not a super-admin" and renders no edge section. It does not consult `/api/admin/me` first: a second, client-side role check could only drift from the platform's answer. Any other failure shows an inline error, so a broken read does not pass for "not a super-admin". The BFF asks for `status=open` only.

## D4 — An explicit anchor for split, no_successor and reused_id

The page always sends `scf_id` for these three classes, and Approve stays disabled until one is chosen, from the listed successors or from the anchor search. The platform already requires it for split and no_successor. For reused_id it would accept the single successor by default, but the code is ambiguous by definition, so the page never approves it without a pick. Reused-ID proposals are flagged and show both meanings: the code's title in the legacy release and its title (and the code it continues, from `legacy_scf_id`) in the new one.

## D5 — Anchor search and lookup in the BFF

`/v1/anchors` has no text search, so the BFF pages `GET /v1/anchors?framework_version_id=…` (500 a page, at most 10 pages, about 1,400 anchors a release) with the caller's bearer and filters by code or title, returning the first 25 matches. It reads the global catalog only. The lookup of named codes calls `GET /v1/anchors/{code}?scf_release=` for up to 10 codes. Codes and release labels are pattern-checked before they reach an upstream path, so a bare `..` is refused. A server-side `?q=` on `/v1/anchors` would be cheaper, but it is a platform change this ticket does not need.

## D6 — Bulk approve is one call

The Approve-all button calls `POST /v1/controls/remap-proposals/approve-unchanged` once; the spec asserts exactly one bulk call and no single approves. Skipped proposals from the result are listed inline.
