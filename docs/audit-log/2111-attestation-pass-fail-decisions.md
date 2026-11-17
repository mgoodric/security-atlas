# #2111 — attestation pass/fail, latest attestation wins (decisions log)

Parent: #2071 (finish the record lifecycles). Builds on #2110 (Evaluate
now). No migration.

- detection_tier_actual: none
- detection_tier_target: unit

## D1 — schema 1.2.0 is additive; the fail rule lives in the schema and the handler

`manual.attestation/1.2.0.json` adds two optional properties, `result`
(`pass` | `fail`) and `fail_reason` (non-empty string), plus an
`if result == fail then required fail_reason` clause. Nothing new is
top-level required, so `CheckAdditiveOver(1.1.0, 1.2.0)` accepts it and every
1.0.0 / 1.1.0 record keeps validating against its own recorded version.

The rule is enforced twice, as asked: the JSON schema (so a pusher that skips
the attest handler and sends a 1.2.0 record cannot store a reasonless fail)
and the attest handler (`attestResult`, so the browser gets a plain 400 that
names `fail_reason` before anything is read or written). The handler also
trims the reason, so whitespace is not a reason.

A missing `result` is a pass, in the handler and by definition for old
records. A `fail_reason` sent with a pass is dropped rather than refused: the
form clears its error when the user flips back to pass, and refusing would
only punish a stale text box. #1742's period fields are left for 1.3.0.

## D2 — the record's `result` column is the attested outcome

The handler used to hard-code `Result_RESULT_PASS`. It now writes the
submitted result to both the ledger `result` column (what evaluation reads)
and `payload.result` (what the schema validates), from one value, so the two
cannot disagree for records this handler writes. A pusher could still send a
1.2.0 payload whose `payload.result` differs from the envelope result; the
engine reads the column, as it does for every kind. Cross-checking the two at
ingest is a general ingest concern, not attestation-specific, and is not
added here.

## D3 — "latest wins" is a pure step before the rollup, with an explicit order

`latestAttestationWins` (`internal/eval/state.go`) runs on the in-window
records before the queries / `computeResult`. Among `manual.attestation.v1`
records it keeps only the latest; every other record passes through in its
original order. So automated evidence keeps "any fail fails", and the
attestation outcome is one vote in that rollup.

"Latest" is compared explicitly, not taken from SQL order:

1. `observed_at`, later wins;
2. `ingested_at`, later wins, on an `observed_at` tie;
3. record id (string compare), greater wins, when both tie.

`ListEvidenceForControlAsOf` now selects `evidence_kind` and `ingested_at`
and orders by `observed_at DESC, ingested_at DESC, id DESC`, so the ledger
read is stable too, but correctness does not depend on it: the unit tests
feed records in both orders.

Clock skew: an attester may set `observed_at` (the handler accepts it, as
before). The attestation claiming the later moment wins even if it was
ingested earlier, which is the honest reading of "the latest attestation".
`ingested_at` is server time and only breaks ties. Tested in
`TestLatestAttestationWins` ("skewed clock", "equal observed_at", "equal
observed_at and ingested_at", both input orders).

`evidence_count_in_window` still counts every in-window record, including
superseded attestations: it describes the evidence, not the vote.

The fixture runner (`EvaluateFixture`) calls the same step so a bundle test
case evaluates the way the live engine does; fixture records carry no kind
today, so it is a no-op there until they do.

## D4 — the attest handler evaluates the control itself

The ticket says the control "evaluates immediately (through the ingest
subscriber, or evaluate-now when the message bus isn't configured)". The
attest handler writes the ledger with `ingest.Service.Process` directly; it
never publishes to the JetStream stream the `IngestSubscriber` consumes, with
or without NATS. So the subscriber never sees an attestation, and relying on
it would leave the control unevaluated until the hourly scheduler.

The handler therefore runs the engine's `EvaluateControl` (trigger `ingest`,
as-of far future) right after a successful write, in every configuration.
That is the evaluate-now path, run server-side. It is detached from request
cancellation and bounded to 30s (Evaluate now's bound). A failure is logged
and reported as `evaluated: false` in the 201; the attestation is already
recorded and is not rolled back. Wiring: `register_controls.go` gives the
handler an engine over the same pool when one is attached.

Calling `POST /v1/controls/{id}/evaluate` from the browser instead was
rejected: that endpoint admits only an admin or the control's assigned owner,
while attesting admits any holder of the control's owner role, so a
legitimate attester could be refused (D5). Publishing attestations to the bus
was rejected for this ticket: it changes the attest response from a ledger
receipt to an async publish and touches the slice-015 anti-criterion that the
publisher is the only ledger path when wired.

If #2479 changes `EvaluateControl` (rate limiting, `triggered_by`), this call
site is the second caller after the Evaluate-now handler to update.

## D5 — who can attest is unchanged

Today an attestation needs a credential that holds the control's
`owner_role` (`credstore.Credential.HasOwnerRole`), with tenant admin as the
wildcard; the control must be `manual_attested` or `manual_periodic`; the
tenant boundary is RLS. This change does not widen that. The post-attest
evaluation is a consequence of writing evidence (as the subscriber's would
be), not a separate permission, and it evaluates only the control just
attested.

The **Attest** link shows on every manual control. The attest page already
renders a banner and a disabled submit when `caller_can_attest` is false, so
the link is not hidden per caller.

## D6 — the control page waits with a bound

The form returns to `/controls/<id>?attested=<ingested_at>`. The header shows
the control's rolled-up result (fail, then pass, then excepted, then
inconclusive, else na, the engine's precedence) and, while no state entry has
`evaluated_at >= ingested_at`, "Evaluating…" instead, re-reading the state
every second. After 30 seconds it stops polling and shows an error that names
Evaluate now and the scheduled run; it never spins forever. When the state
lands the parameter is removed from the URL. An unparseable parameter counts
as landed. The page did not show the control's pass/fail before this change;
the result badge is new.

The result defaults to **Pass** in the form. A fail is the case that needs
more input (the reason), and the existing flow, where an attestation was
always a pass, keeps its one-field shape.

## D7 — tests

- Engine (unit): `internal/eval/attestation_latest_test.go` — fail then pass
  → pass; pass then fail → fail; automated fail fails regardless; tie-breaks;
  input order.
- Schema (unit): `internal/api/schemaregistry/attestation_schema_test.go`.
- Handler (unit): `TestAttestSubmit_RejectsBadResult`, `TestAttestResult`.
- API (integration, real Postgres): `TestAttest_FailWithoutReason_Refused`,
  `TestAttestSchema_120_FailWithoutReason_Refused`,
  `TestAttest_Payload110_AcceptedAsPass`,
  `TestAttest_LatestAttestationDecidesControlState`.
- Web (vitest): `web/lib/attest-evaluation.test.ts`.
- Playwright: `web/e2e/attest-result.spec.ts` attests fail then pass against
  the real platform on its own seeded control.
