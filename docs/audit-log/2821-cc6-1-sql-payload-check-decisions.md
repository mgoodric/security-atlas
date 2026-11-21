# #2821 — cc6_1 phishing-resistant MFA, the first stock SQL payload check (decisions log)

Parent: [#1860](https://github.com/mgoodric/security-atlas/issues/1860).
Implements ADR 0028 tier 1, check 1. This is the tracer: later tier tickets
copy the pattern in D1.

- detection_tier_actual: none
- detection_tier_target: unit

No bug surfaced while building. The check it restores was lost in #1858
because nothing ran the old Rego against real input; the bundle tests and
the guard test below are the unit-tier net for the checks that follow.

## D1 — The pattern every later #1860 check follows

A stock payload check is four things, all in one PR:

1. **The query.** `language: sql` with an `evidence_kind`, so the engine
   hands it only records of that kind (#2818). It returns one boolean (or
   one result string) for the control, not one row per record. It is
   wrapped in `coalesce(..., false)` so an empty record set returns `false`
   rather than NULL, which the SQL evaluator rejects as an error
   (`classifySQLValue`); the engine then turns an empty window into
   `inconclusive` itself. It reads only `payload` and `observed_at`, never
   `result`: the connector's verdict is not the check.
2. **Bundle tests** in `controls/soc2/<bundle>/tests/*.yaml`, with at least
   one `pass`, one `fail` and an empty-ledger `inconclusive` case.
   `TestStockSQLChecksShipBundleTests` fails a SQL stock bundle that lacks
   any of the three. The records carry `evidence_kind` and the `result` the
   connector would emit, so a case shows the query overruling it. The
   upload gate (`internal/api/controls/gate.go`) runs these on every
   bootstrap upload inside a read-only tenant transaction.
3. **An evaluation test.** `TestStockSQLBundleTests_PassAgainstPostgres`
   runs every SQL stock bundle's tests/ through `bundletest.Run` against
   Postgres, so a new check is covered by adding its bundle tests. A check
   ticket also adds a live-engine case to
   `internal/control/stock_sql_checks_integration_test.go`: seed the stock
   control row from `control.yaml`, seed `evidence_records` of the kind,
   call `Engine.EvaluateControl`, read `ControlState`.
4. **The description** says what runs, field by field, and names what is
   not checked (ADR 0028 decision 6).

The result-only roll-up tests (`TestStockControlPoliciesEvaluateOnFreshInstall`,
`TestStockControlsKindFilterChangesOnlyCrossKindReads`) skip SQL bundles:
they assert properties of a roll-up over `result`, which a payload check is
not, and they run with no database.

Two sandbox constraints shape the SQL (`internal/eval/sql.go`):

- The author SQL is appended after the engine's own `WITH evidence AS (...)`,
  so it cannot open with a second `WITH`. Use a subquery in `FROM`.
- The static guard rejects any `word.word` outside `evidence.<col>`,
  including in aliases and comments. Name columns unqualified
  (`AS grp(group_name)`, then `group_name`), and keep dotted kind names out
  of the expression.

## D2 — Each policy's latest snapshot decides

The connector emits one snapshot per policy per run, so a daily window can
hold two snapshots of one policy. The query takes the latest per
`policy_id` (`DISTINCT ON ... ORDER BY observed_at DESC`). Otherwise a
policy that lost WebAuthn this morning would still pass on last night's
snapshot. The control passes when any policy's latest snapshot qualifies.

## D3 — Phishing-resistant means `webauthn`; `okta_verify` does not count

The ticket names WebAuthn, or Okta Verify with number matching. The
snapshot's `factors_allowed` is the list of factor keys from the policy's
`settings.factors` (`connectors/okta/internal/oktapolicy`), so it can say
`okta_verify` is allowed but not whether number matching is on. Counting
`okta_verify` would pass plain push, which the ticket's fail case rules
out. So only `webauthn` counts, and the description says Okta Verify with
number matching is not seen. Making it count needs the connector to emit
the number-matching setting, under a new schema version (ADR 0028
decision 8). That is a tier-2 change.

**Follow-up:** the connector doesn't emit `webauthn` on real tenants.
[#2901](https://github.com/mgoodric/security-atlas/issues/2901) fixes the
connector side: `fido_webauthn` and authenticators. Until it lands, the
description says the control fails on real Okta tenants.

**Unverified:** that Okta returns WebAuthn under the key `webauthn` in an
`MFA_ENROLL` policy's `settings.factors`. I took it from the connector's
own tests (`oktapolicy_test.go`); I did not check Okta's API docs or a
live tenant. An org whose key differs would fail this control.

## D4 — "Everyone" is matched by name, case-insensitively

The query looks for `everyone` in `applies_to_groups`, ignoring case. The
connector copies `conditions.people.groups.include` verbatim, and its tests
and the old Rego both used the literal `everyone`.

**Unverified, and worth checking before relying on this:** Okta's policy
API may put group IDs (`00g...`) in `include`, not names. If it does, a
real tenant's Everyone group never matches and this control fails for
everyone. The fix would be in the connector (resolve the Everyone group's
ID, or emit a flag), not in this query.

**Follow-up:** the review confirmed the connector reports group ids.
[#2901](https://github.com/mgoodric/security-atlas/issues/2901) fixes that
in the connector, along with policy status, so inactive policies stop
counting. Matt chose to merge this PR first.

## D5 — Supersedes the stock bundle `soc2_cc6_1_logical_access_baseline`

The query's `language`, `expression` and `id` change, so the bundle
supersedes on the next boot (#1908): a new control id, with owners,
evidence, evaluations, exceptions and links left on the old row. That is
expected for a tier ticket (#2820 D1). Since it supersedes anyway, the
other strings #2820 left stale are fixed here too:

- query id `idp_mfa_policy_enabled` becomes
  `mfa_policy_everyone_phishing_resistant`, which is what it checks;
- the query description drops "for admins", which nothing checks;
- the title "enforces MFA on all org-managed applications" becomes
  "requires phishing-resistant MFA for everyone". The policy snapshot says
  nothing about applications. `docs-site/docs/framework-setup.md` quotes
  the title and is updated with it;
- the Rego input comment is gone with the Rego.

The changelog fragment carries the upgrade note.
