# 1867: Aggregation lock order, theme read, meta-risk lookup, ledger pointer, 0-child UI (decisions log)

Build-time calls made for issue #1867, the follow-ups from the #1862 review
(#1861).

- detection_tier_actual: manual_review
- detection_tier_target: integration

(Found in review of #1862. The #1861 suite ran deletes and theme writes one
at a time, so neither lock order was ever contended. The two concurrency
tests in `internal/risk/aggrule/lock_order_integration_test.go` contend
them, and were run against the old behaviour: the delete test failed with
`deadlock detected (SQLSTATE 40P01)`, and the theme test left the risk
linked after it lost the theme.)

## Decisions made

### D1: Risk writes take the rules' advisory locks before the risk row

`risk.Store.Delete` used to delete the row, then take the rule's advisory
lock in the after-write hook. An evaluation holds that lock and then links
children; linking takes `FOR KEY SHARE` on the child, which the delete's row
lock blocks. Opposite orders, so Postgres killed one side with 40P01.

A second hook, `risk.BeforeRiskWrite`, now runs before the row is touched.
`aggrule.LockOnRiskWrite` takes the advisory lock of every active rule the
after-write evaluation will run, in the same `(created_at, id)` order
`Evaluate` uses, through the new `Engine.LockRules`. Both sides then take the
key first. Advisory locks are re-entrant within a session, so the evaluation
that follows takes them again without waiting.

- **A second hook, not a change to `AfterRiskWrite`.** `risk` cannot import
  `aggrule` (#1750), and a single hook would have to run on both sides of the
  row write. Two named hooks keep each call site plain. `httpserver.go` wires
  both; the integration tests build the store the same way
  (`riskStoreAsWired`).
- **The delete of a rule-generated meta-risk takes the locks too.** Its
  themes include its rule's `target_theme`, so it now waits out an
  evaluation that is updating it rather than racing it. It still does not run
  the after-write evaluation (#1861 D4).
- **This also closes the old FK race** the issue names: an evaluation can no
  longer link a child whose delete is in flight, because the delete cannot
  start until the evaluation commits.

### D2: Theme writes read the before-themes under a row lock

`AssignThemes` read the risk's current themes with a plain `GetRiskByID`. A
concurrent theme write that had not committed was invisible to it, so a theme
that write added, and this one removed, was never re-evaluated.

The new `LockRiskByID` query reads the row `FOR NO KEY UPDATE`. A concurrent
theme write or delete waits for it; an evaluation linking the risk as a child
(`FOR KEY SHARE`) does not, so it adds no new wait to evaluation. The order is
the same as D1: an unlocked read picks which rules to lock, the rules are
locked, and then the locked read supplies the before-themes the hook uses.
`Delete` re-reads under the same lock, so the hook sees the deleted row's own
themes.

- **`FOR NO KEY UPDATE`, not `FOR UPDATE`.** `FOR UPDATE` conflicts with the
  `FOR KEY SHARE` an evaluation takes on a child, which would recreate D1's
  deadlock between theme writes and evaluations.
- **Returning the old themes from the `UPDATE`** was the other option the
  issue offered. It changes `UpdateRiskThemes`' row shape for one caller and
  still needs the rules locked before the row, so it saves nothing.

### D3: `GetRuleMetaRiskByKey` orders by `created_at, id`

Installs that raced two evaluations before #1861's advisory lock can hold two
meta-risks with one key. `LIMIT 1` without an order picked either. With the
order, every evaluation updates the oldest, and the operator doc says to
delete the other.

### D4: A below-threshold refresh names its meta-risk on the ledger row

Since #1861 an evaluation below threshold still relinks and rescores the
activation's existing meta-risk, but its `near_miss` / `no_match` row could
not carry `meta_risk_id`: `aggregation_rule_evaluations_fired_coherent`
allowed it only on `fired`. The constraint is relaxed, not the gap
documented, because an append-only ledger that cannot name the row an
evaluation changed is the less useful audit trail.

The new CHECK is `window_start` and `meta_risk_id` set together, and always
set on `fired`. A non-fired row carries both when it refreshed a meta-risk
and neither when there was none.

- **Additive and safe on existing rows.** Every existing row satisfies the new
  CHECK (non-fired rows have both NULL), so nothing is rewritten. It is added
  `NOT VALID` and then validated, so the scan runs under `SHARE UPDATE
EXCLUSIVE` rather than the `ACCESS EXCLUSIVE` of a plain `ADD`.
- **The down migration fails rather than rewrites.** A non-fired row that
  names a meta-risk cannot satisfy the old CHECK. Clearing the pointer would
  edit the append-only ledger, so the down stops and an operator decides, as
  #1746's down does.
- **`window_start` comes along with the pointer.** It records the window the
  refreshed score was computed over, which the meta-risk's own
  `inherent_score.window_start` also holds.

### D5: A 0-child meta-risk is labelled "Pattern no longer met"

The engine never closes a meta-risk (canvas §6.4), so one whose last child
left sits open with severity 0, which `DeriveGridCell` maps to 1x1. The web
UI now labels it: a badge on the risks list row and an alert on the risk
page, both keyed on `inherent_score.rule_generated === true` and
`child_count === 0` (`patternNoLongerMet` in `web/app/(authed)/risks/filters.ts`,
covered by vitest).

- **Label, not a lifecycle change.** Auto-closing would break the canvas
  §6.4 rule and the narrow-writer bound. The label tells the operator; the
  operator decides.
- **The operator doc** now says that ageing out happens on the next
  evaluation that touches the theme, not on a timer, and that `evaluate`
  brings every meta-risk up to date.

## Re-evaluate

- **Residual out-of-order case.** The locks are chosen from an unlocked read
  of the risk's themes. If a concurrent theme write commits between that read
  and the locked read, the after-write evaluation may take a rule lock the
  write did not take up front. Likewise a rule activated between the
  pre-lock and the evaluation. Both need two concurrent writes to the same
  risk, or a write racing an activation, and the worst result is a 40P01
  retry rather than wrong data. Closing it fully would need a retry loop
  around the pre-lock; not done here.
