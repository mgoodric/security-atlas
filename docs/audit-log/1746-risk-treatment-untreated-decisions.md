# 1746 — Risk treatment: untreated default + acceptance status — decisions log

Build-time calls made for issue #1746 (risk treatment silently defaulted to
`avoid`). Supersedes slice 663's D1 default (`docs/audit-log/663-risk-create-dead-end-decisions.md`),
which chose `avoid` only because it was the one treatment with no required
fields.

- detection_tier_actual: manual_review
- detection_tier_target: unit

(The bug surfaced in a first-run adopter setup on 2026-11-12, when a register
import recorded every row as `avoid`. A unit test pinning "an omitted treatment
never becomes a real decision" would have caught it; `TestValidateTreatment`,
`TestIsOpenTreatment` and the vitest default pin now do.)

## Decisions made

### D1 — Two migrations, not one

`ALTER TYPE ... ADD VALUE` cannot be used in the transaction that added it, and
the self-host runner (`deploy/docker/bootstrap/migrate.sh`) applies each file
with `--single-transaction`. `20260809000000` adds the enum value alone;
`20260809000001` sets the default and adds the acceptance status. There is no
down for the first file: Postgres cannot drop an enum value, and
`init.down.sql` drops the type. Verified with a `--single-transaction` apply of
every forward migration and a full down/up round-trip.

### D2 — Existing rows

No treatment value is rewritten. A silent `avoid` default cannot be told apart
from a real avoid decision (out of scope per the ticket). Every existing
`accept` row already satisfied the accept CHECK (accepter + expiry), so it is
backfilled `acceptance_status = 'approved'`, which is what it was. All other
rows keep `acceptance_status` NULL.

### D3 — `acceptance_status` is set exactly when treatment is `accept`

A CHECK (`risks_acceptance_status_iff_accept`) and `ValidateTreatment` both
require it. The alternative, a nullable status that means "approved" when
absent, would keep the ambiguity the ticket is removing. Moving a risk off
`accept` (the MCP update applier) clears the status; accepter and expiry are
kept, since the CHECK now binds only an approved acceptance.

### D4 — `mitigate` via an action plan is reachable on update, not on create

`ValidateTreatment` accepts a linked action plan in place of a control. A new
risk has no action plans: linking one goes through `actionplan.Store.LinkRisk`,
which writes the action-plan audit row and needs an actor. Inlining that into
risk create would bypass the action-plan audit trail, so REST create still
needs a control for `mitigate` (400 otherwise). The planned-mitigation flow is
create `untreated` → link an action plan → set `mitigate` through
`update_risk_treatment`. A REST update path is #1749.

### D5 — `update_risk_treatment` gains `accepter` and `accepted_until`

Without them a proposed acceptance had no path to `approved`: create sets
neither field and there is no REST update yet (#1749). Both are optional and
overwrite the risk's values when given. The applier reads the risk's current
side fields, linked controls and non-tombstoned linked action plans inside the
proposal's transaction and runs `ValidateTreatment` before writing, so an
approved proposal can no longer set `mitigate` on a risk with nothing linked.

### D6 — The web form posts `accept` as a proposed acceptance

The form has no accepter or expiry inputs (slice 105), so `accept` was
unsubmittable before. It now posts `acceptance_status: "proposed"`, and the
option reads "accept (proposed, pending approval)". The form opens on
"untreated (not yet decided)".

### D7 — Display and export

Board pack and brief rows render `accept (proposed)` / `accept (approved)`
(`risk.TreatmentLabel`) so a pending acceptance never reads as signed off. The
risk CSV/XLSX export appends an `acceptance_status` column at the end, keeping
existing column positions for position-keyed consumers.

## Re-evaluate

- Who may approve an acceptance (role checks, exec sign-off) — out of scope here.
- Whether REST create should take `linked_action_plan_ids` once the action-plan
  link can be written from the risk side with its audit row (#1749 territory).
