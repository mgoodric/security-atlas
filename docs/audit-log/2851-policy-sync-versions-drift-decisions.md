# 2851 — Policy sync: version chaining and drift detection: decisions log

Issue: mgoodric/security-atlas#2851, under the Confluence policy sync spec
(#2813). Builds on the tracer, #2850 (`POST /v1/policies:sync`).

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced while building.
- **detection_tier_target:** none.

## Decisions

### D1 — supersede, then insert, in the run's transaction

`idx_policies_synced_page` allows one published synced row per page. The
current row is therefore superseded (`SupersedeSyncedPolicy`) before the
next version is inserted (`InsertSyncedPolicy` with `predecessor_id`). Both
happen in the sync run's transaction, under the source's advisory lock, so
no reader sees the page with no published row or with two. An overlapping
run waits, then finds the new version unchanged.

### D2 — a changed version wins over everything else

A page whose declared version differs from its synced row becomes the next
version, whether or not the body changed. The spec makes the version the
signal. A bump with an identical body is a deliberate re-issue, for example
to collect fresh acknowledgements.

"Changed" means different, not greater. Versions are free text, so they are
not ordered. A page moved back from 1.1 to 1.0 publishes a new version 1.0,
chained to 1.1 and superseding it, and staff acknowledge again. The text in
force changed, so the acknowledgements must follow it.
`TestSync_VersionRevertIsANewVersion` pins this.

### D3 — the new version keeps the previous version's control links

Control links belong to security-atlas, not to the page (spec: "Acknowledgements
and control links stay in security-atlas"). A new synced version that dropped
them would silently break control traceability on every bump, so the new row
copies `linked_control_ids` from the row it supersedes. Acknowledgements are
not copied. They belong to a version, which is the point of the bump.

### D4 — drift is two columns on the policy, not a table

`sync_drift_detected_at` and `sync_drift_body_sha256` sit on the published
synced row. Drift is current state about one row, not history: it is set by
one run and cleared by a later one. A CHECK allows the columns only on synced
rows and only together. The down migration drops them without a guard,
because the next run rebuilds drift from the page.

### D5 — detection time is the first sighting

A page edited twice without a bump keeps the time drift was first seen and
takes the newer body hash. "Out of step since" is the useful fact for the
policy owner. The hash says what the page holds now.

### D6 — drift writes no audit row

The policy audit log records writes to a policy's published state: imports,
syncs, approvals. Drift changes no version and no published text, and it is
reported in every run's response, so no `synced` audit row is written for it.
A new version is audited as a `synced` row on the new policy, the same action
as a first sync, carrying the key id, version and body hash.

### D7 — clearing drift

Drift clears in two cases:

- **The body matches again.** The page is reported `unchanged`, and
  `ClearSyncedPolicyDrift` nulls the columns.
- **The page arrives with a new version.** The new row starts with no drift,
  and `SupersedeSyncedPolicy` clears any drift on the row it supersedes. A
  superseded row is history, and "drift" would only mislead there.

### D8 — wire shape

- On the policy, `sync_drift: {detected_at, source_body_sha256}`, omitted
  when the policy is in step. Both list row shapes carry it, with and without
  `?include=ack_rate`. That is the surface for #2852's web badge.
- In the run report, per-status counts `created`, `new_version`, `unchanged`,
  `drift` and `skipped`. A `new_version` result carries `predecessor_id`, and a
  `drift` result `drift_detected_at` and a reason telling the owner to bump or
  revert.

### D9 — a bump is skipped while an in-tool draft holds the successor slot

A row has at most one successor (`policies_predecessor_unique_when_set`).
Until #2852 locks synced policies, an in-tool draft can be created as the
next version of a synced row. A bump of that page would then violate the
index, and because the violation aborts the transaction, the whole run would
roll back with a 500. Instead, the run locks the synced row
(`LockSyncedPolicyForSuccessor`, `SELECT … FOR UPDATE`) and checks for a
successor before superseding it. `FOR UPDATE` conflicts with the `KEY SHARE`
lock that a concurrent in-tool create takes through the predecessor FK, so
the answer holds until the run commits. A page with a successor is reported
`skipped`. The reason names the draft, and the run's other pages still sync.
Once the draft is discarded, the next run publishes the bump.

### D10 — a repeated drift writes nothing

`SetSyncedPolicyDrift` updates only when `sync_drift_body_sha256 IS DISTINCT
FROM` the new hash. A daily run against a page that stays drifted therefore
does not touch the row or its `updated_at`. The page is still reported
`drift` with its first detection time.

## Finding (not built here)

Only the version and the body are compared. A page whose properties changed
without a bump (next review date, owner role, audience) while its body stayed
the same is reported `unchanged`, and the synced row keeps the old values.
Spec user story 16 ("a synced policy's review date follows the page's
next-review property") therefore holds only when the version is bumped. No
ticket under #2813 owned updating properties in place; it is now #2877.
