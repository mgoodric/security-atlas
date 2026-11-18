# #2325 — SCF remap proposals follow-ups: decisions log

detection_tier_actual: manual_review
detection_tier_target: integration

Follow-ups from the review of #2293 (#1775). Two were bugs a reviewer caught by reading: the re-run summary counted proposals again, and a control superseded by a new version kept its pending proposal in the list. Both now have integration assertions that were seen failing before the fix.

## D1 — The summary counts proposals written, not proposals classified

`RebindReport.Proposals` counted every non-pinned control, so a re-run of the same import printed the open queue a second time. It now counts a proposal only when `InsertSCFRemapProposal` wrote a row (`RowsAffected() == 1`). That matches `EdgeRemapReport.Queued` from #2552, which already counted on `RowsAffected() == 1`, so the two import summary lines mean the same thing.

A write includes the one update the insert allows: an `open` proposal that a later run re-bound becomes `rebound` (#2321). That is a change this run made, so it counts. A re-run that changes nothing prints zeros.

## D2 — Hide stale proposals on read, rather than resolving them on supersede

The dispatcher's brief preferred resolving the proposal when the control is superseded. I filtered on read instead:

- A control is superseded on more than one write path (bundle upload, remap approval). Resolving on supersede means a write on each path, or a trigger, plus a new status value (a CHECK change). A read-side predicate is one place and cannot be missed by a future path.
- The filter keys on the control's current state, so it cannot drift from it.
- Deciding such a proposal is already refused (`ErrRemapControlChanged`, "control has a newer version"). The list now agrees with the decide path.

The predicate leaves out only pending proposals (`open`, `rejected`) whose control has `superseded_by` set. Decided proposals stay listed: an `approved` proposal's control is, by design, the version the approval superseded, and `rebound` and `kept_legacy` rows are records. A blanket `superseded_by IS NULL`, as the ticket worded it, would have hidden every approved proposal.

If a new version is still on a legacy anchor, the next import queues a fresh proposal for it, because the proposal is keyed on the control row.

## D3 — `class_not` rather than a flag

Rebound rows whose class is not `unchanged` are surfaced with a `class_not` filter, which composes with `status`: `?status=rebound&class_not=unchanged`. A dedicated boolean would name one combination. A generic exclusion also serves other reviews, such as "every open proposal that is not a split". The CLI gets `--class-not`.

## D4 — Keyset paging, and the review page reads every page

- `?limit=` uses the controls list's bounds: default 100, maximum 500. A malformed limit falls back to the default, as `listPagination` does for `/v1/controls`.
- The cursor is opaque (base64url JSON of the last row's `created_at`, `bundle_id`, `id`). The order (`created_at DESC, bundle_id, id`) ends on the primary key, so the walk has no duplicates or gaps. A cursor that does not decode is a 400.
- `total` is a separate `count(*)` with the same filters and no cursor. It is cheap at this table's size and is served by the tenant index.
- The handler asks for one row past the page to decide whether `next_cursor` is set, so the last page never returns an empty follow-up.
- The review page (#1778) groups every pending proposal by class and shows a count per tab, so a single page would undercount. Its client walks `next_cursor` at the maximum page size. Paging the page itself is left until the queue gets large enough to need it.
- The CLI prints one page and names the `--cursor` for the next one, rather than walking, so `--json` stays one API response.

## D5 — `btrim` on every legacy-ID comparison, and indexes

The import trims legacy IDs before storing them (`catalog.go`), so the untrimmed `=` comparisons were correct in practice. They now use `btrim` like the "has legacy IDs" check beside them, so stored whitespace cannot split the queues. The change is in the control query and in the edge (#1777) and overlay (#1804) listings, so the three queues still classify an anchor the same way.

Migration `20261253000000` indexes `scf_remap_proposals.legacy_anchor_id` and `target_framework_version_id`, both `ON DELETE CASCADE` foreign keys, plus `(tenant_id, created_at DESC, bundle_id, id)` for the paged order.
