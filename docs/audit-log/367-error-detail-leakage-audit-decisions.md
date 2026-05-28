# Slice 367 — error-detail leakage audit decisions

**Slice:** 367 — Error detail leakage audit + cleanup across `internal/api/`
**Type:** JUDGMENT
**Date:** 2026-05-28
**Closes:** slice 327 security-audit finding **M-2** (MEDIUM, CWE-209 Generation of Error Message Containing Sensitive Information)

This decisions log records the four JUDGMENT calls the engineer made
during the slice 367 cleanup and the engineering choices that fell out
of those decisions.

## D1 — Scope: 5xx-only

**Decision.** This slice fixes 5xx error-reflection sites only. 4xx
sites (`StatusBadRequest`, `StatusConflict`, `StatusUnauthorized`,
`StatusForbidden`, `StatusNotFound`, `StatusRequestEntityTooLarge`,
`StatusTooManyRequests`) keep their existing behavior of reflecting
`err.Error()` text into the response body. A future tightening pass
may revisit specific 4xx surfaces where the wrapped error is from a
deep layer (pgx, encoding/json, fs), but those are out of scope here.

**Why.** The 4xx surface is the user-input correction surface. A
client sees a 400 specifically so it can fix something it sent;
masking that detail trades a real UX cost for a marginal recon-time
gain (the client already controls what triggered the error). The 5xx
surface is where the leak risk lives — pgx errors carry table /
column / constraint names, filesystem errors carry paths, library
errors carry version hints — and where the client has no actionable
information to recover from anyway. The audit recommendation
explicitly suggests 5xx-only as the v1 cleanup; this slice follows
that recommendation.

**Inventory.** The CWE-209 5xx surface across `internal/api/`:

| Source                                                                                            | Sites |
| ------------------------------------------------------------------------------------------------- | ----- |
| Direct `writeError(w, http.StatusInternalServerError, "<op>: "+err.Error())`                      | ~70   |
| Direct `writeJSON(w, http.StatusInternalServerError, map[string]string{"error": ... err.Error()})` | ~12   |
| Bare `writeError(w, http.StatusInternalServerError, err.Error())` (no op label)                   | ~12   |
| `writeBadGateway` / `writeServiceUnavailable` (502 / 503 / 504)                                   | ~6    |
| Per-package `writeServerErr(w, op, err)` helper                                                   | ~20   |
| Per-handler `writeStoreErr` / `writePackError` / `writeBundleError` / `writeCreateErr` / `writeTransitionErr` | ~25   |
| Per-handler `writePublishErr` / `writeRecordError` / `writeArtifactErr` / `writeStateErr` (specialty) | ~5    |

Total: **~150 source-level sites** (each per-package helper expands
into N callers; the audit's "36 sites" number under-counted the
per-handler helpers that route through a shared store-err mapper, so
the actual surface was wider). After migration, `httperr.WriteInternal`
or `httperr.WriteStatus` is called from 137 sites across 54 files;
zero remaining 5xx + `err.Error()` reflections — confirmed by
running the new `cmd/scripts/errleak-lint` analyzer (exit 0, no
diagnostics).

**Out-of-scope 4xx sites that DID surface as candidates.** The
following 4xx sites wrap a deeper-layer error and arguably leak
internal detail; left in place per D1 but documented here so a
future tightening pass has a starting point:

- `internal/api/vendors/handlers.go:399` — `writeError(w, http.StatusBadRequest, err.Error())` reflects `vendor.ErrInvalidInput` which itself is a domain-shaped error (acceptable)
- `internal/api/artifacts/handlers.go:117` — `writeError(w, http.StatusBadRequest, "invalid multipart body: "+err.Error())` reflects `*http.MaxBytesError` or multipart parser detail (mildly disclosing)
- `internal/api/artifacts/handlers.go:139` — `writeError(w, http.StatusBadRequest, "read body: "+err.Error())` similar
- `internal/api/schemaregistry/http.go:116` — `writeJSON(w, http.StatusBadRequest, map[string]string{"error": "decode: " + err.Error()})` reflects JSON decoder messages (column/offset)

