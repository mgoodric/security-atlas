# Issue #2845 — Risk import: level and org unit placement

The placement slice of spec #2812 (Q17), on top of the tracer (#2839),
scores (#2841), treatment (#2842), owners (#2843), value maps (#2844) and the
review page (#2847).

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — Placement is checked at staging, and again at approval

**Decision.** Staging matches the org unit cell and applies the create
path's placement rule through `risk.CheckCreatePlacement`, which wraps the
same `resolveLevel` that `Store.Create` uses. A row that cannot be placed is
a rejected row with a reason. Approval passes the staged level and unit to
`CreateTx`, which checks them again against the unit as it is then; a refusal
there (`risk.IsPlacementValidation`) skips the row and leaves it pending with
the reason.

**Why.** The ticket asks for an unknown name to reject the row with a reason,
which is the upload's rejected-rows surface. Checking only at approval would
let an approver approve a batch whose rows then fail one by one. Reusing the
create path's function, rather than restating the rule, keeps "the same rules
as placement on create" true when that rule changes. The approval check
covers a unit deleted or re-levelled between upload and approval.

## D2 — A name two org units share rejects the row

**Decision.** `org_units.name` is not unique per tenant. A cell that matches
the names of two or more units, trimmed and ignoring case, is rejected as
`ambiguous_org_unit` rather than matched to either.

**Why.** Picking one would place the risk in a part of the organisation the
register may not mean, silently. The owner match (#2843 D1) makes the same
call for an address two users share. A separate reason from
`unknown_org_unit` tells the user the fix is renaming a unit, not the cell.

## D3 — A level mismatch rejects the row rather than taking the unit's level

**Decision.** A row whose level differs from its matched unit's level is
rejected as `level_mismatch`.

**Why.** It is what a create does (`ErrLevelMismatch`, a 400). Overriding
either value would be a guess about which column of the register is wrong.

## D4 — The export's `org_unit_id` stays unmapped

**Decision.** `org_unit_id` is not an alias of the org unit field. The alias
set is `org_unit`, `org unit`, `org unit name` / `org_unit_name`,
`organisation unit` and `organization unit`; the level field takes `level`,
`risk level` / `risk_level` and `hierarchy level`. Broader spellings (`business unit`, `department`) are not
aliases and need a column map.

**Why.** The export's value is the unit's uuid in the exporting tenant, and
the field is matched by name, so mapping it would reject every row of a
cross-tenant round trip as `unknown_org_unit`. A broad alias risks turning a
column that used to stage, as unmapped, into a column that rejects rows.

## D5 — A staging rejection still counts toward in-file duplicate IDs

**Decision.** A row rejected for its placement has already been recorded by
the parse step for in-file duplicate detection, so a later row repeating its
risk ID is still rejected as `duplicate_external_ref`.

**Why.** It matches the parse step, where duplicate detection runs over the
rows that pass parsing, in file order. Both rows are listed with their
reasons, so the user sees the repeat and the placement problem together.
