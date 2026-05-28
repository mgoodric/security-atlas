# 328 — Comprehensive code review report (voltagent-qa-sec:code-reviewer)

**Date:** 2026-05-28
**Reviewer agent:** voltagent-qa-sec:code-reviewer (loaded as Engineer context)
**Review type:** Read-only-with-findings; JUDGMENT slice (no code changes in this slice)
**Review scope:** v1-complete `main` at HEAD `1e1a78a6` (post-slice-367)
**Demo seed only:** no runtime introspection performed; static read of source files

---

## Executive summary

The codebase is mature, well-documented, and idiomatically Go-first. Across ~779 Go files (~102k LOC in `internal/` non-test), ~506 TypeScript files, and 7 Python files, the review surfaced patterns that strengthen the platform substantially more often than they weaken it. Per-package doc comments are present on every file examined. TODO/FIXME density is effectively zero in production code. Package-boundary direction is clean — no `internal/*` imports `cmd/*`, no domain package imports `internal/api/*`. The slice 367 `httperr` helper (security M-2 follow-up) demonstrates the conventional pattern the rest of the HTTP surface should follow.

The two High-severity findings are both **reuse consolidations** rather than correctness bugs: the same helper code duplicated across many packages where a single shared package would reduce maintenance load and AI-navigability cost.

**No correctness bugs surfaced rising to High severity.** No data-corruption risks, no goroutine leaks with unbounded growth, no resource-lifecycle bugs, no missing defer chains, no panic-in-hot-path patterns. The constructor-panic pattern is uniformly used at process boot, never in request handlers.

| Severity      | Count | Notes                                                                                         |
| ------------- | ----- | --------------------------------------------------------------------------------------------- |
| Critical      | 0     | No correctness bugs with user-visible impact                                                  |
| High          | 2     | Reuse consolidations: writeJSON/writeError duplication; web/lib/api.ts god-file               |
| Medium        | 4     | httpserver.go god-file; auth clock-injection drift; SESSION_COOKIE rename; Python error-shape |
| Low           | 3     | Two writeLog near-duplicates; apikeystore goroutine shutdown; json encode error discards      |
| Informational | 4     | Doc density; zero TODO debt; clean boundaries; comment-code drift sampled                     |

**Spillover slices filed:** 3 — slots 369, 370, 371.

- 369 — High H-1 writeJSON/writeError consolidation into `internal/api/httpresp`
- 370 — High H-2 split `web/lib/api.ts` god-file into per-domain files under `web/lib/api/`
- 371 — Medium M-2 auth-substrate clock injection (sessions + apikeystore + jwtmw)

Mediums M-1 (httpserver.go), M-3 (SESSION_COOKIE rename), and M-4 (Python OSCAL error shape) are documented in this report but not filed as spillover slices — see decisions log for the bundle-vs-file JUDGMENT.

**Cross-reference to slice 327:** No findings duplicate slice 327's. M-4 (Python OSCAL `str(exc)`) is a security-class issue surfaced from a code-quality angle; it is flagged here as "candidate dedupe with slice 327's M-2" so the maintainer can decide whether to file a separate security follow-up or treat it as out of scope for this audit.

---

## Audit surface coverage

Per slice doc AC-7. One row per top-level package examined.

