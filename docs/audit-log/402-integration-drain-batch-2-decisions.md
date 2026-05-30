# Slice 402 — integration-enrolment drain batch 2 (decisions log)

**Type:** AFK · **Parent:** 390 · **Cluster:** infra

Batch 2 of the slice-390 drain. Enrols the five admin-credentials-surface
packages that carry a `//go:build integration` tag but were absent from the
`tests-integration` job's package list in `.github/workflows/ci.yml`
(catalogued by the slice-345 guard's `KNOWN_UNENROLLED` allowlist). Mirrors
the slice-401 method (depended on 401 landing first so the batches stay
sequential and CI stays green between them).

## Scope

```
internal/api/adminauditlog
internal/api/adminauthzbundle
internal/api/admincreds
internal/api/admindemo
internal/api/adminsso
```

## Method

- Stood up a local harness mirroring the CI integration job: fresh
  `postgres:16-alpine` container, `migrations/bootstrap/01-roles.sql`, all 66
  forward migrations applied in order, `atlas_app` password set, plus MinIO +
  NATS JetStream available for the full-suite measurement. (None of these 5
  packages touch MinIO/NATS directly — they are DB-backed handler suites — but
  the services were up to match CI exactly.)
- Ran `go test -tags=integration -p 1 -v ./internal/api/<pkg>/...` per package
  and confirmed real `=== RUN` lines with **zero `--- SKIP`** in the final
  state. Two packages failed on first run (see below) and were fixed correctly
  — no skip, no delete.
- Measured per-package own-suite coverage with the toolchain's `-cover`. For
  these five, own-suite IS the real floor: three packages
  (`adminauditlog`, `admincreds`, `adminsso`) ship NO unit-test files, so the
  merged unit+integration profile equals the integration own-suite profile —
  there is no transitive-load phantom to inflate the number (contrast slice
  401's `internal/auth/users`, whose own-suite was 20% but merged was driven by
  other packages' tests). `admindemo` (2 unit files) and `adminauthzbundle`
  (1 unit file) carry small unit suites on top, but their coverage is still
  dominated by their own tests, not transitive load. All five floors are
  therefore genuinely earned and anchored conservatively to the own-suite
  number at `floor(own_suite − 2)`.

## Per-package outcomes

| Package                         | Broke?  | Own-suite cov | excludes action       | Floor        |
| ------------------------------- | ------- | ------------- | --------------------- | ------------ |
| `internal/api/adminauditlog`    | YES (3) | 70.8%         | lifted off `excludes` | **68**       |
| `internal/api/adminauthzbundle` | no      | 85.0%         | n/a (never excluded)  | **83** (new) |
| `internal/api/admincreds`       | YES (2) | 64.4%         | lifted off `excludes` | **62**       |
| `internal/api/admindemo`        | no      | 64.9%         | lifted off `excludes` | **62**       |
| `internal/api/adminsso`         | no      | 65.1%         | lifted off `excludes` | **63**       |

### `internal/api/admincreds` — 2 tests broke; fixed test fixture

`TestRevokeInvalidates` and `TestRotateGivesSuccessor` failed with
`Revoke/Rotate: want 204/200, got 400: {"error":"invalid credential id"}`,
preceded by a logged `status=500 ... error="tenancy: no tenant in context"` on
the `Issue` call that seeds each test.

**Root cause (stale TEST, not a product bug).** `newHandler`'s test router
injected the admin credential via `authctx.WithCredential` but did NOT run
`tenancymw.Middleware`. Since the slice-033 contract, the store's
`apikeystore.Store` opens a tenant-scoped tx and calls `tenancy.ApplyTenant`,
which requires `tenancy.WithTenant` on the request context — seeded by
`tenancymw.Middleware`, which derives the tenant from the credential. Without
that middleware every handler that opens a tenant tx returns 500
("no tenant in context"); `Issue` then returned an empty `resp.ID`, and the
follow-up Revoke/Rotate received an empty `{id}` path param → `parseCredID` 400. The test had carried this latent gap since slice 033 because it never ran
in CI.

Confirmed it was test staleness, not a regression, by isolating
`TestIssueReturnsBearerExactlyOnce`: it ALSO failed in isolation but passed in
the full run only via prior-test side effects — a classic order-dependent
false green that the never-run state hid.

**Fix.** Added `r.Use(tenancymw.Middleware)` to `newHandler`'s router, after
the credential-injection middleware — mirroring production and the sibling
admin integration suites (`adminsso`, `admindemo`, which already do exactly
this). 7/7 green after the fix. `TestIssueRequiresAdmin` builds its own router
and was left untouched (it asserts a 403 before any store call, so it needs no
tenant context).

### `internal/api/adminauditlog` — 3 tests broke; fixed a REAL product bug

The three slice-135 export tests failed:

- `TestSlice135_ExportEndpointReusesUnifiedAggregator`: exported 7 of the 9
  seeded kinds — missing exactly `feature_flag` and `me`.
- `TestSlice135_RowCapEnforced413`: got 200, want 413 — the 100 001-row
  `me_audit_log` seed never tripped the cap.
- `TestSlice135_AuditPeriodFreezingClampsWindow`: export body was `[]`; the
  pre-frozen `me_audit_log` row was absent.

**Root cause (genuine PRODUCT bug, fixed here).** `unifiedlog.Query` takes a
`CallerIsPrivileged` flag (slice 270): when `false` it hides `feature_flag`
rows and restricts `me` rows to those whose `actor_id` equals `CallerUserID`
(the non-privileged shape consumed by `/v1/activity/unified`). The slice-124
unified-list handler (`unified.go:161`) sets `CallerIsPrivileged = true`
unconditionally — every caller that passes its `{admin, auditor,
grc_engineer}` admit gate is privileged. The slice-270 activity handler
(`activity.go:133`) derives it from the role probe. **The export handler
(`export.go`) set it NOWHERE**, so it defaulted to the zero value `false`.

Effect: the audit-log **export** — a forensic / evidence artifact, gated to
exactly the same privileged role set as the unified list — silently omitted
`feature_flag` audit rows entirely and dropped every `me` row not authored by
the caller. This is a forensic-completeness / audit-integrity defect, not a
test artifact: an auditor exporting the trail would receive a quietly
incomplete record.

**Why fix here rather than spillover-and-skip.** The slice's spillover policy
covers a genuine product bug that needs separate design work. This one does
not: the correct value is unambiguous (the export's admit gate is identical to
the unified list's, so it is privileged by construction), and the fix is a
one-line mirror of the already-shipped, already-reviewed `unified.go:161`
pattern. It falls under the slice's primary directive — "fix code if a real
regression" — so it is fixed in-place and called out prominently here as a
security-relevant finding for orchestrator visibility.

**Fix.** Set `clampedParams.CallerIsPrivileged = true` in `export.go` before
the `unifiedlog.Query` call, with a comment tying it to the identical
`unified.go` decision and to the slice-270 visibility model. All three tests
plus the rest of the suite went green; the 413 test now trips correctly
because the 100 001 `me` rows are no longer filtered out, and the freezing
test sees the pre-frozen `me` row. No test was skipped or deleted.

### `internal/api/adminauthzbundle`, `internal/api/admindemo`, `internal/api/adminsso` — green on first run

All three suites passed on first enrolment against the real harness; no test
broken. `adminsso` and `admindemo` already ran `tenancymw.Middleware` in their
test routers (the pattern `admincreds` was missing). Floors added per the
own-suite-anchored math above.

## Tests fixed

- `internal/api/admincreds/http_integration_test.go` — added
  `tenancymw.Middleware` to `newHandler`'s router (stale-test fix).
- `internal/api/adminauditlog` — fixed the underlying product code
  (`export.go`), not the test; the three slice-135 export tests were correct
  and now pass.

## Product fix

- `internal/api/adminauditlog/export.go` — set `CallerIsPrivileged = true` on
  the export's aggregator call so the forensic export renders the full
  privileged view (all nine kinds, all `me` rows), matching the unified-list
  endpoint it shares an admit gate with. Closes a silent forensic-completeness
  gap in the audit-log export.

## Spillover

**None.** The one genuine product bug (export `CallerIsPrivileged`) was a
one-line consistency fix mirroring an existing shipped pattern, fixed in-place
per the "fix code if a real regression" directive. No separate-design-work bug
remained to file.

## Coverage dispositions

All five floors are REAL (own-suite-anchored), no phantoms:

- `adminauditlog` 68 = floor(70.8 − 2); 0 unit files → merged == integration
  own-suite, no transitive inflation.
- `adminauthzbundle` 83 = floor(85.0 − 2); never on excludes, new floor.
- `admincreds` 62 = floor(64.4 − 2); 0 unit files → no phantom.
- `admindemo` 62 = floor(64.9 − 2); 2 small unit files, own-test dominated.
- `adminsso` 63 = floor(65.1 − 2); 0 unit files → no phantom.

The four that were on `excludes` (`adminauditlog`, `admincreds`, `admindemo`,
`adminsso`) are lifted off and their `$exclude_justifications` entries removed.
`adminauthzbundle` was never excluded; it gains its first hard floor.

## KNOWN_UNENROLLED shrink

33 → 28 (removed the five batch-2 packages). Guard self-test 12/12 pass;
`./scripts/audit-integration-enrolment.sh` reports OK with 28 waived.

## Files touched

- `.github/workflows/ci.yml` — 5 package entries added to the integration job.
- `scripts/audit-integration-enrolment.sh` — 5 entries removed from
  `KNOWN_UNENROLLED` (33 → 28).
- `cmd/scripts/coverage-thresholds.json` — 5 floors added
  (`adminauditlog 68`, `adminauthzbundle 83`, `admincreds 62`,
  `admindemo 62`, `adminsso 63`); 4 removed from `excludes` +
  their `$exclude_justifications` entries.
- `internal/api/admincreds/http_integration_test.go` — `tenancymw.Middleware`
  added to the test router (stale-test fix).
- `internal/api/adminauditlog/export.go` — `CallerIsPrivileged = true` on the
  export aggregator call (product-bug fix).
- `CHANGELOG.md` — Unreleased › Changed/Fixed entries.
- `docs/audit-log/402-integration-drain-batch-2-decisions.md` — this file.
