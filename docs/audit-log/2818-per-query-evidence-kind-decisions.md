# #2818 — each evidence query sees only its declared evidence_kind (decisions log)

Parent: #1860. Implements ADR 0028 decision 5. No migration.

- detection_tier_actual: none
- detection_tier_target: unit

## D1 — the filter sits in the query dispatch, not the ledger read

`evalQueries` (`internal/eval/engine.go`) narrows the in-window records to the
query's `evidence_kind` with `recordsOfKind` (`internal/eval/state.go`) just
before it hands them to the rego, sql or jsonpath evaluator. The ledger read,
the freshness window, `evidence_count_in_window`, `last_observed_at` and the
no-evidence override still work on all of the control's records. Freshness is
a property of the control, not of one query, so a control with one stale kind
and one fresh kind stays fresh, as before. `latestAttestationWins` runs before
the filter, so the latest attestation is picked over the whole control.

The fixture path (`EvaluateFixture`) calls the same `recordsOfKind`, so a
bundle's tests filter the way a live evaluation does.

## D2 — a query that declares no kind keeps today's behaviour, with a warning

As ADR 0028 says, a query with no `evidence_kind` still sees every record the
control receives. Refusing such a query at upload would break tenant bundles
that upload today. Instead `Manifest.UnkindedQueryWarnings`
(`internal/control/validate.go`) returns one warning per kindless query. Two
surfaces carry it:

- the upload response's `warnings` (`Store.Upload`, beside
  `SCFReleaseDeprecationWarning`);
- `atlas-cli controls validate`, as a `WARN` line.

Neither fails. Every stock query declares a kind (36 of 36), and
`TestStockQueriesDeclareEvidenceKind` keeps it that way.

## D3 — a live record with no kind reaches only a kindless query

`evidence_records.evidence_kind` is nullable. A record with no kind matches no
declared kind, so a query that declares one no longer sees it. It still counts
for freshness and for a kindless query.

## D4 — a fixture record with no kind counts as every kind

Fixture records gained an optional `evidence_kind`. When it is omitted the
record matches every query, whatever kind the query declares. Without this,
every existing test case of a bundle with kinded queries would see no
records, and its expected state would turn inconclusive, although nothing
about the bundle changed. A single-kind bundle has no reason to repeat its
kind on every record. Authors set it when a control has queries over two
kinds. This is the one place the fixture path differs from the live one
(D3), and `docs/spec/control-bundle.md` documents it.

## D5 — stock results that change

Every stock query is the same roll-up of per-record `result`: any fail, else
any pass, else any inconclusive, else na, and inconclusive over no records.
The control combines its queries' results with the same precedence. So for a
one-query control, filtering changes the result only by dropping records of
another kind. For a two-query control, splitting the records by kind changes
nothing while both kinds are present. These are the cases that do change, and
in each one a query was reading another kind's records:

| Case                                                                                                   | Controls                                                                                                                                                                                      | Before                            | After                                                          |
| ------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------- | -------------------------------------------------------------- |
| A record of a kind no query declares reaches the control (pushed with its control id or `control_ref`) | any control with queries                                                                                                                                                                      | the stray record's result counted | ignored by every query; it still counts for freshness          |
| A two-query control receives records of only one of its kinds, and every one of them is `na`           | `soc2_cc1_4_competence_training` (`jira.ticket_evidence.v1`, `security_awareness.training_completion.v1`), `soc2_p4_2_retention_schedule` (`slack.retention_settings.v1`, `manual.upload.v1`) | `na`                              | `inconclusive`: the query over the missing kind has no records |
| A live record with no `evidence_kind` (D3)                                                             | any control with queries                                                                                                                                                                      | counted                           | ignored by every query                                         |

With one kind of a two-query control present and any result other than `na`,
the result is unchanged: the empty query's inconclusive ranks below pass and
fail, and equals inconclusive.

`TestStockControlsKindFilterChangesOnlyCrossKindReads`
(`internal/control/stock_kind_filter_test.go`) evaluates every stock control
before and after the filter over every combination of one record per declared
kind (absent, pass, fail, na or inconclusive), with and without a record of a
stray kind, and fails if a result changes while every declared kind is present
and no stray kind is. `TestStockTwoKindControls_MissingKindOnlyTurnsNAInconclusive`
pins the second row of the table, and fails if the set of two-query stock
controls changes.

The second row is the intended reading. A control that declares two kinds of
evidence and has received only one of them has not shown that both apply, so
`na` from one kind should not stand for the control.

Later addition (#2935): `soc2_a1_1_capacity_monitoring` became a third
two-query stock control, over `osquery.host_posture.v1` and
`monitoring.host_capacity.v1`. Both queries are SQL and neither returns
`na`, so the second row cannot occur for it. Its bundle `tests/` cases cover
the cross-kind combination
(`docs/audit-log/2935-datadog-host-capacity-decisions.md` D4).

## D6 — manual attestations are unaffected for stock controls

`manual.attestation.v1` records are written only for `manual_attested` and
`manual_periodic` controls (`isManualImplementation`,
`internal/api/controls/attest.go`), and no stock control of either type has an
evidence query, so they take the per-record roll-up, which is not filtered. A
tenant bundle that is manual and also declares a kinded query will no longer
have its attestations read by that query. It should declare
`manual.attestation.v1` on a query, or no query, if it wants them read.