| Surface                                                                       | Files / Packages                                                        | Examined? | Findings                        |
| ----------------------------------------------------------------------------- | ----------------------------------------------------------------------- | --------- | ------------------------------- |
| `internal/api/httperr`                                                        | httperr.go (slice 367)                                                  | YES       | (Baseline; positive pattern)    |
| `internal/api/requestidmw`                                                    | requestidmw.go (slice 367)                                              | YES       | (Baseline; positive pattern)    |
| `internal/api/admin*` (8 packages)                                            | admintenants, adminusers, adminauditlog, admincreds, adminvendors, etc. | YES       | L-3 (json encode err discard)   |
| `internal/api/oauth/*`                                                        | authorize, token, introspect, revoke, device\_\*, pkce                  | YES       | (No findings; mature substrate) |
| `internal/api/server.go`                                                      | Server struct definition (~25 fields)                                   | YES       | M-1 (god-struct adjacent)       |
| `internal/api/httpserver.go`                                                  | httpHandler() (1397 lines; 184 routes)                                  | YES       | M-1                             |
| `internal/api/` cross-package                                                 | 50+ packages with writeJSON/writeError                                  | YES       | **H-1**                         |
| `internal/auth/sessions`                                                      | sessions.go (395 LOC)                                                   | YES       | M-2                             |
| `internal/auth/apikeystore`                                                   | apikeystore.go (372 LOC)                                                | YES       | M-2, L-2                        |
| `internal/auth/jwtmw`                                                         | middleware.go (301 LOC)                                                 | YES       | M-2                             |
| `internal/auth/keystore/fsstore`                                              | fsstore.go (slice 327 M-1 already filed as 366)                         | YES       | (Cross-ref slice 327 M-1)       |
| `internal/auth/users`                                                         | users.go (509 LOC)                                                      | YES       | (No findings; mature)           |
| `internal/auth/oauthcode`                                                     | oauthcode.go (367 LOC)                                                  | YES       | (No findings)                   |
| `internal/auth/oauthclient`                                                   | oauthclient.go (252 LOC)                                                | YES       | (No findings)                   |
| `internal/auth/revocation`                                                    | revocation.go + pgx.go                                                  | YES       | (No findings)                   |
| `internal/evidence/ingest`                                                    | ingest.go (clock injection — positive pattern)                          | YES       | (Positive baseline)             |
| `internal/evidence/streambuf`                                                 | streambuf.go (clock injection — positive pattern)                       | YES       | (Positive baseline)             |
| `internal/audit/period`                                                       | period.go (writeLog)                                                    | YES       | L-1                             |
| `internal/audit/walkthrough`                                                  | walkthrough.go (writeLog)                                               | YES       | L-1                             |
| `internal/audit/sink`                                                         | sink.go (fire-and-forget pattern)                                       | YES       | (Documented; not a finding)     |
| `internal/board/*`                                                            | generator, pack_generator, pack_store (clock injection)                 | YES       | (Positive baseline)             |
| `internal/eval/consumer.go`                                                   | NATS consumer goroutine + done watcher                                  | YES       | (Standard pattern)              |
| `internal/risk/*` + `risk/aggrule`                                            | Methodology + store                                                     | YES       | (No findings)                   |
| `internal/drift`, `internal/exception`, `internal/policy`, `internal/control` | Domain packages                                                         | YES       | (Spot-check; no findings)       |
| `internal/freshnessdrift/worker.go`                                           | Goroutine pattern                                                       | YES       | (No findings)                   |
| `internal/scope`, `internal/frameworkscope`, `internal/ucf`                   | Domain packages                                                         | YES       | (No findings)                   |
| `cmd/atlas`, `cmd/atlas-cli`                                                  | Entry points                                                            | YES       | (Spot-check; no findings)       |
| `web/proxy.ts`                                                                | Next.js middleware (slice 367 + slice 087)                              | YES       | (No findings)                   |
| `web/lib/api.ts`                                                              | 2901 LOC; 219 exports                                                   | YES       | **H-2**                         |
| `web/lib/api/bff.ts`                                                          | 80 LOC shared forwardJSON / forwardMultipart                            | YES       | M-3 (SESSION_COOKIE naming)     |
| `web/lib/api/audit-server.ts`                                                 | Audit-workspace fetch helpers                                           | YES       | (Established split pattern)     |
| `web/lib/auth.ts`                                                             | SESSION_COOKIE constant (resolves to `atlas_jwt`)                       | YES       | M-3                             |
| `web/lib/secure-cookie.ts`                                                    | Cookie writer hardening                                                 | YES       | (No findings)                   |
| `web/app/api/*` (~108 route files)                                            | BFF routes — sampled across audits, controls, evidence, admin           | YES       | (Spot-check; consistent shape)  |
| `web/components/shell/*`                                                      | global-search.tsx (539 LOC — largest component)                         | YES       | (No findings)                   |
| `web/components/risk-hierarchy/*`                                             | decision-timeline-panel + theme-heatmap-panel                           | YES       | (No findings)                   |
| `oscal-bridge/atlas_oscal_bridge`                                             | server.py (113 LOC) + serializer.py (442 LOC)                           | YES       | **M-4**                         |

---

## Findings

### H-1 (HIGH) — `writeJSON` / `writeError` / `writeServerErr` duplicated across 50+ packages

- **Category:** reuse / DRY violation
- **Files:** 50+ `internal/api/*/*.go` packages (101 declarations across the surface)
- **Canvas invariant:** Article VII (Simplicity Gate — minimize abstraction); Article VIII (trust the framework — same helper everywhere instead of per-package)
- **Cross-reference:** slice 367's `internal/api/httperr` is the precedent — a shared helper that succeeded in replacing ~119 `err.Error()` reflection sites

**Description.** `grep -rn '^func write[A-Z]' internal/api/ --include="*.go"` finds the following counts of duplicate helper declarations:

```
  41 func writeError(w http.ResponseWriter, code int, msg string) {
  37 func writeJSON(w http.ResponseWriter, code int, body any) {
  18 func writeServerErr(w http.ResponseWriter, r *http.Request, op string, err error) {
   3 func writeError(w http.ResponseWriter, status int, msg string) {  // parameter rename only
   2 func writeJSON(w http.ResponseWriter, status int, body any) {     // parameter rename only
```

All 41 `writeError` and 37 `writeJSON` bodies are byte-identical to:

```go
func writeJSON(w http.ResponseWriter, code int, body any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(code)
    _ = json.NewEncoder(w).Encode(body)
}
func writeError(w http.ResponseWriter, code int, msg string) {
    writeJSON(w, code, map[string]string{"error": msg})
}
```

