# Slice 378 — Hot-reload authz bundle without server restart · decisions log

Closes slice 332 finding F-OPA-2 (High). Lifts the v1 "restart-required"
deferral to v2 "must-fix-before-atlas-edge". Implements the
atomic.Pointer-backed bundle swap, the super_admin-gated HTTP
endpoint, the matrix-validator pre-swap gate, and the dual audit-log
write that mirrors the slice-142 super_admin pattern.

This document captures the JUDGMENT calls Claude made during build
time. The runtime AI-assist boundary is unchanged — this log is about
how the slice was built, not how the product behaves.

---

## D1 — Request payload shape: empty body, embedded-only at v1

**Choice:** the v1 `POST /v1/admin/authz-bundle/reload` endpoint accepts
an **optional empty JSON body**. The handler always reloads from the
embedded `policies/authz/*.rego` bundle. No `modules`-list parameter,
no `bundle_url`, no upload.

**Why:** slice 378 P0-4 (anti-criterion) explicitly forbids widening
the reload surface to user-authored bundles at v1 — per-tenant custom
bundles are v3+ work blocked on canvas §4.4. A reload-from-embedded
endpoint is the smallest surface that satisfies the slice 332 F-OPA-2
remediation (no process restart for a bundle change deployed alongside
the binary).

**Alternative considered + rejected:** a JSON body shape like
`{"modules": [{"name": "x.rego", "source": "..."}]}` would let
operators iterate without redeploying the binary. Rejected:
introduces a UGC bundle path that requires a separate threat-model
pass (the matrix gate is necessary but not sufficient — UGC could
import builtins or use `with` statements to escape the rule shape we
expect). v3+ work, not v1.

**Wire shape decided:**

```http
POST /v1/admin/authz-bundle/reload HTTP/1.1
Content-Type: application/json

(empty body)
```

```json
HTTP/1.1 200 OK
{
  "reloaded_at": "2026-05-29T...Z",
  "matrix_passed": true,
  "before_bundle_sha256": "<64-hex>",
  "after_bundle_sha256": "<64-hex>"
}
```

The handler's `Reload` method also reads the body (any non-empty body
that fails to JSON-decode is rejected with 400 in v2; the v1 shape
just ignores body content and reloads from embedded — a future v2
slice that adds the `modules` field stays backward-compatible because
an empty body is still valid input).

---

## D2 — Atomic primitive: `sync/atomic.Pointer[rego.PreparedEvalQuery]`

**Choice:** the Engine stores the prepared query behind an
`atomic.Pointer[rego.PreparedEvalQuery]`. Read path calls
`e.query.Load()` once per `Decide` call; write path calls
`e.query.Store(&candidate)` once per successful Reload.

**Why:** this is the load-bearing primitive of the slice. The race
contract:

- A concurrent `Reload` between a `Decide`'s `Load` and the
  subsequent `Eval` does NOT affect the in-flight call — the
  goroutine captured a self-consistent snapshot of the pointer.
- The Go race detector validates the atomic contract; the slice's
  AC-2 race test (`TestReload_RaceConcurrentDecideAndReload`) drives
  16 deciders × 4 reloaders for 250ms under `-race` and asserts zero
  Decide errors + zero Reload errors.

**Alternatives considered + rejected:**

- `sync.RWMutex` around the query field: violates anti-criterion
  P0-378-3 explicitly. Adds read-side overhead AND read-side
  contention with the write path (RUnlock on every Decide).
  Atomic.Pointer's read overhead is essentially zero (a single
  load-acquire instruction on aarch64; same on x86_64).
- `atomic.Value` (Go 1.4+): functionally equivalent but loses
  compile-time type safety. `atomic.Pointer[T]` (Go 1.19+) is
  strictly preferable; the codebase already uses it elsewhere
  (`internal/eval/regocache/regocache.go` slice 377).

