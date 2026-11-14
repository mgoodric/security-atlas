# Issue #1782 — Control import: approval creates a real control in the organisation

Part of #1734 (bring-your-own controls). Builds on
`1779-control-import-staging-decisions.md`,
`1780-control-import-candidates-decisions.md`,
`1781-control-import-llm-rerank-decisions.md` and #1770's release-aware
anchor binding.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — One transaction: `control.UploadTx`

**Decision.** `control.Store.Upload`'s body moved into an unexported
`upload(q, tenant, bundle, uploadedBy)`. `Upload` still opens its own
transaction around it. The new `control.UploadTx(ctx, tx, bundle, uploadedBy)`
runs the same body inside the caller's transaction. Approval locks the staged
row, uploads the bundle, records the decision and writes the audit row, all in
one tenant transaction.

**Why.** The ticket says approval goes through the existing control store,
and the dispatcher asked for the audit row in the same transaction. Calling
`Upload` with its own transaction would leave a window where a control exists
but its row is still pending, or has no audit row. A tx-taking variant keeps
one upload path. Hand-written bundles and imported ones share every rule: the
release binding, the family lookup and versioning.

## D2 — Only a person decides

**Decision.** The API resolves the caller with `credstore.CallerUserID` and
refuses approve and reject with 403 when the caller is not a person. That
covers machine tokens, unbound API keys and the bootstrap credential. The
users(id) is the row's `human_approver` and `decided_by`, and the audit row's
`actor`. The store also refuses an empty approver (`ErrApproverRequired`),
and the schema enforces it twice:

- `ai_assist_human_approver_guard` on the row's AI-assist columns;
- `control_import_rows_approved_chk`: an approved row has an approver and a
  control, AI-assisted or not.

**Why.** Matt's "people only" (relayed by the dispatcher). Reject also needs
a person, because the audit actor must name who decided. `ai_assisted` on the
row is true when the approved anchor was a candidate the LLM rerank placed, so
the guard applies exactly where AI output became a control.

## D3 — Decisions are final and idempotent

**Decision.** The row is read `FOR UPDATE`. Anything but `pending` refuses
approve and reject with 409 (`ErrRowDecided`), and nothing is written. A
concurrent second approve waits on the lock and then sees the row decided.

## D4 — Bundle shape

| Field                 | Value                                                                                                                                          |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| `bundle_id`           | `BundleIDFor(org control ID)`: lower-cased, other runs become `-`, a non-letter start gets `c-`, padded to 3, cut at 64                        |
| `title`/`description` | the row's                                                                                                                                      |
| `scf_anchor_id`       | the chosen code; `scf_release` is the batch's release, so the upload binds within it (#1770)                                                   |
| `implementation_type` | approver's, else the row's, else `manual_attested`                                                                                             |
| `applicability_expr`  | approver's, else the row's (it must be a JSON expression; free text is refused with a pointer to `applicability_expr`), else none (every cell) |
| `external_refs`       | `{system: organisation, kind: reference, id: <org ID>}`, plus `{system: <framework slug>, kind: tested_under, id: <code>}` per criterion       |

The bundle is YAML-encoded and goes through `control.FinalizeBundleForHTTP`
and `ValidateApplicabilityExpr`. A row the bundle rules refuse (a title over
200 characters, say) fails with 400, as it would on upload.

**Why `manual_attested`.** The ticket's "manual" is not a bundle value; the
allowed ones are `manual_attested` and `manual_periodic`. Attestation is the
weaker default: it asks nothing about cadence the sheet didn't give.

**Bundle ID collision.** A slug already used by any control of the tenant
fails that row with 409 (`ErrBundleIDTaken`), and the row stays pending.
Uploading through an existing bundle ID would version that control. That is
an update, which #1734 puts out of scope.

## D5 — Duplicates: any `reference` ref with the organisation ID

**Decision.** At approval, a row is a duplicate when an active control of the
tenant has an `external_refs` entry of kind `reference` whose `id` is the
row's organisation control ID, under any `system`. The row is marked
`duplicate`, `control_id` names the existing control, an audit row is
written, and the API answers 409 with the decision. Nothing is created.

**Why any system.** A control written by hand may carry the organisation's
number under its own label (`acme-audit`), not `organisation`. Re-importing
the sheet must not copy it. The cost is a false match when an unrelated
reference has the same ID; the reviewer sees `duplicate_of` and can act on
it.

**Why at approval, not at staging.** The AC is "marked duplicate and not
created". Checking at approval also catches a control created after the batch
was staged. Flagging duplicates at upload (story 7) is still possible later
with the same predicate.

## D6 — Audit: `control_import_audit_log`, kind `control_import`

**Decision.** A new append-only table, with the taxonomy_audit_log shape:

- RLS with SELECT and INSERT policies only;
- a trigger that refuses UPDATE and DELETE;
- no foreign keys, so the trail outlives the batch.

Actions are `approve`, `reject` and `duplicate`. A row carries:

- the batch, row, row number and organisation ID;
- the actor;
- on approve: the anchor id, SCF code and release, the control and the bundle;
- on reject: the reason.

The table is the twelfth branch of the unified audit log. `target_type` is
`control_import_row` and `target_id` is the row id.

**Why its own table.** No existing audit table has a batch, row and anchor
shape. The unified log's extension pattern is a table per domain.

## D7 — API and CLI

- **API.** `POST /v1/controls/imports/{id}/rows/{row}/approve` takes
  `{"candidate_rank": n}` or `{"scf_id": "..."}`, plus optional
  `implementation_type` and `applicability_expr`.
  `POST /v1/controls/imports/{id}/rows/{row}/reject` takes `{"reason": "..."}`.
  `approve` is a transition segment, so authz sees action `approve` on
  `controls`; `reject` is a write on `controls`.
- **CLI.** `atlas-cli controls import approve <batch> <row> --candidate N | --scf-id X`
  and `reject <batch> <row> --reason R`. `<row>` is the row's id or its sheet
  row number, resolved through the rows list. `show` now prints each row's id
  and its decision.
- **Rows read.** The rows API returns `decided_by`, `control_id`,
  `approved_scf_id` and `rejection_reason`.

## Out of this ticket

- Bulk approve-clear.
- Export of approved bundles.
- The web review page.
- Flagging duplicates at upload.
