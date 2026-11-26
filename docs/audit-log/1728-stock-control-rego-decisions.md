# Issue #1728 — stock control Rego queries fail on a fresh install

Build-time calls made fixing
[#1728](https://github.com/mgoodric/security-atlas/issues/1728). On a fresh
self-host install the eval scheduler logged
`rego evidence query failed: policy did not assign 'result'` for one control.

`detection_tier_actual = production` (a first-run adopter install) ·
`detection_tier_target = unit` (a bundle-wide fixture test now covers it).

## What was wrong

The engine evaluates `data.evidence.query.result` with input
`{records: [{result}]}` (`internal/eval/rego.go`). All 27 Rego queries in the
26 `controls/soc2/` bundles that declare one were written as
`package atlas.controls.soc2.<id>` with an `allow` rule over payload-shaped
input (`input.posture_records`, `input.policies`, ...). None of them assigned
`result`, so every one errored. Only one control showed in the log because
`EvaluateAll` returns on the first erroring control. The example in
`docs/spec/control-bundle.md` §3 taught the same wrong shape.

## D1 — Roll up per-record verdicts instead of porting the payload logic

**Decision.** Each stock Rego expression becomes `package evidence.query` with
a `result` rule that rolls up `input.records[].result` using the engine's own
precedence (`computeResult` in `internal/eval/state.go`): any fail, then any
pass, then any inconclusive, else na, and inconclusive on an empty ledger. The
query `id`, `evidence_kind` and `description` are unchanged.

**This is a semantic change, stated plainly.** The old `allow` bodies encoded
specific checks (disk below 85%, a phishing-resistant factor, a 24h revocation
window). Those checks never ran: the sandbox does not hand the payload to
Rego, and the field names do not match the registered payload schemas
either (for example `okta.mfa_policy.v1` has `mfa_required` and
`factors_allowed`, not `policies[].required_factors`). The check that does run
is the connector's: the Okta and osquery connectors compute a pass/fail
verdict per record before pushing it, and that verdict is what Rego sees. The
rollup makes the control state follow those verdicts.

**Rejected.** Widening the Rego input to include payloads and rewriting the 27
policies against the real schemas. That changes the sandbox contract (slice
054 / rego.go) and means authoring new control logic for 27 controls, which is
claim-sized work, not a fix. Porting the checks to JSON-path queries was
rejected for the same reason: it is new control logic, and the old field
names would not carry over.

## D2 — The test runs every stock bundle through `eval.EvaluateFixture`

**Decision.** `internal/control/stock_policy_eval_test.go` parses every
`controls/soc2/` bundle (the directory `deploy/docker/bootstrap/bootstrap.sh`
uploads on a fresh install) and evaluates its declared queries through
`eval.EvaluateFixture`, the seam the live engine shares, against an empty
ledger plus single pass, fail, mixed and na records. It asserts no error and
the expected rollup.

**Why.** The empty case is the ticket's fresh install. The non-empty cases stop
a query from passing by returning a constant. Running through the fixture seam
rather than a Postgres-backed engine keeps it in the unit tier, where this bug
should have been caught.

## Not changed

`EvaluateAll` still stops at the first erroring control. Making it continue
past one bad control is a separate behavior change and is left for its own
ticket.
