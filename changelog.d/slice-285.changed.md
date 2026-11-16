- **test(coverage):** slice 285 — `internal/oscal` coverage lift from
  41.4% to 71.1% unit-only. Three new white-box test files exercise the
  pure-data OSCAL marshalling surface that slice 279's audit flagged as
  unit-testable: `aggregate_proto_test.go` covers every branch of
  `sspInput` (scope cells, controls with + without ScfID, policies,
  statement template), `assessmentInput` (populations with + without
  frozen-horizon, walkthroughs, audit notes with + without ScopeID +
  CreatedAt), and `poamInput` (severity branches `high` for stale /
  no_evidence vs `moderate` otherwise; due-date branches
  LastObservedAt > EvaluatedAt > now; owner / title fallback for
  controls missing from the lookup map). `bridge_test.go` wires the
  generated `OscalBridgeServiceServer` onto a `bufconn` in-memory
  listener so the previously 0%-covered `grpcBridge` wrappers
  (`SerializeSSP` / `SerializeAssessment` / `SerializePOAM` /
  `RoundTripValidate` / `Close`) get full happy + RPC-error coverage
  without spawning Python. `extra_branches_test.go` covers the
  load-bearing rejection branches in `VerifyBundle` (unsupported
  algorithm, malformed public key / signature bytes / digest),
  `NewSigner` happy path, `uuidFromPg` invalid-input fallback,
  `WriteBundle` partially-zero-signature guard, and the `Export`
  `uuid.Nil` short-circuit ordering. No new dependencies — uses the
  existing `google.golang.org/grpc/test/bufconn` already in
  `go.sum`. The `internal/oscal` floor in
  `cmd/scripts/coverage-thresholds.json` ratchets from 39 → 69 per
  slice 069's `max(0, floor(measured - 2pp))` policy. Spillover from
  slice 279's coverage audit (`docs/coverage-audit-2026-05.md`).
