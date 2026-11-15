# #1776 — Review and apply SCF remap proposals: decisions log

detection_tier_actual: none
detection_tier_target: none

A person resolves each SCF remap proposal (#1775) through
`POST /v1/controls/remap-proposals/{id}/approve | reject | keep-legacy`,
`POST /v1/controls/remap-proposals/approve-unchanged`, or
`atlas-cli catalog remap list | approve | approve-unchanged | reject | keep-legacy`.

## D1 — Approval is a re-upload of the stored manifest

Approve rewrites the control's stored `control.yaml`. It sets `scf_anchor_id` to the chosen successor code and `scf_release` to the proposal's target release, and leaves every other key as it was. It then runs the result through `control.UploadTx`, the same path any manifest change takes. Because `scf_release` changes, the upload always supersedes: a new control row with `version + 1`, with the legacy row's `superseded_by` pointing at it. Nothing edits the control in place. If the upload did not supersede the locked version, the approval rolls back.

The rewritten manifest declares `scf_release`, so the approved control is pinned to the target release. That is the point of a reviewed re-anchor: the choice should not drift on the next import. The active row's description is carried over, because the stored YAML does not hold any `DESCRIPTION.md` text.

**Known edge, not fixed here.** A stock control re-uploaded at boot from its original `controls/soc2/` manifest no longer matches the approved revision's hash, so it supersedes again with the original code. Stock controls mostly move through #2321's title-only re-bind and not through this queue. A stock control that a person re-anchors here should also get its bundle updated in `controls/soc2/`.

## D2 — The history is a separate, append-only table

`scf_remap_decisions` takes one row per decision. A row holds the decision, the legacy control and its anchor, code and release, the new control, anchor, code and release (on an approval only), an optional reason, `decided_by` and `decided_at`. A CHECK ties the four `new_*` columns to `decision = 'approved'`.

The proposal row also changes: its `status`, and its `target_anchor_id` on an approval. But a rejected proposal can later be approved, and one row cannot hold both decisions. So the history is its own table. The app role has `SELECT, INSERT` and no `UPDATE` or `DELETE`. RLS (`tenant_read`, `tenant_insert`, FORCE) scopes it, and composite `(tenant_id, …)` FKs to the proposal and to both control rows keep each row inside its tenant.

## D3 — Statuses and transitions

| From \ action                  | approve  | reject   | keep legacy |
| ------------------------------ | -------- | -------- | ----------- |
| open                           | approved | rejected | kept_legacy |
| rejected                       | approved | 409      | kept_legacy |
| approved, kept_legacy, rebound | 409      | 409      | 409         |

The status CHECK keeps `open` and `rebound` and adds the three new values. #2236's `target_matches_status` CHECK becomes `(status IN ('rebound','approved')) = (target_anchor_id IS NOT NULL)`, still `NOT VALID` for the reason #2236 gives. #2236's composite FK and its `tenant_insert` and `tenant_update` policies (USING and WITH CHECK on tenant) stay as they are. Its column-limited `UPDATE (status, target_anchor_id, source)` grant already covers what a decision writes, so no grant widens. The import's upsert only promotes `open` to `rebound`, so a re-import never overwrites a decision.

## D4 — Choosing the anchor

With no `scf_id`, approval takes the single proposed successor. That works for `unchanged`, `renamed`, and a `reused_id` with exactly one successor. `split` and `no_successor` (and any proposal without exactly one successor) need `scf_id`, or the API returns 400. When given, `scf_id` may be any code in the target release, not only a listed successor. The reviewer may know better than the legacy-ID lineage, and for `no_successor` there is no list to pick from. A code outside the release is a 400.

## D5 — Stale proposals

Approve locks the proposal, then the control row (`FOR UPDATE`, active only). If the control has a newer version, or is no longer on the proposal's legacy anchor, the approval returns `409` and the proposal stays as it was. Bulk-approve reports such a proposal as skipped with that reason.

## D6 — Bulk approve

`approve-unchanged` selects the tenant's `open`, `unchanged` proposals `FOR UPDATE NOWAIT` in one transaction, the #2277 pattern. Lock contention returns `ErrRemapBulkRunning` (409) instead of waiting. Each approval runs under a savepoint, so a stale proposal is skipped without losing the others. Running it again finds nothing to do. Proposals a person rejected or kept legacy are left alone, even when their class is `unchanged`.

## D7 — Who may decide

The routes sit under `resource=controls`: `/approve` derives `action=approve`, and the rest derive `write`. The existing rego admits only admin and grc_engineer. control_owner, auditor and viewer are denied (`internal/authz/remap_decisions_1776_test.go`). On top of the role, `approval.Check` with no requester enforces people-only: an unbound key, an OAuth client or the bootstrap admin gets 403. The decider is recorded as `credstore.PrincipalOf`, which is the bare `users(id)` for a person.
