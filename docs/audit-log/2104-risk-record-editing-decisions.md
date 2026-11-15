# Risk record editing and the risk audit trail — decisions log (#2104)

`PATCH /v1/risks/{id}` takes the record fields as well as placement
and category. `risk_audit_log` records every field change. It has a
read route, `GET /v1/risks/{id}/audit-log`. The risk page links to an
Edit route and shows a History section. Parent: #2071 (finish the
record lifecycles).

detection_tier_actual: none
detection_tier_target: none

No bug surfaced in shipped code. Two existing tests asserted the old
contract and were updated: `TestDecodePatch` and
`TestPlacement_PatchRejects` both refused `title` on PATCH, which this
ticket makes a record field.

## Decisions

**D1: One `fields_changed` row per PATCH, holding only the changed
fields.** Its `before_state` and `after_state` hold only the fields that
changed, keyed by their wire names. The ticket asks for "before, after"
per change, and one row per edit keeps a two-field edit as one History
entry. Placement and category keep their own `placement_changed` and
`category_changed` rows (#1749, #1812). A PATCH that sets placement and a
record field writes one row of each kind. A PATCH that changes nothing
writes no row.

**D2: The PATCH goes through `UpdateRisk`, the existing full-update
query.** The ticket names it. `updateRecordTx` carries treatment,
acceptance status, accepted_until and accepter over from the locked row
unchanged, and writes the category enum the row already holds, so the
`risks_category_sync` trigger sees no category move. Category moves stay
on `UpdateRiskCategory` through `ResolveCategoryTx`, so an archived or
unknown category is refused as on create.

**D3: Treatment and acceptance fields are a 400 that names the field.**
`treatment`, `acceptance_status`, `accepted_until`, `accepter`,
`residual_score` and `linked_control_ids` are checked before the strict
decode. The message names the field and says these are not record
fields, so a caller is not left guessing why an otherwise valid body
failed. Any other unknown field is still refused by
`DisallowUnknownFields`, and that message lists the fields PATCH accepts.

**D4: A methodology change resets the residual score.** A residual on
the old scale does not read on the new one: a 5x5 residual is
meaningless against a FAIR inherent exposure. The reset matches what
create stores: for FAIR the residual equals the normalized inherent
exposure, and otherwise it is `{}` (pending evaluation, which the deriver
fills). The reset shows up in the same `fields_changed` row as
`residual_score`, so the History explains why the residual moved.

**D5: The inherent score is validated against the methodology it will
have.** That is the new methodology when the PATCH changes it, and the
current one otherwise. A FAIR score is normalized as on create.

**D6: A rule-generated meta-risk's score and methodology are refused on
PATCH.** The aggregation engine owns that score (slice 054) and would
overwrite an edit on its next evaluation. An edit that silently comes
undone is worse than a clear 400. Its title, description, owner and
other record fields stay editable.

**D7: Clearing `instrument_reference` on a transfer risk is refused.**
Canvas §6.1 requires an instrument reference for transfer, and
`ValidateTreatment` enforces it on create. PATCH applies the same rule to
the one record field it governs, without re-running the full treatment
validation, because PATCH cannot change treatment.

**D8: `review_due_at` must be an RFC 3339 timestamp and cannot be
cleared.** The `risks_review_due_default` trigger fills the date only on
insert. A cleared date would leave the risk with no review date at all,
which #2059 removed.

**D9: Same role guard as create.** `CreateRisk` has no handler-level
guard. The OPA middleware's `risks` write rule is the gate (grc_engineer
and admin), and it covers `PATCH /v1/risks/{id}` by the same rule. The
read route falls under the `risks` read rule, so any role that can read
the risk (viewer included) can read its history. Both are tenant-scoped
by RLS.

**D10: The read route returns the whole trail, newest first.** A risk's
trail is one row per edit, so pagination is not needed at this volume.
The order is `created_at DESC, id DESC` (the existing
`ListRiskAuditLog`). Rows written in one PATCH share a `created_at`, so
their relative order is not meaningful. Rows from different PATCHes are
strictly ordered. An unknown id returns an empty list, not a 404: the
trail has no FK to `risks` and outlives a deleted risk (#1749).

**D11: The Edit route reuses the create form in edit mode.** `RiskForm`
with `initial` opens on the stored risk, reads the category list with
archived categories so a risk on one still shows it, and hides the
treatment select and the control picker. It adds an instrument reference
input. It sends only the fields that differ from the opening state, so
an untouched form sends nothing, and the page returns with "No changes
to save" instead of sending a PATCH the platform would refuse as empty.

**D12: The risk page adopts `DetailHeader` (#2102).** It keeps every
testid the existing specs use. Its date display now reads the UTC
calendar day, as the form and History do. The platform renders
timestamps in its own zone, so slicing the string could show a different
day from the one saved.

## Not done here

- Treatment and acceptance transitions (propose, approve, change
  treatment) have no endpoint yet. When one lands, it adds its own action
  to the `risk_audit_log_action_type_check` list, and History will render
  it through the same entry shape.
