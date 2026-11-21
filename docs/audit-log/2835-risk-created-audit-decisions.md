# #2835 — A `created` audit row for every risk create path — decisions log

Before this change, a risk's later edits were in `risk_audit_log` but its
creation was not, and neither were theme changes. Migration
`20261277000000_risk_audit_created.sql` adds two actions, `created` and
`themes_changed`. This log does not block merge.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced while building it. One unrelated defect was seen in
passing; see D7.)

## D1 — One writer, `risk.RecordCreatedTx`, called by every create path

The ticket asks for the entry to be written "at the risk store layer". Four
paths insert a risk, and two of them do not go through `Store.Create`:

| Path                                       | Inserts via                          | Writes `created` via                   |
| ------------------------------------------ | ------------------------------------ | -------------------------------------- |
| `POST /v1/risks`                           | `Store.Create`                       | `Store.Create`                         |
| `POST /v1/risks/aggregate` (manual parent) | `Store.Aggregate`                    | `Store.Aggregate`                      |
| MCP `create_risk`, applied on confirm      | hand-written `INSERT` in the applier | the applier, in the confirm tx         |
| Aggregation rule meta-risk                 | `aggrule.Engine.createMetaRisk`      | the engine, in the triggering write tx |

The MCP applier inserts by hand inside the proposal's confirm
transaction, and `Store.Create` opens its own transaction
(`appliers.go`'s header gives the reasons). So the applier writes the row
itself. To make sure it is the _same_ row, every path calls
`risk.RecordCreatedTx` with a `risk.CreatedState`; no path builds the row
on its own. `TestCreatedAudit_MCPCreateRisk_2835` covers the applier. With
the applier's call removed, that test fails ("audit rows = [], want one
created row"); with it restored, the test passes.

The coming risk import should call `Store.Create` and will get the row for
free. If it inserts directly the way the MCP applier does, it must call
`RecordCreatedTx`.

## D2 — The row's shape

`before_state` is `{}`. `after_state` holds `title`, `category_id`,
`treatment`, `owner_user_id` (null when unset) and `treatment_owner` (the
free-text owner), keyed by wire name like the other risk audit rows. The
ticket asks for "owner", and a risk has two owner fields, so the row
records both. The table's `tenant_id` column records the tenant. The
unified log's risk branch names the five fields in `changed_fields` and
never carries their values, as it does for the other actions.

## D3 — The actor

- REST create and aggregate: `credstore.PrincipalOf(cred)`. That is the
  person's user id for a session, or the key / OAuth client otherwise, the
  same principal the delete and link rows record. Both routes now answer
  401 without a credential instead of creating an unattributed risk. The
  router's auth middleware already requires one, so only a misconfigured
  test harness could reach that branch.
- MCP: `Confirmer.Actor()`, the person who confirmed. The proposal's
  `created_by` is the credential the AI-assisted proposal was filed under,
  and the risk exists because someone confirmed it. This matches what the
  treatment applier records (#2371).
- Rule meta-risk: no person is involved, so the actor is
  `system:aggregation-rule:<rule uuid>` (`aggrule.RuleActor`). It follows
  the repo's `system:<name>` convention for machine actors and names the
  rule that made the meta-risk.

`Store.Create`, `Store.Aggregate` and `Store.AssignThemes` refuse an empty
actor with `ErrTransitionInput`, so a create path added later cannot skip
the row by accident.

## D4 — Theme changes: `themes_changed`

`Store.AssignThemes` takes an actor and writes one row, before and after
`{themes: [...]}`, only when the normalised set changes. The handler's
add (`POST /v1/risks/{id}/themes`) and remove
(`DELETE /v1/risks/{id}/themes/{theme}`) both go through it, so
re-assigning the same set or removing an absent theme writes nothing, as
slice 053's AC-2 no-op promises.

## D5 — Paths deliberately left without a per-risk row

- **Theme rename (`RenameThemeOnRisks`).** Renaming a theme rewrites the
  name in every risk's `themes` array in one statement. The rename is
  already audited once, as a theme rename in the taxonomy/theme audit log
  (`writeThemeAudit`). Per-risk `themes_changed` rows would add N rows that
  each restate that one event. The rename is the change, and that is where
  it is logged.
- **The demo seed (`internal/demoseed`).** It inserts its fixture risks
  directly into a demo tenant. They are fixtures, not anyone's risk
  decisions, and a `created` row naming a seeding operator would make
  every demo tenant's history open with a long list of synthetic
  creations. If a real tenant ever gets demo rows seeded into it, this is
  the line to revisit.
- **Test fixtures** (`fixtures/e2e/*.sql`, `internal/dbtest`) insert
  directly by design.
- **Themes a manual aggregation or a meta-risk carries at creation.** They
  are the union of the children's themes, set in the same insert. The
  `created` row is the record of that insert. Later changes to them go
  through `AssignThemes` and are logged.

## D6 — Existing tests that pinned an exact audit trail

Many `internal/api/risks` integration tests assert exact audit-action
lists for a risk made over `POST /v1/risks`. Each such list now starts
with `created`. The transition env's `auditRows` helper now fails unless
the oldest row is a `created` row with an actor, and returns the rows
after it. So those tests keep pinning what they test and also confirm that
every risk they make has its origin row. Tests that count rows directly
count the `created` row explicitly.

## D7 — Seen in passing, not fixed here

`GET /v1/risks/{id}` returns 500 (`derive residual: inherent_score missing
numeric likelihood/impact`) for a risk an MCP `create_risk` proposal made
with no `inherent_score`. The MCP test here passes a score. The defect is
outside this ticket and needs its own.