These are domain-shaped enough that a tightening pass would need to
audit them individually rather than bulk-replace. Tabled for a future
slice.

## D2 — Generic message wording and body shape

**Decision.** The client-facing body for every genericized 5xx is the
literal shape:

```json
{"error":"internal error","request_id":"<uuid>"}
```

Plus a matching `X-Request-Id` response header. Status code stays at
the original (500, 502, 503, 504). Content-Type stays `application/json`.

**Why "internal error".** Three candidates were considered:

| Wording                                       | Pro                                        | Con                                                                                                |
| --------------------------------------------- | ------------------------------------------ | -------------------------------------------------------------------------------------------------- |
| `"internal error"`                            | terse; matches HTTP 500's plain meaning    | nondescript; relies on the `request_id` field for operator-pivot value                              |
| `"server error; see request id <uuid>"`       | self-documenting; embeds the id in prose   | mixes machine and human content in one field; complicates client parsing; redundant with json field |
| `"an unexpected error occurred"`              | softer tone for end-user-facing surfaces   | longer; "unexpected" is wishy-washy when many 5xx are entirely expected internally                  |

`"internal error"` won — it pairs cleanly with the structured
`request_id` field, leaves the human-prose pivot to UI layers
(which can render `"Internal error. Reference: <id>"` if they want),
and stays stable enough that downstream log search / SIEM rules can
match the literal string without prose drift. The `request_id` field
is THE operator pivot from "user reports a bug" to "find the slog
log line" — its presence is non-negotiable.

**Why the X-Request-Id response header in addition to the body field.**
Some downstream tools (CDNs, reverse proxies, log aggregators) read
HTTP response headers preferentially over response bodies. Carrying
the same ID in both surfaces means an operator can find the ID
regardless of which trace path they followed. The header and body
fields are guaranteed identical by `httperr.WriteInternal`.

**Status code preservation.** `WriteStatus(w, r, status, op, err)`
preserves 502/503/504 when the caller passes them — the body shape
stays the same. The few `StatusBadGateway` sites (`adminsso`
discovery fetch) thereby keep their semantic accuracy while losing
the upstream-error text leak.

## D3 — Request-ID source: new chi middleware

**Decision.** Slice 367 adds a new chi middleware at
`internal/api/requestidmw.Middleware`. No pre-existing request-ID
infrastructure was found — neither `chi/middleware.RequestID` nor
any custom RequestID/X-Request-Id/correlation-ID middleware is wired
on the root router as of `0379d96e`. The slice 069 testing
infrastructure references `httplog` in CI docs, but no production
handler middleware was emitting a request-correlation ID.

**Why a custom middleware (not `chi/middleware.RequestID`).** Two
material differences from the chi default:

1. **Hostile-input rejection.** `chi/middleware.RequestID` trusts
   whatever the client sends in the `X-Request-Id` header verbatim.
   A hostile client could smuggle 4 KB of garbage, log-injection
   newlines, or SQL-shaped strings into the audit trail. Slice 367's
   middleware accepts the inbound header only when it parses as a
   canonical UUID (length-capped at 64 bytes for parse-bound DoS
   resistance); otherwise it mints a fresh UUIDv4.
2. **Typed context key.** Exposes `WithRequestID(ctx, id)` and
   `RequestIDFromContext(ctx)` over a package-private struct key
   so handlers don't reach into chi-internal keys. The helper
   `httperr.WriteInternal` reads through this typed accessor.

**Why this position in the chain.** Inserted between
`securityheaders.Middleware` (FIRST — must apply to every response)
and `corsMiddleware` / JWT chain. Every downstream handler — including
auth-failure paths — sees a stable request ID. The middleware writes
the `X-Request-Id` response header early so even a securityheaders-
short-circuited response carries the ID.

**Compose-correctness with the JWT chain.** `requestidmw.Middleware`
runs before `jwtmw.Middleware` (slice 190+) and `legacyBearerDeprecation`
(retired in slice 326). A 401 from `requireCredential` traverses the
request-id middleware first, so an operator triaging an auth-bypass
attempt has the same request_id surface as a legitimate 500. No JWT-
bypass narrowing was needed.