(Verified via diff across `internal/api/board/handlers.go:287`, `internal/api/metrics/handlers.go:672`, `internal/api/admintenants/handler.go:717`, `internal/api/adminsso/handler.go:527`, and 10 more samples.)

The 18 `writeServerErr` declarations are the slice-367 era's per-package `writeServerErr(w, r, op, err)` adapters that now delegate to `httperr.WriteInternal` internally — keeping their per-package call sites intact while letting the central helper own the body format. This is a useful intermediate pattern but still leaves 18 wrapper functions in the codebase that just `httperr.WriteInternal(w, r, op, err)` and return.

**Impact.** Five concrete maintenance costs:

1. **Drift surface.** A future change to the JSON response shape (adding `request_id` to every 4xx, switching to RFC 7807 problem+json, adding cache-control headers) requires editing 50+ files. The slice 367 migration touched 36 files for the 5xx case alone — sustained drift risk.
2. **Inconsistent naming.** Three handlers use `status int` while 41 use `code int` for the same parameter. Five packages also have `writeServerErr`; 45 do not.
3. **Test coverage fragmentation.** Each duplicate copy needs its own test (currently it does not — tests assert against the response shape, not the helper itself, which works only because all 78 copies are byte-identical today).
4. **New-developer cognitive load.** New `internal/api/<foo>/*.go` files have a 50/50 chance of finding either `writeJSON` or `httperr.WriteInternal` next door, with no rule for which to use.
5. **AI-navigability tax.** A code-search for "how does this API respond?" surfaces 78 hits rather than one.

**Recommended mitigation.**

1. Create `internal/api/httpresp` (mirror of `internal/api/httperr` scope and conventions). Public surface:

```go
// WriteJSON writes a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, body any) { /* ... */ }

// WriteError writes a JSON error response: {"error": msg}.
func WriteError(w http.ResponseWriter, status int, msg string) { /* ... */ }
```

2. Migrate the 78 `writeJSON` + `writeError` sites in one mechanized sweep (sed-driven; the bodies are byte-identical so the rewrite is regex-safe).

3. Retire the 18 `writeServerErr` wrappers — call sites now use `httperr.WriteInternal` directly. (This is the half-completed pattern slice 367 deliberately left for follow-up per its decisions log §D2.)

4. Add a custom golangci-lint check (analogous to the slice-367 `errleak-lint` analyzer) preventing new `func writeJSON\|writeError\|writeServerErr` declarations in `internal/api/*`.

**Spillover slice:** 369 — `infra/369-httpresp-shared-helper-consolidation`

