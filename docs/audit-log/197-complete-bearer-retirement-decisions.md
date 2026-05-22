# Slice 197 — JUDGMENT decisions log

**Slice**: 197 — Complete slice 034 bearer-middleware retirement
(test-fixture migration + final removal)
**Type**: JUDGMENT (per `Plans/prompts/04-per-slice-template.md`)
**Date**: 2026-05-21
**Author**: Claude (Opus 4.7), implementing per
`docs/issues/197-complete-slice-034-bearer-retirement.md`

## Why this log exists

JUDGMENT slices land subjective implementation decisions inline rather
than blocking the merge on a human sign-off. The slice doc enumerated
load-bearing decisions; each one is recorded below with the chosen path,
the alternatives weighed, and the confidence level so the maintainer
can iterate post-deployment.

## D1 — Test-fixture mint API shape

**Decision**: `Server.IssueTestJWT(t testing.TB, claims jwt.AtlasClaims) string`
on `*api.Server`, backed by a lazy-wired in-memory keystore +
`internal/api/testjwt/` claim builders.

**Alternatives weighed**:

1. **Server method + testjwt builders (chosen)**. One-line migration
   for the typical fixture site (`srv.IssueTestJWT(t,
testjwt.AdminFor(tenant))` replaces three lines of
   `IssueBootstrapAdminCredential` + error handling). The Server
   method lazy-wires `AttachJWTValidator` on first call with a
   deterministic test issuer + audience + a nil revocation store
   (the jwtmw middleware short-circuits revocation on nil per
   `jwtmw.Middleware` line 150). Subsequent calls reuse the same
   signer.
2. **testjwt-only, callers wire validator themselves**. Each test
   would need ~4 lines of boilerplate: open keystore, build signer,
   call `srv.AttachJWTValidator`, then mint. Multiplied across ~30
   files this is ~120 lines of boilerplate vs. the lazy-wire pattern.
3. **JWT injected via context bypass**. Tests skip the wire path and
   set `authctx.WithCredential` directly. Rejected: would diverge
   from production behaviour (the slice 190 JWT middleware fires in
   production; tests should exercise the same path) and would not
   catch JWT-shape regressions.

**Trade-off**: option 1 takes a deliberate `testing` import in
`internal/api/server_testing.go` (a non-`_test.go` file). The same
pattern is already used by `internal/api/testjwt/` and is widely
accepted Go idiom (the import is gated to the package's _test_ file
in non-test files); production code cannot accidentally call
`IssueTestJWT` because the `testing.TB` parameter is unobtainable
outside test scope.

**Confidence**: high. The lazy-wire pattern keeps the migration
mechanical and is reversible if a future slice (e.g. real OAuth-AS
in tests) wants to take over.

## D2 — Fail-closed credential gate (`requireCredential` middleware)

**Decision**: Add `requireCredential(exempt ...string)` middleware to
`internal/api/httpserver.go`, mounted AFTER the slice 190 JWT
middleware. It returns RFC 6750 §3-shaped 401 for any non-exempt
request whose context lacks `authctx.CredentialFromContext`.

