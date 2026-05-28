# 371 — Auth-substrate clock injection (decisions log)

**Slice:** [`docs/issues/371-auth-clock-injection-substrate.md`](../issues/371-auth-clock-injection-substrate.md)
**Audit reference:** [`docs/audits/328-code-review-comprehensive-report.md`](../audits/328-code-review-comprehensive-report.md) finding **M-2**
**Type:** JUDGMENT (1d)
**Branch:** `auth/371-clock-injection`

## What shipped

Three auth packages converge on the established clock-injection
convention previously used by `internal/board`,
`internal/evidence/ingest`, `internal/drift`, and
`internal/api/admintenants`:

- `internal/auth/sessions/Store` gains a `clock func() time.Time` field
  alongside a `WithClock(fn) *Store` setter. The three drift sites at
  `sessions.go:137,183,188` route through `s.clock()`.
- `internal/auth/apikeystore/Store` gains the same field and setter. The
  three drift sites at `apikeystore.go:165,251,293` route through
  `s.clock()`.
- `internal/auth/jwtmw` — the package already exposed
  `Options.Now func() int64` as the injection point. The slice's drift
  site was the nil-fallback at `middleware.go:298` which returned
  `time.Now()` in host-local zone. Slice 371 normalizes it to
  `time.Now().UTC()` for parity with the rest of the codebase.

Fourteen new unit tests ship across three new `clock_test.go` files
(sessions: 5; apikeystore: 5; jwtmw: 4). Each exercises a boundary
case the slice doc calls out — TTL→expired transition at exactly
`now + ttl`, rotation-grace at `T + grace`, JWT validity at exact
`iat = nbf`, JWT rejection at exact `exp` and one second before `nbf`.

## Decisions

### D1 — Setter shape: mutate-and-chain (mirror admintenants)

**Choice:** `WithClock(fn func() time.Time) *Store` — mutates the
receiver, returns the receiver for chain construction, includes a nil
guard that no-ops on `WithClock(nil)`.

**Alternative considered:** the `*-copy-on-set` shape used by
`internal/auth/oauthcode` and `internal/evidence/ingest`
(`cp := *s; cp.clock = fn; return &cp`).

**Rationale:**

- Both shapes are present in the codebase, but the slice doc + the
  orchestrator brief explicitly call out the
  `internal/api/admintenants/handler.go:185` shape as the target —
  mutate-and-chain.
- The mutate-and-chain shape avoids the copy-on-each-call cost for
  Stores that carry expensive fields (here: pgxpool.Pool pointers
  cost a pointer-copy, which is cheap; but the principle scales).
- The copy-on-set shape preserves an immutable receiver, which is
  marginally safer if multiple goroutines share a Store reference
  during test construction — but in practice tests run sequentially
  and call WithClock once, immediately after construction.
- The nil guard exists so a test that accidentally passes a nil
  callback doesn't crash with a nil-function-pointer panic on the
  next call to `s.clock()`. Pre-371 there is no analogous panic
  path; the guard is cheap and defensive.

### D2 — Default clock: UTC (matches established baseline)

**Choice:** `func() time.Time { return time.Now().UTC() }` — matches
the canonical shape in `internal/board/generator.go:61`,
`internal/evidence/ingest/ingest.go:195`, `internal/drift/drift.go:67`.

**Alternative considered:** `time.Now` directly (a method value),
which is what `internal/auth/oauthcode/oauthcode.go:118` uses today.

**Rationale:**

- UTC is the documented baseline — every persisted timestamp in the
  platform is stored as `timestamptz` (per migrations) and JSON-
  serialized as RFC 3339 with a Z suffix. Carrying a host-local
  zone through the Store layer risks subtle off-by-one-zone bugs
  near midnight UTC.
- The cost of `.UTC()` is one zone-pointer assignment; not measurable.
- The `internal/auth/oauthcode` default (`time.Now` host-local) is a
  pre-existing minor drift from this convention — noted as a
  follow-up candidate (D4 below). Slice 371 does not change it;
  the slice's scope is the three packages called out by the audit
  finding M-2.

