# #2450 — owner users follow-ups — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2450 (from the
review of #2441 / #2129)

detection_tier_actual: manual_review
detection_tier_target: integration

(All four items were found reading #2441 in review. The vendor audit gap
would have shown in an integration test that read an owner change back as a
non-admin; none existed.)

## D1 — a `vendor_audit_log` table, kind `vendor`

The per-domain logs are tables that the unified log reads as one kind each
(`risk_audit_log` as `risk`, #1954). The vendor log follows `risk_audit_log`
column for column: `actor` (the caller's `credstore.PrincipalOf`, as the risk
handlers record it), `action_type` CHECK-listed (`owner_changed` only),
`before_state` / `after_state`, `subject_module`; FORCE RLS with SELECT and
INSERT policies only, and an append-only trigger. No FK to `vendors`: the
trail outlives the vendor.

`actor` holds the principal, so it plays the part `me_audit_log` gave a
separate `actor_principal` column (#2329): a machine caller is named by its
key or client id, never `uuid.Nil`. A write with no caller at all (none in
production; the store is only called from the handlers) records
`system:vendor-store`, because the column is never empty.

The payload is the before and after owner. Unlike the risk branch, which
strips values because it predates the payload gate, the vendor branch keeps
them and relies on the gate (#2379): `kindSourceResource` maps `vendor` to
`vendors`, so a caller who cannot read vendors sees the envelope only.

## D2 — existing `me_audit_log` rows stay

Moving audit evidence is a separate, approved step. The migration leaves
`vendor_owner_update` in the `me_audit_log` CHECK so those rows stay valid,
and the down migration of `20261226000000` keeps refusing while they exist.
`scripts/dry-run-2450-vendor-owner-audit.sql` counts them per tenant
(read-only, rolled back) and carries the proposed copy in its header. The
copy does not delete: `me_audit_log` is append-only, so removing the
originals is its own decision.

## D3 — MCP owner check shares REST's validator

`risk.checkOwnerUser` became `risk.CheckOwnerUser`, and the MCP appliers call
it, so MCP and REST refuse the same owners (disabled, other tenant, unknown).
A refusal is `ErrInvalidInput`, like the category refusal beside it. A
malformed id is refused when the proposal is filed (`validateToolInput`);
whether the user is active is checked at confirm, inside its transaction,
because it can change in between.

On `update_risk_treatment`, `owner_user_id` wins over `treatment_owner` when
both are given, as POST `/v1/risks` drops the text when an owner is picked.
The new `risk.SetOwnerUserTx` writes the change through the same record-field
path PATCH uses, so it logs one `fields_changed` row.

## D4 — History already rendered owners with `<Person>`

The ticket's third item was done by #2581 (#2138): `history-format.ts` marks
`owner_user_id` as a `person` ref and `risk-history.tsx` renders it through
`<Person>`. No change here.

## D5 — vendor export names the owner by display name only

The dispatcher's rule was the `/v1/people` name with its local-part fallback.
The risk export uses exactly that (`people.NamesByID`). The vendor export
cannot: slice 139 D1 masks every email-shaped owner so a vendor export handed
to a third party leaks no local-parts, and a test pins that the local-part
never appears. So `owner_name` there is the trimmed `display_name`, empty
when the user has none; `owner_user_id` still identifies them. Both columns
are appended so existing positions hold.
