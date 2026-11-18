# 2359 — The controls Freshness filter offers only real states: decisions log

Issue: mgoodric/security-atlas#2359. The `/controls` Freshness filter offered
"Expired", but no evaluation can carry it: `control_evaluations` has
`CHECK (freshness_status IN ('fresh', 'stale', 'no_evidence'))`, and
`internal/eval/state.go` writes only those three. The value came from
`ENUM_LABELS.FreshnessState`, which nothing held to the backend, and from
two hand-written allow-lists (`internal/api/controls/list.go`,
`web/app/(authed)/controls/server-query.ts`) that both accepted it.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found while building #2347.
- **detection_tier_target:** unit. The label group now comes from
  `check-enums.golden.json`, which a vitest test holds the options to.

## Decisions

### D1 — remove "expired" rather than map it to a real state

There is no state for it to mean. `stale` is a real state with its own
option, so mapping would show two options with the same result.

### D2 — the source of truth is the CHECK, read through the #2328 golden

`FreshnessState` joins `checkEnumColumns` in
`internal/db/check_enums_integration_test.go`, so
`web/lib/contracts/check-enums.golden.json` records the
`control_evaluations.freshness_status` values from the migrated database.
The existing label test then fails on a missing label, and
`server-query.test.ts` fails unless the page's options
(`enumOptions("FreshnessState")`) and the query's accepted values
(`FRESHNESS_VALUES`, now derived from the same label keys) equal the golden
exactly. The Go allow-list uses the `eval.Freshness*` constants.

### D3 — an old `?freshness=expired` link shows all controls

`normalizeFilterValue` already widens an unknown value to `ALL` rather than
failing the page, so an old link or saved view with `freshness=expired`
reads as no freshness filter. It matched nothing before, so no saved view
loses rows it used to show. Mapping it to `stale` was the alternative; it
would silently change what an old view means.

### D4 — the API returns 400 for `freshness=expired`

`GET /v1/controls?include=state&freshness=expired` was accepted and matched
nothing. It is now refused like any other unknown value, with the message
`freshness must be one of fresh|stale|no_evidence`. The web never sends it
(D3), so only a hand-built API call sees the change.

### D5 — the rollup SQL's dead `'expired'` rank is left in place

`internal/db/queries/controls.sql` ranks `'expired'` as worst in two CASE
expressions. The CHECK makes that branch unreachable, so it changes no
result. Removing it means regenerating `internal/db/dbx` with the pinned
sqlc, for no behaviour change; it is out of this ticket's scope.
