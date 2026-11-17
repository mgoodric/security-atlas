# #2147: declared transition tables for the lifecycles Spec B changed

detection_tier_actual: none
detection_tier_target: none

Build-time calls made while adding the risk treatment, risk open/closed and
control-state tables to the #2146 export.

## D1. Every table says what enforces it

`lifecycle.Table` gains `enforcement`: `database` (a trigger, named in
`trigger`), `store` (only the Go store's checks and guarded UPDATEs) or
`derived` (nothing refuses a move; the state is recomputed from inputs).
`Validate` keeps it consistent with `trigger` and requires every derived move
to be the system's. The five #2146 tables were re-exported with the field;
the web diagram check reads only states, initial, terminal and transitions, so
it is unaffected.

## D2. Risk treatment is one lifecycle over two columns

`treatment` and `acceptance_status` move together (an acceptance status
exists exactly when the treatment is accept), so the table's states are the
six combinations: `untreated`, `mitigate`, `transfer`, `avoid`,
`accept_proposed`, `accept_approved`. `risk.TreatmentState` maps a row to one.
A table may list one action per from-state, so the single "change
treatment" route appears as one action per target (`clear_treatment`,
`mitigate`, `transfer`, `avoid`), all on `PATCH /v1/risks/{id}/treatment`.

Open/closed is a separate table (`risk_status`). A closed risk takes no
treatment move; `TestTreatmentLifecycle_ClosedRiskTakesNoMove` holds that
rather than folding status into the treatment states, which would double
them.

## D3. Control state is a complete graph, labelled derived

`control_evaluations` is append-only and each evaluation is computed afresh,
so every result can follow every other. The table lists all twenty pairs,
each named for the input that produces its to-state (`evidence_passes`,
`evidence_fails`, `evidence_not_applicable`, `no_current_evidence`,
`exception_in_force`). It is simplified to one scope cell and no declared
evidence queries. `TestLifecycle_EveryPairFromComputeRow` drives the
engine's own `computeRow` through every (state, input) pair; fail -> pass
and pass -> fail are attestations, so they hold only through
latest-attestation-wins.

## D4. A new state fails a test until its table draws it

Each new table has a test comparing its states with the enum or CHECK behind
the column (`evidence_result`, `risk_treatment` x `risk_acceptance_status`,
`risk_status`), and the action-plan table gains the same against the
`action_plans.status` CHECK.

## D5. Action plans: no new rows

The #2146 action-plan table already marks verified -> completed as
`actor: system`. completed -> verified is made by a person (no linked
controls) and by the reconcile (linked controls); a table lists one move per
pair, so it stays a person's move with a note saying the system makes it too.
`TestLifecycle_SystemMovesRefusedToAPerson` now checks, from the table, that
a person cannot make any system move through the store.