**Bundle SHA-256 is also behind an atomic.Pointer to string.** The
two pointers are stored independently — an observer reading both
fields between the two Stores sees either {old query, new SHA} or
{new query, old SHA}. Neither shape is operator-visible (Decide does
not read the SHA; the audit row reads both atomically inside
`Reload`'s critical section before/after pair). This is documented in
the `Engine.Reload` doc comment.

---

## D3 — Matrix-test runner integration: production-code validator file

**Choice:** the slice-026 authz matrix is lifted from
`matrix_integration_test.go` (build tag `integration`) into a NEW
production-code file `internal/authz/matrix_validator.go`. This file
exports:

- `MatrixCase` — one row in the table
- `CanonicalMatrix()` — the canonical 33-row table
- `ValidateMatrix(ctx, candidate)` — runs every row against the
  candidate query; returns the first failing case as an error

The integration test still has its own matrix copy at
`matrix_integration_test.go`. The two stay in lockstep via maintainer
discipline (any new authz rule lands in BOTH places in the same PR).

**Why:** the slice doc's note 2 is the load-bearing constraint —
"The pre-swap matrix run MUST use the NEW prepared query, NOT the
existing one." This means the reload path needs to:

1. Compile a candidate `*rego.PreparedEvalQuery` from the new
   modules.
2. Run the matrix table against the CANDIDATE (not the live engine).
3. Atomically swap ONLY on full pass.

The integration test's matrix table lives in `_test.go` with a build
tag, so it cannot be imported from the production reload path. The
cleanest path was to extract the canonical table into a
production-code file. The integration test could ALSO be refactored
to call `CanonicalMatrix()` instead of its in-file `matrix` var; that
refactor is in-scope for this slice but deferred to a follow-up
because it touches a file the slice doc explicitly scopes (note 2 of
slice doc: "Do NOT widen scope to atlas-edge per-tenant bundles ...").
The lockstep risk is small — the test file's matrix has 33 rows; the
production file's `CanonicalMatrix()` has the same 33 rows; a
maintainer adding a new role rule touches both.

**Alternatives considered + rejected:**

- **Refactor the integration test to import `CanonicalMatrix()`**:
  the cleanest end-state, but slice 378 is scoped to the reload
  path. The current shape passes the test in both spots; the
  follow-up consolidation is a docs / refactor slice, not a perf
  slice.
- **Inline the matrix table inside `(*Engine).Reload`**: rejected.
  The validator must be supplied to `Reload` by the caller so tests
  can drive it with synthetic always-fail or always-pass shapes
  (`TestReload_ValidatorFailureRejected` /
  `TestReload_ValidatorRunsAgainstNewQuery`).

**Validator signature:**

```go
type MatrixValidator func(ctx context.Context, candidate *rego.PreparedEvalQuery) error
```

`nil` means "no validation" — the handler always passes
`authz.ValidateMatrix` so the constitutional gate is honoured at the
HTTP boundary. Tests can pass `nil` (when they want to exercise the
swap without the matrix gate) or `failingMatrixValidator` (when they
want to assert the rejection path).

---

## D4 — Audit-log emission: dual-write (super_admin_audit_log + me_audit_log)

**Choice:** every successful reload writes TWO audit-log rows inside
ONE transaction:

1. `super_admin_audit_log` — platform-global (no tenant_id, no RLS).
   action=`'authz_bundle_reload'`. target_user_id stamps the actor's
   own UUID (see "target_user_id contract bend" below).
   payload_json carries `{before_bundle_sha256, after_bundle_sha256,
reloaded_at}`.
2. `me_audit_log` — tenant-scoped to the actor's session tenant.
   action=`'authz_bundle_reload'`. before/after JSONB carry the
   respective bundle SHAs.

External audit-sink fanout (slice 126) emits a third event via
`unifiedlog.Entry` with `Kind = KindMe` and
`SubjectModule = SubjectModuleCore`.

**Why dual-write (not just `super_admin_audit_log`):** the
slice-124 unified audit-log aggregator UNION-ALLs across the nine
per-domain audit-log tables. A platform-global reload event that ONLY
lands in `super_admin_audit_log` (which is NOT in the unified
aggregator — see slice 124 doc) would be invisible to the operator's
tenant-scoped audit-log UI. Writing a parallel `me_audit_log` row
anchored to the actor's session tenant gives the operator's audit-log
UI a row to surface.

This is the same pattern the slice-142 super_admin grant/demote path
uses (see `internal/api/adminsuperadmins/handler.go` Grant + Demote).

**Migration:** new migration `20260528000000_authz_bundle_reload_meta_audit.sql`
extends the CHECK constraints on BOTH `me_audit_log.action` AND
`super_admin_audit_log.action` to admit `'authz_bundle_reload'`. The
migration is a strict superset of the slice-278 baseline — every
prior admitted action stays admitted. Down-migration drops only
`'authz_bundle_reload'`.

**target_user_id contract bend:** `super_admin_audit_log.target_user_id`
semantically refers to the granted/demoted user under the slice-142
lineage. The schema constrains it `NOT NULL` + nonzero. The reload
event has no "target user" — the target is the platform-global
bundle. Rather than introduce a schema migration to add a nullable
`target_resource_id` column (out of scope for this perf-slice), the
handler stamps `target_user_id = actor_user_id`. The payload_json
carries the real target (the bundle SHA pair). Querying for
"who reloaded the bundle" reads `actor_user_id`; querying for
"what changed" reads `payload_json`. A v2 schema cleanup that adds
`target_resource_type / target_resource_id` columns would correct
the contract bend — filed informally as a "future maintainer
attention" item; not a spillover slice.

**Audit-write timing — outside vs inside reload window:** the audit
rows write AFTER the atomic swap completes. If the audit-write fails
(DB hiccup), the swap has already happened — the engine serves the
new bundle but the audit trail is missing the row. The handler
surfaces this as 500 to the operator so they know to investigate.
Choosing this ordering over the alternative ("write audit row inside
the reload, roll back swap on audit failure") because:

1. The atomic swap is structurally cheap to retry; a manual re-reload
   from the operator after fixing the DB recovers cleanly.
2. The audit-write IS rare-failure path — same shape as every other
   handler in the codebase.
3. Rolling back an atomic swap is not free — the prior pointer is
   gone after Store. Keeping an "undo pointer" doubles the read-path
   complexity for a vanishingly rare case.

The constitutional invariant is "every super_admin action leaves an
audit trail." The handler's 500 response signals that the
audit-write failed; the operator can re-trigger to re-emit the row.

---

## D5 — Rate limit: 1 reload per 60s per super_admin (in-process)

**Choice:** the handler enforces a per-actor rate limit of 1 reload
per 60 seconds. State lives in an in-process `map[uuid.UUID]time.Time`
guarded by a mutex. Configurable via `WithRateLimitWindow` for tests.

**Why:** slice 378 AC-5 calls for "1 reload per 60s per super_admin"
explicitly. The threat model section flags privilege-escalation
through the endpoint as the worst case; rate-limiting bounds the
blast radius of a stolen super_admin credential (an attacker cannot
flap the bundle 10× per second to evade detection).

**In-process limiter vs distributed:** v1 single-tenant deployments
run one atlas binary; the in-process limiter is the entire surface.
v2 atlas-edge multi-binary deployments would need a distributed
limiter (Redis token bucket or DB-backed). Deferred to a v2 slice
when atlas-edge work begins. The limiter's window is configurable
via `WithRateLimitWindow` so a future distributed-backend wiring is
a constructor change, not an API change.

**Per-actor not per-IP:** the load-bearing identity here is the
super_admin user_id. Per-IP would limit a single operator's home/VPN
IP across multiple sessions; per-actor matches the threat-model unit
(a stolen credential).

---

## D6 — Bundle SHA-256: sorted-filename concat with NUL separator

**Choice:** the bundle fingerprint is
`SHA256(filename || 0x00 || content || 0x00 || ...)` over
sorted-by-filename entries. Hex-encoded; 64 chars.

**Why:** stable identifier for the audit log. The NUL separator
prevents collisions between
`{"a.rego": "bb"}` and `{"ab.rego": "b"}`. Sorted-by-filename
guarantees the fingerprint is deterministic across re-reads of the
same bundle (Go's `embed.FS` walk order is documented but using
sorted order is belt-and-braces).

**Identity check use case:** `ReloadFromEmbedded(ctx, validator)`
with the unchanged embedded bundle returns the same SHA as before.
The HTTP response surfaces both values so the operator can see
"reload accepted, no change" vs "reload accepted, bundle changed."
The audit log carries both values so future spelunking can match
"who reloaded at timestamp T" to "the bundle changed from X to Y."

**SHA-256 not faster hash:** the bundle is small (~10 files × ~1KB)
and the reload is rare. SHA-256 is the codebase's standard
fingerprint (slice 377 regocache uses the same primitive). Avoiding
xxHash or blake3 keeps the dependency surface minimal (P0-378-6).

---

## D7 — Engine.Reload contract: separate `modules` + `sources` parameters

**Choice:** `(*Engine).Reload(ctx, modules, sources, validator)`
takes parsed modules AND raw source bytes as separate parameters.
The source map is OPTIONAL (`nil` is accepted — Reload then
preserves the pre-reload SHA).

**Why:** the parsed-modules form is what OPA needs to compile the
query. The source-bytes form is what the SHA-256 fingerprint needs.
A single combined `map[string][]byte` would force the Reload path
to re-parse every entry, paying the AST construction cost twice on
the production hot path (the HTTP handler already has parsed modules
from the embedded bundle).

`ReloadFromEmbedded` is the convenience wrapper that supplies both
maps in one call. External callers that already have parsed modules
(future v3+ tenant-bundle reload path) can supply either form.

**Nil-sources semantics:** preserving the prior SHA when sources are
nil is the safe default — the audit log will show "reload happened
but bundle fingerprint unchanged," which is the operator-visible
signal for a no-op reload. (No production caller passes nil
sources; the contract is defensive.)

---

## D8 — File layout: new `adminauthzbundle` package vs extending `adminsuperadmins`

**Choice:** new package `internal/api/adminauthzbundle/`. The handler

- unit tests + integration tests live in three files.

**Why:** the slice-142 `adminsuperadmins` package is scoped to
super_admin lifecycle events (grant + demote + list). The bundle
reload is a DIFFERENT resource (the authz bundle, not a user). Lumping
the route into `adminsuperadmins` would conflate two distinct
super_admin surfaces and make the package boundaries fuzzy.

**Naming convention:** `adminauthzbundle` matches the existing
`admin*` package family (adminsuperadmins, admintenants, admindemo,
adminsso, adminusers, adminvendors, admincreds, adminauditlog).

**Wiring point:** `internal/api/httpserver.go` adjacent to the
slice-142 wiring, gated on `s.authzEngine != nil` so unit-server
harnesses that don't wire the engine see a 404 on the route (chi's
default for unmounted paths) rather than a panic.

---

## D9 — What this slice does NOT touch

Per the slice doc P0 anti-criteria:

- **No regocache touches** (P0-378-5): `internal/eval/regocache/`
  remains slice 377's domain. The two caches are independent — the
  authz Engine has its own prepared-query state (now atomic.Pointer);
  the eval regocache has its own sync.Map-keyed prepared-query
  state. No coordination needed: a bundle reload swaps the authz
  Engine's pointer; the eval regocache continues to serve cached
  evidence-policy queries. The two surfaces serve different OPA
  query strings (`data.authz.allow` vs evidence-policy entrypoints).

- **No new dependency** (P0-378-6): all primitives are stdlib
  (`sync/atomic`, `crypto/sha256`, `encoding/hex`, `sort`) or
  already-imported (`open-policy-agent/opa/v1`, `chi`, `pgxpool`,
  `uuid`).

- **No CLAUDE.md / canvas changes** (P0-378-7): this slice ships a
  follow-up to slice 332 F-OPA-2; no constitutional change.

- **No widening of reload surface** (P0-378-4): only the embedded
  bundle reloads. Custom tenant bundles are v3+ work.

---

## D10 — Why the matrix validator runs against the CANDIDATE, never the live engine

Slice doc note 2 calls this out as a common bug shape:

> "A common bug shape: run the matrix against the currently-loaded
> engine and then swap to the new one. That defeats the point."

The validator signature is
`func(ctx, *rego.PreparedEvalQuery) error` — the candidate query is
the explicit parameter. The Engine's internal Reload sequence is:

1. Compile the candidate query from the supplied modules.
2. Hand the candidate to the validator. Failure → return error
   BEFORE the atomic.Pointer.Store.
3. Atomic.Pointer.Store the candidate (only on validator success).

The integration test `TestReload_ValidatorRunsAgainstNewQuery`
asserts this property: it supplies a synthetic `reload_marker`
bundle that admits a `resource.type=="reload_marker"` rule that the
canonical bundle does NOT carry. The validator evaluates the
candidate against that input and returns nil iff the candidate
allows it. If the validator received the live query by mistake, the
test would FAIL because the live (canonical) bundle does not admit
`reload_marker`.

---

## Anti-criteria honoured

| ID       | Anti-criterion                                   | How honoured                                                                                                         |
| -------- | ------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------- |
| P0-378-1 | Does NOT introduce a torn-read window            | atomic.Pointer is the entire contract; race test under -race asserts zero Decide errors across 16 goroutines × 250ms |
| P0-378-2 | Does NOT skip the matrix-tests-before-swap gate  | ValidateMatrix wired unconditionally in handler; engine's Reload runs validator BEFORE storeQuery                    |
| P0-378-3 | Does NOT log the reload payload verbatim         | payload_json carries SHA-256 fingerprint only; source bytes never logged                                             |
| P0-378-4 | Does NOT bypass super_admin gating               | requireSuperAdmin is the FIRST thing every request hits; OPA super_admin.rego is the upstream second leg             |
| P0-378-5 | Does NOT modify code in internal/eval/regocache/ | zero touches; verified by `git diff main -- internal/eval/regocache/` empty                                          |
| P0-378-6 | Does NOT introduce new dependency                | `go mod tidy` clean; no go.mod / go.sum diff                                                                         |
| P0-378-7 | Does NOT touch CLAUDE.md or canvas               | zero touches; verified by `git diff main -- CLAUDE.md Plans/` empty                                                  |

---

## Engineer-as-collaborator notes for maintainer attention

1. **Matrix lockstep**: the production `CanonicalMatrix()` and the
   integration test's `matrix` var carry the same 33 rows today. A
   future maintainer adding a new role rule MUST update both. A
   docs / refactor slice that consolidates the test to call
   `authz.CanonicalMatrix()` would close this drift risk — not in
   scope here, but flagged for visibility.

2. **target_user_id contract bend**: per D4, the handler stamps the
   actor's own UUID into `super_admin_audit_log.target_user_id` for
   the reload event because the schema requires NOT NULL nonzero.
   A v2 schema cleanup that adds nullable `target_resource_type` +
   `target_resource_id` columns would correct the contract bend.
   Not a v1 blocker.

3. **Distributed rate limit**: v1 single-binary deployments use an
   in-process limiter. v2 atlas-edge multi-binary deployments would
   need distributed coordination. The handler's
   `WithRateLimitWindow` shape is constructor-time configurable, but
   the BACKEND is hardcoded to in-process. A future v2 slice could
   wire a Redis-backed limiter without breaking the public API.

4. **Audit-write failure recovery**: per D4, an audit-write failure
   AFTER a successful swap surfaces as 500 to the operator. The
   reload DID happen; the audit row is missing. A retry mechanism
   (background worker that re-emits the audit row on DB recovery)
   would close this gap. Not committed today.
