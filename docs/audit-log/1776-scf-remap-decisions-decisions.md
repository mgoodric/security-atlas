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

## D1a — The boot seed keeps an approved re-anchor of a stock control (review B1)

Bootstrap re-uploads every `controls/soc2/` manifest on each boot. After a person approves a remap of a stock control, the shipped manifest no longer matches the active revision. Without a guard it would supersede that revision on every boot and put the control back on the shipped code, while the proposal and the history still said "approved". When the shipped code is gone (a split or a withdrawn anchor), the upload would instead fail with 404 on every boot.

So `control.upload` first checks two things. Is the incoming manifest a shipped stock manifest (`scfimport.IsStockManifest`: hash and bundle ID)? Is the bundle's active revision the `new_control_id` of an `approved` decision (`IsApprovedRemapRevision`)? When both hold, the upload is a no-op and returns `KeptReviewedAnchor` with a warning. The upload handler also logs the kept anchor, and `atlas-cli controls upload` prints the response, so the boot log shows it. The check runs before the manifest's anchor is resolved, so a shipped code missing from the current release does not fail the boot.

We keyed on the stock manifest, not on the uploader. Any non-stock upload, including a person's own revised bundle, proceeds as before and can supersede the approved revision on purpose. Changing a reviewed stock control means uploading a changed bundle.

`TestRemapDecide_ApprovedStockReanchorSurvivesBootSeed` approves the real `soc2_cc8_1_change_management` stock control twice:

- once when its code is split, so the shipped code is gone;
- once when its code is reused for another control, so the shipped code still resolves and an unguarded upload would silently move the control.

After each approval it re-runs the boot seed and the re-bind, then asserts the control is still on the approved anchor. With the guard disabled, the reused case supersedes to v3 on the reused code. That is the failure this guard prevents.

We considered refusing approval of stock controls instead (the alternative), and rejected it. Split and no-successor stock controls are exactly where the human choice matters, and #2321 cannot move them.

### Held-back shipped updates (#2358)

`IsStockManifest` matches every revision the platform ever shipped (`stockManifestHashes`), not only the current one. So once a tenant's stock control is re-anchored, a _later_ shipped update to that control's `control.yaml` is held back too, on every boot, until a person uploads a bundle that carries the change. That is the intended priority: the reviewed anchor wins over the shipped one. But it means a platform fix silently never reaches that tenant.

The hold is no longer visible only as a boot-log warning. Each time the guard keeps a lineage it upserts a row into `stock_control_holdbacks` (one per tenant and bundle): the approved revision behind the hold and the sha256 of the shipped manifest it did not apply. `first_held_at` restarts when a different shipped manifest is held back. `GET /v1/admin/controls/held-back` (tenant admin only) and `atlas-cli catalog remap held-back` list them. Each entry shows:

- the active revision;
- the approved re-anchor;
- the shipped manifest being missed;
- the stock manifest the approval replaced.

`shipped_update` is true when the last two differ. In that case the platform changed the control after the re-anchor, and the tenant does not have that change.

The row is derived state, refreshed on every boot, so it cascades with the controls it names. The platform cannot list a shipped update before the boot seed offers it, because the server holds only the hashes of shipped manifests, not their text. A row appears on the first boot after the approval.

### Edits after approval (#2358)

The guard first keyed on the active revision _being_ the approved one. A person who edited the approved control (v2 approved, v3 a non-stock edit) lost that protection. On the next boot the shipped manifest superseded v3. When the shipped code was gone, as in a split, the boot failed with 404 instead.

The guard now asks whether the active revision **is or descends from** an approved revision. `ApprovedRemapLineageRevision` walks `superseded_by` back from the active row and returns the nearest revision an `approved` decision created. No schema change: the lineage is already the `superseded_by` chain. `TestHeldBack2358_ReviewedStockControlListedAndEditsKept` approves the split case, edits v2 into v3, and re-runs the boot seed. v3 stays active, and the held-back row names v3 held behind the approved v2. With the walk limited to the row itself, the boot seed tries to supersede v3 and fails on the gone code.

detection_tier_actual: manual_review (the #2351 re-review). detection_tier_target: integration.

## D2 — The history is a separate, append-only table

`scf_remap_decisions` takes one row per decision. A row holds the decision, the legacy control and its anchor, code and release, the new control, anchor, code and release (on an approval only), an optional reason, `decided_by` and `decided_at`. A CHECK ties the four `new_*` columns to `decision = 'approved'`.

The proposal row also changes: its `status`, and its `target_anchor_id` on an approval. But a rejected proposal can later be approved, and one row cannot hold both decisions. So the history is its own table. The app role has `SELECT, INSERT` and no `UPDATE` or `DELETE`. RLS (`tenant_read`, `tenant_insert`, FORCE) scopes it, and composite `(tenant_id, …)` FKs to the proposal and to both control rows keep each row inside its tenant.

Every FK on the history is `ON DELETE RESTRICT` (review R2). Deleting a decided proposal, either control row or either anchor fails, rather than erasing the record with it. An undecided proposal still cascades from its control and anchors, as #1775 set up. No production path deletes controls: they are superseded, never deleted. The catalog reseed's `TRUNCATE controls CASCADE` still clears the table, as `TRUNCATE` does. Test cleanups delete `scf_remap_decisions` before `controls`.

Every query in the decision path also filters on `tenant_id` explicitly, on top of RLS (review R1, the #2337 convention). Decision bodies must be one JSON object; trailing data is a 400 (review R3).

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
