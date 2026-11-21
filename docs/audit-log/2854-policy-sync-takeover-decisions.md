# 2854 — Policy sync: take over imported policies: decisions log

Issue: mgoodric/security-atlas#2854, under the Confluence policy sync spec
(#2813, Q34). Builds on #2850 (the sync endpoint), #2851 (versions and
drift), #2852 (the lock) and #2853 (missing pages).

## Detection-tier classification

- **detection_tier_actual:** integration, then manual_review. Adding the `sync_takeover` action
  broke two older down-migration tests (`TestPolicySyncMigration`,
  `TestPolicyControlsUpdatedMigration`) whose cleanup listed only the
  actions they knew; caught by the local integration run, fixed in this PR.
  Review then caught that the first version silently picked one of several
  same-titled imported policies (D2), and that the migration timestamp
  collided with open #2906 (renumbered to `20261289000000`).
- **detection_tier_target:** integration.

## Decisions

### D1 — the match is exact title, published, imported, same tenant

The lookup is `title = page title` (both trimmed at parse), case-sensitive,
on `status = 'published' AND source_attribution = 'imported'`, under the
caller's tenant RLS context. A looser match (case folding, fuzzy) risks
taking over the wrong policy, which is worse than a duplicate the security
leader can see in the list. It runs only when the page has no published
synced row in its source, i.e. on its first sync.

### D2 — an ambiguous title is refused, not resolved

The import does not chain versions, so a tenant can hold "X 1.0" and
"X 2.0" both published and imported. A first version of this PR picked one
(same version first, then the latest published). Review (#2908) rejected
that: an audit tool should refuse rather than choose. The sync now skips
the page with an `ambiguous title` reason in two cases:

- more than one published imported policy carries the page's title; the
  reason names every candidate with its version;
- the title matches one imported policy, but more than one page in the
  push carries it; each of those pages is skipped, and the reason names
  them all.

A skipped page writes nothing, so it is still a first sync on the next
run, and it takes the policy over once the title is unambiguous. The
candidate query has no `LIMIT`. Its `FOR UPDATE` makes a concurrent run for
another source wait; under READ COMMITTED that run re-checks the committed
row and no longer sees it as a published imported policy.

### D3 — same version is in place; the imported fields are cleared

The imported row becomes the synced row: same id, so its acknowledgements
and control links are untouched. `policies_imported_fields_chk` makes
`imported_by` and `imported_at` legal only on imported rows, so the
takeover clears them. Who imported the policy and when is still in its
`imported` audit row. The historical approver stays the import's: the
version did not change, so the approval did not either.

### D4 — same version, different body: taken over, and drift

What staff acknowledged is not rewritten (#2851). `synced_body_sha256` is
the hash of the body the row already holds, and when the page body
differs the drift columns are set in the same update. The result is
`taken_over` with a reason that says the page drifted; later runs report
`drift` until the page's version is bumped or its body matches. An import
made from an export of the same Confluence page will often differ in
whitespace from the connector's conversion, so this is expected to be
common on a first sync. It is the honest outcome, and one bump clears it.

### D5 — different version: a chain, as a synced version bump

The imported row is superseded (it stays `imported`) and the synced row is
inserted with it as predecessor and its control links carried over, as
`syncNewVersion` does. The same successor check applies: an in-tool draft
already holding the imported row's next-version slot skips the page with a
reason, rather than failing the run on
`policies_predecessor_unique_when_set`. The superseded row's
acknowledgements stay on it; staff are asked to acknowledge the new
version.

### D6 — one outcome, `taken_over`, for both takeover shapes

The report gains `taken_over` and a `taken_over` count. The chained shape
also carries `predecessor_id` (the imported policy). Splitting it into
`new_version` would hide the takeover, which is the thing the security
leader needs to see on the first run.

### D7 — the audit row is `sync_takeover`, on the taken-over policy

A new action and a new column, `previous_source_attribution`, rather than
overloading `previous_state`/`new_state`, which hold the policy status.
The row is written on the imported policy in both shapes, with the key's id
as actor, the page id as `source_file` and the page URL. The chained shape
also writes the usual `synced` row on the new version. The down migration
refuses while any `sync_takeover` row exists, as the other audit-action
migrations do.

### D8 — an authored policy is reported, never taken over

A non-superseded policy with the page's title whose attribution is neither
`imported` nor `synced` (tenant-authored, vendor-provided, community
draft) is left alone. The page is created as a new policy and the
`created` result's reason names the authored policy. This is the only
`created` result that carries a reason.
