# 1861: Aggregation meta-risk identity, concurrency and staleness (decisions log)

Build-time calls made for issue #1861, from the #1848 review. Three slice 054
engine bugs became reachable once #1848 ran rules on risk writes:

- a rolling window that creates a new meta-risk every day after `window_days`;
- duplicate meta-risks from concurrent evaluations;
- stale links and scores after a theme is removed or a risk is deleted.

- detection_tier_actual: manual_review
- detection_tier_target: integration

(Found in review of #1848. The slice 054 suite never moved the clock past
`window_days`, never overlapped two evaluations, and evaluated with the theme
still on the risk. The three `_1861` tests in
`internal/risk/aggrule/meta_risk_lifecycle_integration_test.go` do. Each one
was run against the old behaviour and failed: the old window key, no lock,
and the hook with only the new themes.)

## Decisions made

### D1: A meta-risk is keyed on (rule_id, activated_at), not on the window

The window start moves forward every UTC day once `window_days` have passed
since activation, so `sha256(rule_id|window_start)` changed every day. The
key is now `sha256(rule_id|activated_at)`. It is fixed while the rule stays
active and changes only on re-activation.

- **Keying on the rule alone** was rejected. A re-activated rule would then
  pick its pre-deactivation meta-risk back up, and `DeactivateAggregationRule`
  promises that historical meta-risks are left untouched. Per-activation
  identity keeps that promise.
- **Old meta-risks still match.** The hashed string is the one the old key
  hashed during a rule's first `window_days`, when `window_start` was
  `activated_at` (both formatted RFC 3339 to the second). A meta-risk created
  in that period keeps matching after upgrade. No migration is needed.
- **Existing per-day duplicates are not cleaned up.** Installs that already
  ran a rule past `window_days` hold one meta-risk per day it fired after
  that. The engine stops adding to them and leaves them in place. Deleting
  risk rows automatically is not something the engine does (narrow writer).
  `docs/operator/aggregation-rules.md` tells operators to review and delete
  them.

### D2: The window is an attribute, and the meta-risk tracks it

Each evaluation re-links the meta-risk to exactly the eligible risks in the
current window. It unlinks children that left, whether by losing the theme or
by ageing out, and recomputes `inherent_score`, which also records
`window_start` and `child_count`. This happens below threshold too, so a
meta-risk never reports a child it no longer has. With zero scored children
the severity is 0. The meta-risk is never closed or deleted, as canvas §6.4
requires ("closing all children does not auto-close the parent").

Only links whose `rule_id` is this rule are removed. Manual slice 053 links
(`rule_id` NULL) and links from other rules are never touched. This adds
`UnlinkRiskAggregation` to the engine's narrow writer, limited to those
links.

### D3: Concurrency uses `pg_advisory_xact_lock`, not a unique index

The engine takes `pg_advisory_xact_lock(hashtextextended(key, 0))` before its
candidate read. Under READ COMMITTED, a second evaluation waits for the first
to commit. Its reads then see the first one's meta-risk, so it updates that
meta-risk instead of creating another.

- **A partial unique index on `(tenant_id, inherent_score->>'aggregation_key')`
  was rejected.** It needs a migration that fails on any install already
  holding a duplicate. Avoiding that would mean deduplicating, and so deleting
  or rewriting, risk rows inside a migration. The loser of a race would also
  get a 23505 and roll back the risk write that triggered it, unless the
  engine retried. The lock leaves the schema alone, and the race loser simply
  waits.
- **The lock is taken before the candidate read, not just around the
  insert.** That way the second evaluation also counts any risk the first
  one committed.
- **The precedent** is `internal/auth/users` (bootstrap admin), which
  serializes the same read-then-insert shape the same way.

### D4: Which writes run the hook (extends #1750 D2/D3)

- **Theme assignment and theme removal** run the hook with the union of the
  themes before and after the write. Before this change, removing a risk's
  last theme skipped the engine entirely. Removing one of several themes
  evaluated only the remaining ones.
- **Risk delete** runs the hook with the deleted risk's themes. The child's
  `risk_aggregations` row already cascades. The score did not.
- **Deleting a rule-generated meta-risk** does not run the hook. Evaluating
  would re-create the meta-risk in the same transaction the operator deleted
  it in. The next matching risk write re-creates it if the threshold still
  holds. That is the pre-existing behaviour and is documented for operators.

### D5: The clock is injectable

`Engine.WithClock` and `Store.WithClock` replace `time.Now`, so the
rolling-window test can step across more than `window_days`. Production
callers don't set it.

## Re-evaluate

- A broken active rule (for example a failing `custom_rego`) now also blocks
  theme removal and risk delete on its theme, not just creates and
  assignments. That follows from D4, and the operator doc covers it.
  Isolating rule failures, so that one bad rule records an error evaluation
  instead of failing the write, is a product call left for a follow-up.