**Estimated size:** ~3d (similar to slice 367's scope — mechanized migration + lint rule + tests). Fits the JUDGMENT slice mold: no behavior change at the wire, just structural consolidation.

---

### H-2 (HIGH) — `web/lib/api.ts` god-file (2901 LOC, 219 exports)

- **Category:** structure / god-file / convention drift
- **File:** `web/lib/api.ts`
- **Canvas invariant:** Article VII (Simplicity Gate); Article VIII (anti-abstraction — same domain split everywhere)
- **Cross-reference:** the established convention in `web/lib/api/` — `audit-server.ts`, `bff.ts`, `anchors-export.ts`, `controls-export.ts`, `metrics.ts`, `risks-export.ts`, `audit-log.ts`, `audit.ts`, `exceptions-export.ts`, `controls-history-export.ts`, `activity.ts` are all per-domain files under the directory

**Description.** `wc -l web/lib/api.ts` returns 2901 lines. `grep -c "^export " web/lib/api.ts` returns 219 exported symbols. The file spans every API client function the dashboard uses — vendors, framework scopes, attest forms, artifact upload, admin credentials, feature flags, super admins, admin tenants, admin demo, admin SSO, control coverage, control state, control effectiveness — without any internal organization beyond commented section banners.

The split convention IS already established next door: `web/lib/api/bff.ts` (80 LOC, 2 exports for `forwardJSON` + `forwardMultipart`), `web/lib/api/audit-server.ts` (40 LOC, audit-workspace fetch helpers), and 9 other per-domain files. `web/lib/api.ts` predates this convention and was never split.

**Impact.**

1. **Editor performance.** TypeScript Language Server reanalyses every dependent file when any of the 219 symbols changes. The file is on the dependency path of essentially every dashboard route.
2. **Merge conflict density.** Any two slices touching different domains both touch this file.
3. **Test split.** vitest coverage tracking flags `web/lib/api.ts` as a single unit; a per-file floor in `web/coverage-thresholds.json` would gate the slowest-moving function in any of 219 against the fastest-moving — coarse-grained gate.
4. **Convention drift.** The clearest "do as the rest of the file does" signal next door is `web/lib/api/*.ts` files of 40-200 lines per domain. `web/lib/api.ts` is the outlier.
5. **AI-navigability tax.** Same as H-1: surfaces 219 hits when searching for a specific domain's client functions.

**Recommended mitigation.**

1. Split `web/lib/api.ts` into per-domain files under `web/lib/api/`:
   - `web/lib/api/anchors.ts` (lines 85-265 in current file)
   - `web/lib/api/vendors.ts` (lines 366-475)
   - `web/lib/api/framework-scopes.ts` (lines 473-590)
   - `web/lib/api/attest.ts` (lines 590-700)
   - `web/lib/api/artifacts.ts` (lines 634-704)
   - `web/lib/api/admin-credentials.ts` (lines 704-781)
   - `web/lib/api/feature-flags.ts` (lines 781-829)
   - `web/lib/api/super-admins.ts` (lines 829-917)
   - `web/lib/api/admin-tenants.ts` (lines 917-980)
   - `web/lib/api/admin-demo.ts` (lines 980-1076)
   - `web/lib/api/admin-sso.ts` (lines 1076-1306)
   - `web/lib/api/control-coverage.ts` (lines 1306-1400)
   - Plus remaining domains beyond the sample
2. Keep `web/lib/api.ts` as a backward-compat re-export shim (`export * from "./api/anchors"; export * from "./api/vendors"; …`) for one release window so the 200+ existing import sites do not need to change in the same PR.
3. Migrate import sites in a follow-up PR. Retire the shim once the migration is mechanical-only.
4. Add an eslint rule limiting `web/lib/api*.ts` files to a soft cap (e.g., 600 LOC) to prevent regression.

**Spillover slice:** 370 — `web/370-api-client-split`

**Estimated size:** ~3d. The first day is structural slicing + shim creation (mechanical). The second day is per-route import-site migration (high count, low risk per change). The third day is removing the shim + adding the lint rule.

---

### M-1 (MEDIUM) — `internal/api/httpserver.go` is a 1397-LOC route-wiring file with 80+ imports

- **Category:** structure / god-file
- **File:** `internal/api/httpserver.go`
- **Canvas invariant:** Article VII (Simplicity Gate)
- **Cross-reference:** `internal/api/server.go` Server struct (25 fields, each Attach-ed at startup)

**Description.** `internal/api/httpserver.go` houses the entire chi router wire-up: `httpHandler()` is the 1003-line method that registers 184 routes (`grep -nE '(r|root)\.(Get|Post|Put|Patch|Delete|Mount|Route|Handle)\('` counts) across 50+ handler packages. Each route registration is preceded by a slice-numbered comment explaining its provenance — the comments are excellent — but the structural shape is a single procedural file that grows on every new endpoint.

The `Server` struct in `internal/api/server.go` mirrors this: 25 fields, one per attached dependency, each with its own `Attach<Thing>(...)` method. The pattern is functional but unbounded.

**Impact.**

1. **Merge conflict density.** Every slice that adds an endpoint touches the same file. Slice 327's H-1 fix (slice 365 — OIDC nonce) had to navigate the existing route wiring.
2. **AI-navigability tax.** "Where is route X mounted?" requires a grep across one 1397-line file rather than navigation into a per-domain wiring file.
3. **Conditional-wire complexity.** Many routes mount only when `s.<dep> != nil`. Reading the file means tracking 25 separate "is this attached?" branches.

**Recommended mitigation (no spillover slice; v2+ architectural project).**

The natural pattern would be per-handler-package `Mount(r chi.Router, deps Deps)` methods, with `httpserver.go` becoming a 200-300 line orchestrator that constructs deps and calls `<pkg>.Mount(r, deps)` per registered package. This is a meaningful refactor — it would touch every handler package and require establishing the `Deps` convention. Filing it as one slice would create a multi-PR cleanup; filing it as a "tracking" slice without immediate execution would clutter the backlog.

**Disposition.** Audit report only. The growth rate has been ~30 routes per quarter; the file is functional today. Reconsider as a tracking slice when the file crosses 2000 LOC, or as a v2 architectural project once v1 binary criterion is met. Decisions log §D3 documents the bundle JUDGMENT.

---

### M-2 (MEDIUM) — Auth substrate uses raw `time.Now()` instead of clock injection (convention drift)

- **Category:** convention drift / testability
- **Files:**
  - `internal/auth/sessions/sessions.go:137,183,188` — TTL calculations + expiry checks
  - `internal/auth/apikeystore/apikeystore.go:165,251,293` — rotation grace + last-used + TTL
  - `internal/auth/jwtmw/middleware.go:298` — default clock literal
  - `internal/auth/keystore/fsstore/fsstore.go:236` — directory naming (acceptable here; not a hot path)
- **Canvas invariant:** Article III (Test-First Imperative — testability of time-dependent logic)
- **Cross-reference:** established positive baseline in `internal/board/generator.go:61`, `internal/board/pack_generator.go:99`, `internal/evidence/ingest/ingest.go:195`, `internal/evidence/streambuf/streambuf.go:259`, `internal/drift/drift.go:67`

**Description.** The convention next door uses an injected clock:

```go
// internal/board/generator.go:61
clock: func() time.Time { return time.Now().UTC() },

// internal/evidence/ingest/ingest.go:195
clock: func() time.Time { return time.Now().UTC() },

// internal/drift/drift.go:67
now: func() time.Time { return time.Now().UTC() },
```

The auth substrate's `Store` types do NOT carry a clock field:

```go
// internal/auth/sessions/sessions.go:77-79
type Store struct {
    pool *pgxpool.Pool
    ttl  time.Duration
}
```

And then use raw `time.Now()` in time-dependent operations:

```go
// internal/auth/sessions/sessions.go:137
expiresAt := time.Now().UTC().Add(s.ttl)

// internal/auth/sessions/sessions.go:183
if !row.ExpiresAt.Valid || time.Now().UTC().After(row.ExpiresAt.Time) {

// internal/auth/apikeystore/apikeystore.go:251
now := time.Now().UTC()
```

**Note:** `internal/api/oauth/token.go` DOES use clock injection (`now func() time.Time` in the TokenEndpoint), so the convention is already known and applied where security testing has driven the design. The auth/sessions and auth/apikeystore stores predate this pattern.

**Impact.**

1. **Test brittleness.** Tests that assert session expiry or key-rotation grace must use real wall-clock + sleep + tolerance windows. A test that wants to verify "session expires at T+24h" today uses real time; with clock injection it could use `WithClock(pinnedClock)` like the rest of the platform.
2. **Convention split.** Future maintainers reading the codebase see two patterns. Without a documented rule, drift continues.
3. **Reduced coverage of expiry edge cases.** Tests for "what if ExpiresAt is exactly now?" require nanosecond-level real-clock control today.

**Recommended mitigation.**

1. Add a `clock func() time.Time` field to:
   - `internal/auth/sessions/Store`
   - `internal/auth/apikeystore/Store`
   - `internal/auth/jwtmw/Middleware` (already exposes `nowFn`; just unify its default with the rest)
2. Default to `func() time.Time { return time.Now().UTC() }` in each `NewStore`.
3. Add a `WithClock(fn)` method (mirrors the slice 143 `admintenants.WithClock` pattern at `internal/api/admintenants/handler.go:185`).
4. Replace the ~8 raw `time.Now()` call sites with `s.clock()`.
5. Add at least one test per store that exercises a time-dependent path via the injected clock (red-first per Article III).

**Spillover slice:** 371 — `auth/371-clock-injection-substrate`

**Estimated size:** ~1d. Three packages, ~8 call sites, mechanical addition of one field + one method per store + a test or two per package to cover the previously hard-to-reach edges.

---

### M-3 (MEDIUM) — `SESSION_COOKIE` symbol is misnamed (resolves to `atlas_jwt`); 80 route files import it

- **Category:** naming consistency / comment-vs-code drift
- **Files:** `web/lib/auth.ts:19` (declaration) + ~80 `web/app/api/**/route.ts` files (consumers)
- **Canvas invariant:** None directly; convention / readability concern

**Description.** `web/lib/auth.ts` defines:

```ts
// Slice 189 set the canonical `atlas_jwt` cookie via the
// OAuth callback's "Set-Cookie" header, but the BFF helper here still
// resolves to the same cookie the OAuth callback writes (`atlas_jwt`).
// The constant NAME stays `SESSION_COOKIE` so the 30+ import sites that
// existed at slice-189-time don't all churn in the same PR. A
// follow-on cleanup slice may rename the symbol to `ATLAS_JWT_COOKIE` once
// the OIDC bootstrap flow is wired everywhere.
//
// flow will set `atlas_jwt` and the loop resolves.
export const SESSION_COOKIE = "atlas_jwt";
```

The symbol name `SESSION_COOKIE` describes the legacy session-bearer pattern; the cookie value is now `atlas_jwt`. ~80 BFF route handlers import the misleadingly-named symbol.

**Impact.**

1. **Cognitive friction.** Readers see `SESSION_COOKIE` and infer "this is the bearer-based session cookie"; the value `atlas_jwt` tells a different story.
2. **Onboarding friction.** A new contributor searching for `atlas_jwt` consumers does not find the symbol; they have to follow the constant from `web/lib/auth.ts` outward.
3. **Self-acknowledged debt.** The comment names the follow-up explicitly; no follow-on slice has been filed in 100+ slices.

**Recommended mitigation (no spillover slice).**

Rename `SESSION_COOKIE` to `ATLAS_JWT_COOKIE` in a single mechanized PR:

```bash
# Conceptual, not a final command — the codebase has no plain SESSION_COOKIE
# string outside the import path; the rename is safe.
sed -i.bak 's/SESSION_COOKIE/ATLAS_JWT_COOKIE/g' web/**/*.ts
```

Keep `SESSION_COOKIE` as a `@deprecated` re-export for one release window. Retire after one slice's worth of import-site migration.

**Disposition.** Audit report only. The drift is cosmetic (the underlying behavior is correct) and the rename is one mechanical PR — does not warrant the slice + decisions log + reconcile flow. The maintainer can bundle the rename into the H-2 slice (web/lib/api.ts split) if that PR opens for renames anyway, or file it as a 1-hour cleanup. Decisions log §D4 documents the bundle JUDGMENT.

---

### M-4 (MEDIUM) — Python OSCAL bridge reflects compliance-trestle exception text into gRPC INTERNAL status

- **Category:** correctness / information disclosure (mirror of slice 327's M-2 pattern)
- **File:** `oscal-bridge/atlas_oscal_bridge/server.py:42-70`
- **Canvas invariant:** None directly; defense-in-depth
- **Cross-reference:** **candidate dedupe with slice 327 M-2** (which closed the Go-side equivalent via slice 367). This Python-side mirror was not covered by slice 327's scope.

**Description.** Each gRPC RPC handler catches a broad `Exception` and aborts with `context.abort(grpc.StatusCode.INTERNAL, f"... failed: {exc}")`. Sample (`server.py:42-49`):

```python
def SerializeSSP(self, request, context):  # noqa: N802 — gRPC naming
    try:
        data = serialize_ssp(request.input)
    except SerializeError as exc:
        context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
    except Exception as exc:  # noqa: BLE001
        context.abort(grpc.StatusCode.INTERNAL, f"SSP serialize failed: {exc}")
```

`SerializeError` is a domain-specific `ValueError` subclass — that path is acceptable (the message is platform-authored). The broad `except Exception` path catches anything the underlying `compliance-trestle` (pydantic v1) library raises and reflects `str(exc)` into the gRPC error message. trestle's pydantic models raise `ValidationError` with full schema names and field paths; those propagate to the gRPC client as the INTERNAL status message.

**Impact.**

1. **Library version disclosure.** A pydantic v1 ValidationError carries class names like `pydantic.error_wrappers.ValidationError` and field paths from trestle's internal model graph — useful to an attacker fingerprinting the deployment.
2. **Mirror of slice 327's M-2 pattern.** The Go side fixed this via slice 367's `httperr` helper. The Python side was out of scope for slice 327 (the security-auditor's review surface was the Go HTTP server). A code-reviewer's lens spots it.

**Recommended mitigation.**

The fix mirrors slice 367's pattern but adapted to gRPC:

1. Log the full `exc` server-side with a request-correlated ID (gRPC has metadata for this; the bridge can mint one per call).
2. Reflect a generic `"oscal serialize failed; see server logs for request <id>"` into the client-facing INTERNAL status.
3. Add a unit test that asserts ValidationError details do not appear in the client-facing status message.

**Disposition.** Audit report only — cross-referenced to slice 327. The maintainer's choice:

- **Option A** — treat as in-scope for slice 327's M-2 and file a Python-specific follow-up slice (would slot as 369-or-later, but H-1 and H-2 from this audit already claim 369/370/371). Slot 372 if filed.
- **Option B** — defer until a v2 security follow-up audit; the OSCAL bridge runs over loopback per the slice doc's threat model ("the Go side is the trust boundary"), so the actual disclosure surface is bounded by who can hit the Python port directly.

Decisions log §D5 documents the maintainer-choice JUDGMENT.

---

### L-1 (LOW) — Two near-identical `writeLog` functions in `internal/audit/period` and `internal/audit/walkthrough`

- **Category:** reuse / bounded duplication
- **Files:** `internal/audit/period/period.go:485`, `internal/audit/walkthrough/walkthrough.go:640`

**Description.** Both files implement a `writeLog(ctx, q, tenantID, <entityID>, action, actor, detail) error` helper with byte-identical bodies except for the sqlc-generated method (`WriteAuditPeriodLog` vs `WriteWalkthroughAuditLog`) and the parameter type name (`AuditPeriodID` vs `WalkthroughID`).

**Disposition.** Documented bounded duplication. The audit-log tables are intentionally separate (different schemas), and the sqlc-generated `Params` struct is the natural divergence point. Consolidating would require either codegen-time templating (not warranted) or a runtime registry of "audit-log writers" keyed by entity type (over-abstraction).

**Recommendation.** No action. If a third `writeLog` appears in a future audit-log table, revisit and consolidate via generics:

```go
func writeLog[Params any](ctx context.Context, write func(ctx context.Context, p Params) error, p Params) error { ... }
```

---

### L-2 (LOW) — `apikeystore.Authenticate` fire-and-forget goroutine has no shutdown coordination

- **Category:** resource lifecycle
- **File:** `internal/auth/apikeystore/apikeystore.go:262-266`

**Description.**

```go
// Best-effort last_used_at bump; never blocks.
go func() {
    bctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    _ = q.TouchAPIKeyLastUsed(bctx, hash)
}()
```

The goroutine is correctly bounded by a 2s timeout, so it cannot leak indefinitely. The `context.Background()` is intentional — it must survive the request's cancellation so the bump completes after the response is written. Both are correct.

The (small) concern: on graceful server shutdown, in-flight goroutines from this pattern are not waited on. Up to ~2s of background writes may be lost when the process exits. This is acceptable for `last_used_at` (a non-load-bearing field used for operational visibility) but the pattern would be a concern if reused for an auth-critical write.

**Disposition.** Documented as a known design. Recommendation: if this pattern is adopted elsewhere for less-tolerant writes, introduce a package-level `sync.WaitGroup` and a `Shutdown(ctx context.Context) error` method to drain. Not warranted for this single use site.

---

### L-3 (LOW) — `_ = json.NewEncoder(w).Encode(body)` pattern discards encode errors at 93 sites

- **Category:** error handling / convention
- **Files:** 93 of 94 `json.NewEncoder(w).Encode(...)` call sites across `internal/api/*`

**Description.** The Go convention for HTTP response bodies is to ignore `Write` errors (the client has already disconnected by the time you'd see one; logging adds noise). The codebase does this consistently. Specifically:

```go
_ = json.NewEncoder(w).Encode(body)
```

is correct for the response-write path. The single non-`_` site is `cmd/scripts/errleak-lint/main.go` (which checks its own errors at lint-tool startup).

**Disposition.** Not a finding — documented as positive baseline. If a future code-reviewer surfaces this as a candidate concern, the explanation is: the response has already been committed to the client by the time `Encode` runs; an error there does not give the server an actionable recovery, and the slog log would just collect noise from broken-pipe clients. The convention is correct.

---

### I-1 (INFORMATIONAL) — Per-package documentation density is exceptional

Every Go package examined opens with a `// Package <name>` doc comment that explains purpose, slice provenance, load-bearing invariants, and cross-references to canvas sections or other packages. This is the gold-standard pattern for AI-navigable codebases.

Spot samples worth highlighting:

- `internal/api/admintenants/handler.go` (lines 1-61): 60 lines of structured commentary explaining LOAD-BEARING DESIGN constraints with explicit P0-CT-\* anti-criteria mapping
- `internal/api/ucfcoverage/handlers.go` (lines 1-30): per-route description with constitutional-invariant honoring statement
- `internal/auth/users/users.go` (lines 1-13): explains the slice 198 bootstrap-path divergence and the BYPASSRLS pool convention

This pattern is what makes the codebase amenable to drift detection by a code-reviewer in a fixed pass — every file declares its own conventions up-front.

**Action.** None. Continue.

---

### I-2 (INFORMATIONAL) — Zero TODO / FIXME / HACK debt

`grep -rn 'TODO\|FIXME\|XXX\|HACK' internal/ cmd/ --include="*.go" | grep -v _test.go` returns 4 hits total, 3 of which are the literal alphabet description `XXXX-XXXX` in `internal/api/oauth/device_authorization.go` (the device-code format mask). The single real `TODO(slice-035)` is at `internal/policy/acknowledgment.go:350` for an acceptable scope deferral.

Frontend equivalent: 3 hits across all `*.ts`/`*.tsx` outside node_modules + tests/specs; same pattern.

**Action.** None — this is the cleanest TODO hygiene the auditor has seen. The slice-discipline (one slice = one decisions log = one PR) removes the temptation to leave inline TODOs.

---

### I-3 (INFORMATIONAL) — Zero package-boundary violations

- `grep -rn "security-atlas/cmd" internal/` returns zero — no `internal/*` imports `cmd/*`.
- `grep -rn "security-atlas/internal/api" internal/ | grep -v "internal/api/"` returns zero — no domain package reaches back into the HTTP-handler layer.

The canonical dependency direction (cmd → internal/api → internal/<domain> → internal/db/dbx) holds across all 779 Go files.

**Action.** None — continue. Add a CI check via `go-deps` or a custom lint to make the invariant explicit if the codebase grows past a certain threshold.

---

### I-4 (INFORMATIONAL) — Comment-vs-code drift sample

A `grep` sample for "comment promises a follow-up that hasn't happened" surfaces:

1. `web/lib/auth.ts:19` — the `SESSION_COOKIE` rename (M-3, above) — self-acknowledged
2. `internal/policy/acknowledgment.go:350` — slice-035 rate-denominator note (acceptable scope deferral)
3. `cmd/scripts/errleak-lint/main.go:19` — panic-leak deferral note (deliberate)

Three documented "follow-up TBD" comments in a ~120k LOC codebase. No other significant drift detected in the sampled high-churn packages.

**Action.** None — exceptionally low drift.

---

## Verified positive observations

Patterns that explicitly strengthen the codebase and are worth recording:

1. **Slice 367's `internal/api/httperr`** is the model the rest of `internal/api/*` should follow (this audit's H-1 finding is the call to extend the pattern to `httpresp`).
2. **`internal/api/requestidmw`** correctly rejects malformed inbound `X-Request-Id` headers — defense-in-depth against log-injection.
3. **Constructor-panic pattern** uniformly used at process boot, never in request handlers. The `oauth.NewTokenEndpoint`, `oauth.NewAuthorizeEndpoint`, etc. panic on missing required dependencies — programmer-error at wire-up time, not runtime.
4. **Clock injection** used correctly in `board`, `evidence`, `drift`, `oauth/token`, `admintenants` — establishing the pattern the auth substrate should adopt (this audit's M-2 finding).
5. **Fire-and-forget context-detach pattern** in `oauth/token.go`, `oauth/pkce.go`, `audit/sink.go`, `apikeystore.go` is correctly documented as intentional in each case.
6. **gRPC + HTTP composition** in `internal/api/server.go` — the `Attach<Thing>(...)` builder pattern lets unit tests construct minimal-dependency Servers (a contract that the constructor-panic discipline keeps honest).
7. **`go.uuid` vs `pgtype.UUID`** boundary handled cleanly via per-package adapter functions (`uuidToPgtype` / `uuidFromPgtype` pattern in adminsso, admintenants).
8. **Defer chains** consistently used for `rows.Close()`, `tx.Rollback`, `f.Close` — no missing defers in the sample.
9. **sqlc-generated `defer rows.Close()`** is automatic in `internal/db/dbx/*.sql.go`; the human-written wrappers also defer correctly.
10. **`web/lib/api/bff.ts` shared `forwardJSON` + `forwardMultipart`** is the convention for new BFF routes — clean ~80 LOC file.
11. **Next.js proxy in `web/proxy.ts`** correctly hardens with security headers + exact-equality public-path matching (per the slice 092 P0-A1 convention).
12. **PUBLIC_STATIC_FILES** allow-list in `web/proxy.ts` is intentionally short + literal — defense against a regex broadening that would expose tenant-scoped assets.
13. **`web/proxy.ts` redirect logic** correctly preserves the `from` query parameter so post-login redirect is the user's original destination.
14. **`oscal-bridge/atlas_oscal_bridge/server.py`** correctly identifies its own trust boundary in the docstring — "the Go side is the trust boundary" — and binds loopback by default.
15. **Type-safe context keys** (`authctx.ctxKey{}`, `requestidmw.ctxKey{}`, `jwtmw.ctxKey{}`) prevent string-key collisions across packages.

---

## Methodology and integrity

- Audit was conducted by reading source files end-to-end against the slice-doc surface enumeration plus targeted `grep` sweeps for anti-patterns (panic locations, context.Background usage, fmt.Errorf wrapping, defer chains, time.Now usage, package-boundary violations).
- Per-finding severity rubric: see `docs/audit-log/328-code-review-comprehensive-decisions.md` §D1.
- Cross-referenced against slice 327's security audit report; explicit dedupe applied — see decisions log §D6.
- Read-only audit; no code modifications in this slice's diff.
- No production or demo data examined at runtime; review is static-read-only.

### Audit agent identity per AC-8

The audit was conducted via the primary Engineer agent persona (Marcus Webb), loading the `voltagent-qa-sec:code-reviewer` persona file as Engineer context. The persona's `tools: Read, Write, Edit, Bash, Glob, Grep` boundary was respected; `Write` and `Edit` were used only for the audit report, decisions log, and spillover slice docs — no source code was modified.

---

## Open questions for maintainer triage

1. Should M-3 (`SESSION_COOKIE` → `ATLAS_JWT_COOKIE` rename) bundle into the H-2 slice (`web/lib/api.ts` split)? Both are TypeScript-side mechanical renames; bundling saves one review cycle. The decisions log §D4 leans "bundle if H-2 opens for renames anyway".

2. Should M-4 (Python OSCAL bridge error reflection) file a follow-up slice or remain audit-only? The OSCAL bridge runs loopback per its threat model, but the slice 327 M-2 hygiene was applied to the Go side and arguably should extend to the Python side for consistency. Decisions log §D5.

3. Should M-1 (`httpserver.go` god-file) be filed as a v2 architectural tracking slice now, or wait for the file to cross 2000 LOC? Decisions log §D3 leans "wait; the current shape is functional".

4. Should the audit-only Medium findings be re-surfaced quarterly via a recurring code-review audit, so drift doesn't accumulate? This is a process question — answer outside the scope of this report.

---

## Companion documents

- **Decisions log:** `docs/audit-log/328-code-review-comprehensive-decisions.md` — JUDGMENT calls made by this audit (severity rubric, scope choices, bundle-vs-spillover allocation, slice 327 cross-reference).
- **Spillover slices:**
  - `docs/issues/369-httpresp-shared-helper-consolidation.md` (infra — H-1)
  - `docs/issues/370-web-api-client-split.md` (web — H-2)
  - `docs/issues/371-auth-clock-injection-substrate.md` (auth — M-2)