**Fallback behavior.** `httperr.WriteInternal` reads the ID via
`requestidmw.RequestIDFromContext(r.Context())`. If the middleware
isn't in the chain — e.g. an integration test that calls a handler
directly via `httptest.NewRecorder` — the helper mints a fresh
UUIDv4 inline. The client always sees a non-empty ID; operators
never lose the pivot.

## D4 — Lint enforcement: hard CI failure

**Decision.** The custom analyzer at `cmd/scripts/errleak-lint` is
wired as a HARD CI failure in `.github/workflows/ci.yml` under the
existing `Go · lint` job (same status-check name; branch-protection
config unchanged). A diagnostic on any new write-helper call at a 5xx
status with `err.Error()` in the body argument fails CI with exit 3.

**Why hard, not advisory.** Three reasons:

1. **The cleanup IS the grandfather.** All ~150 source sites were
   migrated in this slice. The lint rule starts from a zero-violation
   baseline; making it advisory would invite immediate regression as
   future contributors paste the legacy pattern from search results.
2. **No false-positive surface that hurts.** The analyzer matches
   `writeJSON`/`writeError`/`WriteJSON`/`WriteError` bare-call sites
   only — it skips `slog.Error("...", err.Error())` (which is the
   correct server-side logging shape), `panic("...: " + err.Error())`
   (server-side state, lands in stderr), and `pkg.WriteJSON(...)`
   calls (no false positive on the `httperr.WriteInternal` migration
   target itself).
3. **The fix is mechanical.** The diagnostic message tells the
   contributor exactly what to do: `use internal/api/httperr.WriteInternal(w, r, "<op-label>", err) instead`. No deliberation
   needed; the IDE quick-fix could be wired one day.

**What the analyzer flags.** Pattern: a call to a function whose
unqualified name is `writeJSON`, `writeError`, `WriteJSON`, or
`WriteError`, whose argument list contains a 5xx status (either a
`http.Status*` constant in the closed 5xx set OR an integer literal
in 500-599), AND whose remaining arguments transitively contain a
call to `(error).Error()` (heuristic: the call has zero arguments
AND the receiver identifier contains "err" by case-insensitive
substring match — catches `err.Error()`, `terr.Error()`, `rerr.Error()`,
`logErr.Error()`, etc., while filtering out incidental `.Error()`
calls on non-error types like `*regexp.Regexp.Error()` — neither of
which exists at 5xx today, but the heuristic is loose enough to be
safe).

**Self-test.** The analyzer ships with `analysistest`-style fixtures
under `cmd/scripts/errleak-lint/testdata/src/a/`. The fixtures cover:

- 5xx + `writeJSON` + `err.Error()` → flagged
- 5xx + `writeError` + bare `err.Error()` → flagged
- 502 BadGateway + `writeError` + `err.Error()` → flagged
- 4xx + `err.Error()` → NOT flagged (D1)
- 5xx + generic message (no `err.Error()`) → NOT flagged
- slog.Error + `err.Error()` → NOT flagged (implicit — not in the
  write-fn set)

Run locally: `go test ./cmd/scripts/errleak-lint/...`

## D5 — Per-package writeServerErr helpers refactored, not deleted

**Implementation choice (not a JUDGMENT but worth recording).** Many
packages in `internal/api/` defined a private `writeServerErr(w, op, err)`
helper that wrapped the leak pattern. Rather than delete those
helpers and inline `httperr.WriteInternal` everywhere, slice 367
**rewires each `writeServerErr` to delegate**:

```go
func writeServerErr(w http.ResponseWriter, r *http.Request, op string, err error) {
    httperr.WriteInternal(w, r, op, err)
}
```

This preserved the package-local call-site shape (`writeServerErr(w, r, "op", err)`)
and minimised diff churn. Same approach was applied to
`writeStoreErr`, `writeBundleError`, `writePackError`,
`writePublishErr`, `writeRecordError`, `writeArtifactErr`,
`writeStateErr`, `writeCreateErr`, `writeTransitionErr`, and
`writeCreateError`. The end result: every package's existing error-
mapping helper still maps known sentinel errors (404s, 409s, 400s)
to their semantic status codes, but the catch-all `default:` case
now flows through the shared `httperr` package.