### D3 — jwtmw retains the `func() int64` clock shape

**Choice:** `internal/auth/jwtmw.Options.Now` stays as
`func() int64` (Unix seconds), not converted to
`func() time.Time`. The slice 371 normalization is limited to making
the nil-fallback at `middleware.go:nowTime` return
`time.Now().UTC()` (was `time.Now()`).

**Alternative considered:** unify by replacing `Options.Now func() int64`
with `Options.Clock func() time.Time`, matching the Store-style.

**Rationale:**

- The downstream consumers — `tokensign.Signer.Verify` and
  `jwt.Validate` — operate on Unix-seconds integers (`int64`). The
  current shape lets the middleware pass `opts.Now()` directly
  without any int64↔time.Time hop.
- Converting at the call site would push the same hop into every
  caller (the middleware itself, plus any future callers) for zero
  semantic gain.
- The middleware's existing tests (`middleware_test.go`) already use
  the int64 hook via the `nowAt(time) func() int64` helper —
  swapping the shape would force every test to refactor.
- The slice doc's AC-2 explicitly allows the Middleware-style
  variant: "a `Clock` config field (Middleware-style)" — `Options.Now`
  IS the Clock config field.

**Future shape change deferred to v2 if/when needed:** if a future
slice introduces a clock-driven path in jwtmw that needs the
`time.Time` shape natively (e.g., for sub-second precision in
revocation TTLs), the `Options.Now` shape can be extended additively
without breaking the current contract.

### D4 — Engineer-as-collaborator: minor drift in oauthcode

While sweeping the three target packages, one adjacent drift surfaced
that is OUT OF SCOPE for slice 371 but worth noting:

- `internal/auth/oauthcode/oauthcode.go:118` initializes its `now`
  field to `time.Now` (the method value) rather than
  `func() time.Time { return time.Now().UTC() }` (the codebase
  baseline shape). This means oauthcode's default clock carries the
  host-local zone, which works correctly today because the consumers
  only read Unix() / seconds-since-epoch values, but it is a minor
  drift from the convention.

**Decision:** DO NOT widen slice 371 scope. The slice was specifically
sized at 1d JUDGMENT for the three packages called out by audit
finding M-2. The orchestrator brief explicitly directs:
"if you find another auth file with raw `time.Now()` for TTL math
beyond the 7 listed, note in PR body for slice 372 follow-up but DO
NOT widen scope here." Slice 372 (or a successor) can address
oauthcode's default shape in a focused follow-up.

### D5 — Test scope: pure unit, no DB

**Choice:** all six new tests are in-package unit tests, no `//go:build
integration` tag, no DB requirement. They exercise the clock-injection
SEAM — the setter, the nil-guard, the default-zone contract, and the
boundary arithmetic — but do not exercise the full Create/Read/Issue/
Authenticate code paths (those are DB-bound and live in their
respective integration suites: `internal/auth/integration_test.go`
for sessions/apikeystore, `internal/auth/jwtmw/integration_test.go`
for jwtmw).

**Rationale:**

