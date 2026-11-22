# #2881 — Risk import decisions: no GRC engineer, no self-approval — decisions log

Matt decided on #2881 (2026-11-21, option 1, strict). Risk import approve
and reject refuse a caller whose only approver basis is `grc_engineer`,
and the uploader never approves or rejects their own batch. This is
stricter than the other approver gates, and applies to risk import only.
This log records the build-time choices made to satisfy that decision. It
does not block merge.

- detection_tier_actual: manual_review
- detection_tier_target: unit

(The conflict between spec #2812 and the gate was found in review of PR
#2875, decisions log D4, not by a test.)

## D1 — One predicate: `authz.IsRiskImportDecider` = `IsTenantAdmin || IsApprover`

This is `IsTenantApprover` with the grc_engineer clauses removed: both the
live grant and an unbound key's label. The approver flag still counts. An
admin grants it, either when minting a key (`/v1/admin/credentials`) or
through `super_admin` on a JWT, so it is a basis separate from the
grc_engineer role. The predicate lives in `internal/authz` beside the
others, so the #2447 gate scan inventories it (`importDec2447`) and the
#2674 label-shape agreement covers it.

## D2 — OPA is unchanged; the handler is the narrower layer

`grc_engineer.rego` grants approve on risks, and it still admits
grc_engineer on the three routes. Narrowing OPA for one route family would
take a path rule that the rest of the bundle does not have. The three
route/role pairs are listed in `handlerNarrower2428`, like the other
pre-existing narrowings. The opposite case, a handler admitting what OPA
refuses, is still never allowed.

## D3 — `decidingPerson` is the one gate; the store refuses the uploader

The #2888 review asked for all three routes to narrow in one place:
`decidingPerson` calls `IsRiskImportDecider`. The self-decision check runs
in the store, inside each decision's transaction. `lockPendingRow` reads
the batch's `uploader_user_id` with the locked row, so no request can slip
past it, and `ApprovePending` checks once before any row. That way an
uploader gets a 403 even on a batch with no pending rows, rather than a 200
that approved nothing. The error is `riskimport.ErrSelfDecision`, which
maps to 403.

## D4 — `uploader_user_id UUID NULL`, not a reinterpretation of `uploaded_by`

`uploaded_by` stays as it is (`credstore.PrincipalOf`), so existing readers
see no change. The new column holds `credstore.CallerUserID` of the
uploader: a users(id) for a person, and NULL for a machine token, an
unbound key or the bootstrap credential. A NULL uploader binds no one,
since no person uploaded. The migration backfills the column where
`uploaded_by` parses as a uuid: `PrincipalOf` writes a bare uuid for a
person and nothing else that parses. There is no foreign key, as on
`risk_import_rows.owner_user_id`.

## D5 — The page reads `can_decide` from the batch, not `/v1/me` is_approver

`/v1/me` `is_approver` is `IsTenantApprover`, which admits a GRC engineer,
and it knows nothing of the uploader. `GET /v1/risks/imports/{id}` now
returns `can_decide`, computed by the same gate as the decide routes plus
the uploader check. The review page shows the decide controls only when it
is true. The routes stay the authority. The #2944 approve-all warning is
untouched.

## D6 — `/values` create stays on the taxonomy gate

As the #2944 review asked, `PUT /v1/risks/imports/{id}/values` with
`create` still calls `IsTenantApprover`, the gate of
`POST /v1/risks/categories` and `POST /v1/themes`. The routes2428 and
gateSites2447 rows for it are unchanged, and their comments now say so.