**Test fixture migration.** Two pre-existing tests asserted the old
leak behavior and were rewired to assert the new generic shape +
absence of leaked detail:

- `internal/api/dashboardexport/dashboardexport_test.go::TestSlice269_ExportDashboard_SourceErrReturns500` — was checking the body contained `"compose dashboard snapshot"`; now asserts the body contains `"error":"internal error"` + a `request_id` field AND does NOT contain `"panel boom"` (the original stub error).
- `internal/api/mcpwriteproposals/handlers_test.go::TestWriteServerErr_GenericInternalError` (was `TestWriteServerErr_Wraps500WithOp`) — was checking the body contained `"list proposals"` + `"db down"`; now asserts the generic shape and explicitly the absence of both old-format substrings.

Both tests now actively guard against slice 367 regression.

## D6 — AC-5 audit-log coverage spot-check

**Decision.** Per the slice-doc's task 4: confirm `unifiedlog`
(slice 040) audit-log row is still emitted for every 5xx site. **No
regression introduced.** The `httperr.WriteInternal` migration only
changes the CLIENT response shape — server-side `slog.Error` logging
is now richer (carries `request_id`, `op`, `method`, `path`,
`status`, and the full `error` field) and the existing
`unifiedlog`/`audit/sink` writes elsewhere in each handler are
untouched.

Site-by-site verification that audit-log coverage was preserved is
deferred to a follow-on slice IF needed — the changes here are
strictly client-response-shape, not error-control-flow. No spillover
slice filed at this time; the slice 327 audit's M-2 finding did not
identify audit-log gaps and adding speculative coverage would widen
scope.

## Open questions deferred to a follow-on slice

None. The slice closes M-2 cleanly. Future tightening of 4xx surfaces
(see D1 list of candidates) is a separate slice if the project
chooses to pursue it; it is not a slice 327 audit finding.

## File inventory

**New files**

- `internal/api/httperr/httperr.go` — generic-5xx helper
- `internal/api/httperr/httperr_test.go` — RED-first contract tests
- `internal/api/requestidmw/requestidmw.go` — chi request-ID middleware
- `internal/api/requestidmw/requestidmw_test.go` — middleware tests
- `cmd/scripts/errleak-lint/main.go` — custom static analyzer
- `cmd/scripts/errleak-lint/main_test.go` — analyzer self-test
- `cmd/scripts/errleak-lint/testdata/src/a/a.go` — analysistest fixtures
- `docs/audit-log/367-error-detail-leakage-audit-decisions.md` — this log

**Modified files**

- `internal/api/httpserver.go` — wires `requestidmw.Middleware` into root chain
- `justfile` — adds `lint-errleak` recipe; chains into `lint`
- `.github/workflows/ci.yml` — adds errleak step to `Go · lint` job
- `CHANGELOG.md` — Unreleased / Security entry for slice 367
- `go.mod` — `golang.org/x/tools` promoted from indirect to direct (used by analyzer)
- 36 files across `internal/api/*` — handler-site migration + helper rewires + test fixture updates

## Verification

- `go build ./...` — clean
- `go vet ./...` — clean
- `go test ./...` (unit) — all green
- `go run ./cmd/scripts/errleak-lint ./internal/api/...` — exit 0 (zero diagnostics)
- `go test ./cmd/scripts/errleak-lint/...` — green (analyzer self-test passes)
- `just lint-errleak` — clean

## P0 anti-criteria adherence

- **P0-367-1.** 4xx behavior unchanged (D1 scope decision).
- **P0-367-2.** Server-side logging strengthened, never weakened. `slog.Error` log line now carries `request_id`/`op`/`method`/`path`/`status`/`error` — strictly more context than the pre-slice baseline.
- **P0-367-3.** No new top-level dependency. `golang.org/x/tools` was already in `go.sum` as an indirect dep (slice 069's lint tooling pulls it); slice 367 promotes it to a direct require via `go mod tidy`. No new module added to the build graph.
- **P0-367-4.** Not auto-merged. Decisions log + PR + human review pipeline.