- The slice doc invariants ("session VALID at T+3599, INVALID at
  T+3600+ε") are expressible at the arithmetic level — the predicate
  is `now.After(expiresAt)` regardless of whether the timestamp came
  from a DB row or a Go literal.
- DB-bound tests would force this slice to enroll a Postgres test
  fixture, which adds CI surface area (slice 069 integration
  enrollment) for marginal additional coverage — the
  clock-injection seam itself is the load-bearing change, and that
  seam is fully testable without a DB.
- The integration suites already exist for the DB-bound paths; this
  slice is intentionally scoped to the seam.

### D6 — sessions Read path: `time.Until` → explicit subtraction

While sweeping the three sessions.go drift sites, the
sliding-window-refresh predicate at `sessions.go:187` used
`time.Until(row.ExpiresAt.Time) < RefreshThreshold`. `time.Until`
reads the wall clock internally — it is conceptually a fourth drift
site that the slice doc didn't itemize.

**Choice:** replaced with explicit
`row.ExpiresAt.Time.Sub(now) < RefreshThreshold` where `now` is
obtained from the same `s.clock()` call that the expiry check at line
183 uses. This pins the refresh predicate to the same injected clock
— a session checked at exactly RefreshThreshold-back-from-expiry is
not refreshed; one nanosecond closer to expiry, it is.

**Rationale:** keeping `time.Until` would have created a subtle
clock-source mismatch — the test's pinned clock would drive the
expiry check but the refresh predicate would silently read wall-clock,
producing nondeterministic refresh behavior under a pinned clock.
This is a tiny, safe, in-scope correction that makes the seam
complete.

## Acceptance criteria (per slice doc)

- [x] AC-1: `internal/auth/sessions/Store`,
      `internal/auth/apikeystore/Store`, `internal/auth/jwtmw/Middleware`
      each carry a `clock`/Now injection point with a UTC-returning
      default.
- [x] AC-2: Setter (Store: `WithClock(fn) *Store`) or config field
      (Middleware: `Options.Now`) exposed on each.
- [x] AC-3: All 7 explicitly-listed drift sites + the time.Until
      refresh predicate at sessions.go:187 (D6) route through the
      injected clock.
- [x] AC-4: 14 new tests across three new `clock_test.go` files
      (sessions 5 / apikeystore 5 / jwtmw 4). Each exercises a
      time-dependent edge via the injected clock — meets the
      orchestrator's "≥ 6 minimum (2 per package)" floor with room.
- [x] AC-6 (orchestrator brief): decisions log D1+ recorded
      (this file).
- [x] AC-7: `pre-commit run --all-files` clean — see PR CI.

## Anti-criteria (P0)

- [x] P0-371-1: Does NOT change runtime behavior under the default
      clock. Defaults are identical wall-clock semantics; the only delta
      is the UTC-zone normalization in jwtmw's nil-fallback (was
      host-local, now UTC — observable only via `time.Location()`, not
      via Unix-seconds claim comparisons).
- [x] P0-371-2: Does NOT touch `fsstore.go:236`. The directory-naming
      use site is unchanged.
- [x] P0-371-3: Does NOT change TTL durations, rotation graces, or
      policy constants. `DefaultTTL`, `RefreshThreshold`, the 7-day
      rotation-grace fallback are unchanged.
- [x] P0-371-4: Does NOT add a new dependency. All test imports come
      from the existing transitive set.

## Files touched

- `internal/auth/sessions/sessions.go` — clock field + WithClock +
  swept 3 drift sites + Read-path time.Until → s.clock() Sub
- `internal/auth/sessions/clock_test.go` — new unit tests (5)
- `internal/auth/apikeystore/apikeystore.go` — clock field + WithClock
  - swept 3 drift sites
- `internal/auth/apikeystore/clock_test.go` — new unit tests (5)
- `internal/auth/jwtmw/middleware.go` — nil-fallback UTC normalization
  - doc-comment alignment
- `internal/auth/jwtmw/clock_test.go` — new unit tests (4)
- `CHANGELOG.md` — Unreleased / Changed bullet
- `docs/audit-log/371-auth-clock-injection-substrate-decisions.md` —
  this file

## Out of scope (recorded for future)

- `internal/auth/oauthcode/oauthcode.go:118` default clock is
  `time.Now` (host-local) instead of the canonical
  `func() time.Time { return time.Now().UTC() }`. Minor drift,
  no observable bug today. Candidate for a focused follow-up.
- `internal/auth/keystore/fsstore/fsstore.go:236` raw `time.Now()`
  remains as-is per slice doc's explicit exclusion (directory
  naming, not security-critical TTL math).