**Why this was needed**: The slice 190 JWT middleware passes
requests with NO JWT shape through to the downstream chain (its
comment at line 119-125: "no JWT and no cookie → pass through. The
downstream chain (legacy bearer middleware or exempt-path bypass)
handles authentication"). With slice 197 removing the legacy bearer
mount, those requests would otherwise reach handlers
unauthenticated. The handlers do tenant-scoped queries and would
either error on missing-tenant GUC (500) or, worse, return rows
without RLS filtering.

The slice 191 design (P0-191-1) assumed the legacy middleware would
catch this case during the partial-cutover window; slice 197
restores the invariant at the platform layer with a dedicated
fail-closed gate.

**Alternatives weighed**:

1. **New requireCredential middleware (chosen)**. Stays inside
   `internal/api/` (slice 197's scope), reuses the exempt list
   pattern (mirrors `jwtBypass` and `legacyBearerDeprecation`
   exempts), gives a clean 401 response with `WWW-Authenticate`
   header.
2. **Modify jwtmw to fail closed when no token present**. Rejected:
   slice 187-190 code is out of slice 197's scope per the
   user-supplied constraints. Future slice could land this if the
   coexistence-with-legacy semantics are no longer needed.
3. **Let handlers fail naturally on missing tenant**. Rejected:
   handlers return 500 (`tenancy: no tenant in context`), which
   leaks an implementation detail to clients and is not the
   spec-grade 401 the platform should issue.

**Confidence**: high. The middleware is a 30-line function with a
mirrored exempt list; the change matches the pre-slice-197 behaviour
exactly (a missing bearer always got 401 from the legacy mount).

## D3 — Calendar.ics `?token=` URL path kept on credstore

**Decision**: `internal/api/calendar/integration_test.go` retains
ONE `srv.IssueBootstrapOwnerCredential` call site to mint a
credstore opaque bearer for the `/v1/calendar.ics?token=...` URL
parameter test. All other calendar tests (Authorization-header
gated) migrated to JWT.

**Why**: The `/v1/calendar.ics` handler is slice 091 calendar
subscription path. Calendar clients (Google / Apple / Outlook)
cannot exchange JWTs as URL parameters; they use opaque tokens
minted via POST `/v1/calendar/subscription`. The handler
authenticates the URL token via `h.creds.Authenticate(token)` —
a direct credstore lookup. JWTs are not in the credstore.

The `TestCalendarICS_RejectsNonCalendarScopeToken` test asserts
that a wrong-scope credstore token returns 403. With JWT, the same
test would 401 (credstore.ErrUnknownKey, because JWTs aren't in
credstore) and the test's scope-mismatch behaviour cannot be
exercised.

The fix keeps a documented credstore bearer for the URL-token test
case AND a JWT bearer for the Authorization-header test cases.
This is the cleanest expression of "calendar.ics keeps its slice
034 auth path" (slice 091 commitment); slice 197 only retires the
HTTP `Authorization: Bearer` credstore path, not the slice 091
URL-token path.

**Confidence**: high. The deviation is documented inline at the
test setup, and the calendar handler hasn't been changed.

## D4 — Subject claim as the JWT analog of RebindBearerUserIDForTests

**Decision**: Tests that previously called
`srv.RebindBearerUserIDForTests(bearer, userID)` now set
`claims.Subject = userID.String()` before calling
`srv.IssueTestJWT(t, claims)`.

**Why**: The `jwtmw` middleware synthesizes a `credstore.Credential`
from the JWT (jwtmw line 199-208). UserID on the synthesized
credential is `claims.Subject` (line 202). The legacy rebind hook
patched the in-memory credstore record's UserID after issuance; the
JWT analog is to set Subject at mint time.

Affected files:

- `internal/api/policyacks/integration_test.go` (3 call sites)
- `internal/api/policies/ack_rate_integration_test.go` (1 call site)
- `internal/api/me/profile_integration_test.go` (1 call site)

The legacy `bindBearerToUser` helper in policyacks was removed
entirely; `makeUserAndBearer` and `makeUserAndBearerWithUser` now
build claims with `Subject = user.String()` and mint via
`IssueTestJWT`.

**Confidence**: high. The JWT bridge's UserID-from-Subject mapping
is the published slice 190 contract; tests rely on the same
behaviour as production.

## D5 — `actor_id` / `credential_id` shape assertions relaxed

**Decision**: Two assertions in
`internal/api/controls/attest_integration_test.go` (lines 516 and 533) checked that `actor_id` / `credential_id` started with
`"key_"` — the legacy `credstore.Issue` credential ID prefix. With
JWT, the synthesized credential ID is `"jwt:" + claims.ID` (jwtmw
line 200) and UserID is `claims.Subject` (e.g.
`"test-owner:<uuid>"`). The assertions are relaxed to
non-emptiness checks.

**Why**: The `key_` prefix was a legacy implementation detail of
slice 034's credstore. Slice 197 retires that path; the prefix is
no longer relevant. The test's load-bearing assertion is that an
`actor_id` IS present (audit trail integrity), not its specific
string format.

**Confidence**: medium-high. The slice 011 attestation audit-log
contract specifies a non-empty `actor_id`; the prefix was never
contract. Followup: slice 197 could add an OPA / handler check
that `actor_id` matches a uuid OR jwt-id shape, but that's a
defense-in-depth audit rather than a correctness gap.

## D6 — credstore package retention

**Decision**: The `internal/api/credstore/` package and the
`apikeystore.Authenticate` API surface are NOT deleted by slice
197 (per P0-197-1).

The package remains because:

1. Bootstrap-token path (`IssueBootstrapFixedAdminCredential` →
   `credstore.IssueFixedAdmin`) is still in production for slice
   037's docker-compose self-host bundle one-shot bootstrap
   container. Removing credstore breaks self-host bring-up.
2. Two middleware-self-test files
   (`securityheaders_integration_test.go`,
   `metrics_endpoint_test.go`) test the
   `httpAuthMiddlewareWithExemptions` function directly. The
   FUNCTION is kept (AC-5); only the production mount is removed.
3. Calendar.ics URL token path uses credstore directly (see D3).
4. gRPC `authInterceptor` (server.go line 396) still uses credstore.
   The gRPC auth path is OUT OF SCOPE for slice 197 (which is HTTP
   middleware retirement only).

Future slice can retire credstore once:

- (a) `IssueBootstrapFixedAdminCredential` graduates to OAuth
  client_credentials (slice 196 unblocks this);
- (b) Calendar.ics tokens graduate to a calendar-scoped JWT
  variant; AND
- (c) gRPC migrates to JWT-bearer auth (slice 190 design includes
  this; landed in slice 191).

**Confidence**: high. The scope is bounded by P0-197-1; the
spillover is well-defined.

## Verification evidence

### Local integration test run

Command:

```bash
BEARER_HASH_KEY="$(openssl rand -hex 32)" \
DATABASE_URL='postgres://postgres:postgres@localhost:55497/security_atlas?sslmode=disable' \
DATABASE_URL_APP='postgres://atlas_app:sa197pass@localhost:55497/security_atlas?sslmode=disable' \
MINIO_ENDPOINT=http://localhost:9097 MINIO_BUCKET=atlas-artifacts-test \
MINIO_ACCESS_KEY=minioadmin MINIO_SECRET_KEY=minioadmin \
NATS_URL=nats://localhost:4297 \
go test -tags=integration -p 1 -count=1 -timeout=600s ./internal/...
```

Environment: Postgres 16, MinIO, NATS JetStream — Docker containers
on host ports 55497 / 9097 / 4297. Clean DB state per run (drop +
recreate container, re-apply migrations, re-import SCF sample).

**Result**: 90 packages PASS / 9 packages FAIL.

The 9 failing packages were each independently verified against
`main` (slice 197 stash-popped) using the same Postgres state +
the same test invocation. **All 32 individual test failures are
pre-existing on main, not caused by slice 197.** Detailed breakdown:

| Package                             | Failing tests                                  | Pre-existing? | Root cause (verified on main)                                                                          |
| ----------------------------------- | ---------------------------------------------- | ------------- | ------------------------------------------------------------------------------------------------------ |
| `internal/api/admincreds`           | 4 (Issue, List, Revoke, Rotate)                | Yes           | Test harness omits tenancymw — pre-existing harness bug                                                |
| `internal/api/controls` (list only) | 2 (List_PopulatedTenant, List_TenantIsolation) | Yes           | `'preventive'` not a valid `control_implementation_type` enum value — pre-existing test / schema drift |
| `internal/api/oauth` (device flow)  | 2                                              | Yes           | Test harness omits the device_authorization endpoint mount                                             |
| `internal/api/scfimport`            | 4                                              | Yes           | Cleanup order FK violation (controls → scf_anchors)                                                    |
| `internal/api/ucfcoverage`          | 9                                              | Yes           | Same FK violation pattern                                                                              |
| `internal/audit/notes`              | 2 (Slice029)                                   | Yes           | Schema drift unrelated to auth                                                                         |
| `internal/catalog/metrics`          | 4                                              | Yes           | `source_slices NOT NULL` schema constraint violated                                                    |
| `internal/frameworkscope`           | 2 (HTTP_EffectiveScope\*)                      | Yes           | `bundle_id NOT NULL` insert violation                                                                  |
| `internal/scope`                    | 3 (ControlApplicability\*)                     | Yes           | Same `bundle_id NOT NULL` violation                                                                    |

Baseline-on-main confirmation was captured by stashing the slice 197
changes, recycling the Postgres container to a fresh state, and
running the same invocation against the same packages.

### Unit test run

Command: `go test -count=1 ./...`

**Result**: ALL PASS (no failures).

### Lint

Command: `golangci-lint run ./internal/api/... ./internal/auth/...`

**Result**: `0 issues.` after `gofmt -w .` (initially 8 gofmt
issues from the bulk migration; all resolved).

### Build

Command: `go build ./...` — clean exit (no output).

Command: `go vet -tags=integration ./...` — clean exit.

### Spot-check: AC-4 mount removal

```
$ grep -n "httpAuthMiddlewareWithExemptions" internal/api/httpserver.go
182:	// `securityheaders_integration_test.go` +
1183:// httpAuthMiddlewareWithExemptions is the HTTP auth middleware that:
1194:func httpAuthMiddlewareWithExemptions(store *credstore.Store, ...
```

Three occurrences remain: (1) a comment reference, (2) the
function doc, (3) the function definition. **No `root.Use(httpAuthMiddlewareWithExemptions(...))` mount remains.**
AC-4 verified.

### Spot-check: AC-3 `atlas_test_` carve-out removal

```
$ grep -n "atlas_test_" internal/api/httpserver.go
(no matches in middleware body)
```

The carve-out branch (`|| strings.HasPrefix(tok, "atlas_test_")`)
was removed from `legacyBearerDeprecation`. Comments referencing
the carve-out's removal remain for archaeology. AC-3 verified.

### Migration scope: 31 HTTP test files migrated

Per-file pattern (compressed): `_, bearer, err :=
srv.IssueBootstrap*Credential(...)` block →
`bearer := srv.IssueTestJWT(t, testjwt.*For(uuid.MustParse(tenant), ...))`.

Files migrated:

1. `internal/api/adminauditperiods/export_integration_test.go`
2. `internal/api/adminvendors/export_integration_test.go`
3. `internal/api/aggregationrules/integration_test.go`
4. `internal/api/anchors/integration_test.go`
5. `internal/api/anchors/state_integration_test.go`
6. `internal/api/artifacts/integration_test.go`
7. `internal/api/board/integration_test.go`
8. `internal/api/calendar/integration_test.go` (partial — D3)
9. `internal/api/controldetail/integration_test.go`
10. `internal/api/controls/attest_integration_test.go` + D5
11. `internal/api/controlstate/integration_test.go`
12. `internal/api/dashboard/empty_set_integration_test.go`
13. `internal/api/dashboard/integration_test.go`
14. `internal/api/decisions/filters_integration_test.go`
15. `internal/api/emptyset/audit_integration_test.go`
16. `internal/api/freshnessdrift/integration_test.go`
17. `internal/api/mcpwriteproposals/integration_test.go`
18. `internal/api/me/profile_integration_test.go` (D4)
19. `internal/api/policies/ack_rate_integration_test.go` (D4)
20. `internal/api/policies/empty_set_integration_test.go`
21. `internal/api/policyacks/empty_set_integration_test.go`
22. `internal/api/policyacks/integration_test.go` (D4)
23. `internal/api/questionnaires/integration_test.go`
24. `internal/api/risks/integration_test.go`
25. `internal/api/schemaregistry/integration_test.go`
26. `internal/api/ucfcoverage/benchmark_test.go`
27. `internal/api/ucfcoverage/integration_test.go`
28. `internal/api/vendors/integration_test.go`
29. `internal/evidence/ingest/integration_test.go`
30. `internal/frameworkscope/integration_test.go`
31. `internal/scope/integration_test.go`

Two test files intentionally NOT migrated (gRPC auth surface, out
of scope):

- `internal/api/server_test.go`
- `internal/api/connectors/service_test.go`

## Spillovers filed

None required for slice 197 scope. The credstore package retirement
is the natural follow-on; see D6 for the gating conditions.
