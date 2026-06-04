# Slice 426 — decisions log

Targeted coverage-lift round across five low-floor surfaces. Type: **AFK**
(autonomous, no subjective product calls). This log is the audit trail for
the floor lifts per the orchestrator brief — it records which packages were
lifted, by how much, the measured-vs-floor margins, and the
detection-tier classification (slice 353 Q-13).

## Context

The five lowest non-excluded floors in `cmd/scripts/coverage-thresholds.json`
were clustered on user-facing / eval-adjacent surfaces left low by the
enrolment-only drain batches (which enrolled pre-existing integration suites
without adding pure-Go branch tests). Each package gained a `helpers_test.go`
following the slice-353 Q-2 pure-Go fast-loop convention (validators,
normalizers, formatters, predicate guards, pre-transaction input checks,
handler deny branches) — no Postgres, no `//go:build integration` tag.

Measurement replicated the slice-279 merged-profile flow the CI gate uses:

```
go test -coverpkg=<5 pkgs> -coverprofile=unit.cov   <5 pkg globs>
go test -tags=integration -p 1 -coverpkg=<5 pkgs> -coverprofile=int.cov <5 pkg globs>
go run ./cmd/scripts/coverage-gate -profile=unit.cov -extra-profile=int.cov
# → coverage-gate: checked 5 packages, 0 failed … ALL CHECKS PASS
```

against a dedicated, freshly-migrated Postgres (`security-atlas-pg-426`,
bootstrap roles + all 66 forward migrations applied; `atlas_app` granted the
catalog-table privileges the CI Leg-A seed otherwise provides).

## D1 — Floors lifted (the load-bearing record)

Floor formula: `max(0, floor(merged_measured − 2pp))` (slice 069 P0-A4 /
the thresholds-file `$methodology`). Monotonic ratchet — every lift is up,
none above measured.

| Package                   | Old floor | Measured (merged) | New floor | Margin (measured − floor) | Statements |
| ------------------------- | --------- | ----------------- | --------- | ------------------------- | ---------- |
| `internal/api/decisions`  | 18        | 39.52%            | **37**    | +2.52pp                   | 248        |
| `internal/api/me`         | 43        | 52.14%            | **50**    | +2.14pp                   | 443        |
| `internal/api/policies`   | 39        | 51.33%            | **49**    | +2.33pp                   | 413        |
| `internal/freshnessdrift` | 19        | 26.44%            | **24**    | +2.44pp                   | 87         |
| `internal/policy`         | 35        | 72.75%            | **70**    | +2.75pp                   | 367        |

All five pass the gate at the new floors (`0 failed`). No other package's
floor was touched (sibling agents own the rest of the batch).

## D2 — Why `freshnessdrift` lifted least (+5pp, not to-tier) — AC-6 residual

`internal/freshnessdrift` is 87 statements, and the bulk of them live in
`Scheduler.Run`, `SweepOnce`, and `RefreshSubscriber.Start` — paths that
enumerate tenants (`dbx.ListTenantsWithActiveControls`), open transactions,
and bind a durable JetStream consumer. `Scheduler.Run` fires an immediate
sweep on start with **no nil-pool short-circuit**, so it cannot be unit-tested
without a real Postgres (a nil-pool unit attempt panics in
`pgxpool.Pool.Acquire`). The pure-Go residue is the constructor nil-logger
guards, the no-op `discardWriter`, the `RefresherFactory` wiring, and the
published constants — all now covered. The remaining branches are genuinely
integration-territory and stay in `integration_test.go`; the residual is
documented in the `helpers_test.go` header rather than over-lifting the floor
(AC-6, P0-426-2). This is the honest call: a 26.44% floor backed by real
assertions beats a vanity number.

## D3 — Deny-branch discipline on the tenant/role-touching packages (P0-426-3)

`policy`, `internal/api/policies`, and `internal/api/me` touch
tenant/role/identity, so per the brief their new tests assert the **deny**
branch, not happy-path-only:

- `me`: `authnContext` denies missing-credential / empty-TenantID /
  no-tenancy-context; the audit-period handlers return 401 on missing context
  AND on an empty caller user id (the identity-resolution rejection AC-4
  names).
- `policies`: every handler returns 401 on missing context; `Approve` /
  `Publish` return 403 to a non-approver credential (the elevation deny
  branch); `tenantCredContext` deny branches asserted.
- `policy`: the `AckStore` pre-tx guards reject missing/invalid caller
  identity (`ErrAckMissingPolicyID`, `ErrAckMissingUser`, non-UUID user id)
  and `inTx` rejects a missing/invalid tenant context.

Every deny test is driven with a **nil store/pool** (P0-426-4): if any guard
path reached the DB it would panic on the nil pool, so a passing test proves
the rejection happens before any query — the guard is real, not faked.

## D4 — Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: **none** — no product bug surfaced during the
  slice. This is a coverage-lift round over already-merged, working code; the
  new tests assert existing behavior, they did not uncover a defect.
- `detection_tier_target`: **none** — N/A (no bug to classify).

One process note (not a product bug): the first draft of the
`freshnessdrift` `Scheduler.Run` unit tests assumed a nil-pool short-circuit
that does not exist; the nil pool panicked. Caught at the local `go test`
loop (unit tier) before commit and resolved by removing those tests and
documenting the integration-only residual (D2). Fix-forward rate impact:
none (caught pre-commit).

## Constitutional invariants honored

- **Slice 069 monotonic ratchet:** tests + floor lift in the same PR; every
  floor up, none above measured.
- **Slice 353 Q-2:** pure-Go-first; integration reached for only where a
  branch genuinely needs real services (the `freshnessdrift` residual).
- **Invariant #6 (tenant isolation at the DB layer):** the `policy` / `me` /
  `policies` deny tests reinforce the RLS/role/identity guards, never bypass
  them; no DB mock fakes RLS (P0-426-4).
- **Invariant #2 (ingestion/evaluation separation):** `freshnessdrift` is a
  read model; its tests read, never write source-of-truth evidence.
