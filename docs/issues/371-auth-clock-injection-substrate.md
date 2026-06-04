# 371 — Auth-substrate clock injection (sessions + apikeystore + jwtmw)

**Cluster:** Auth
**Estimate:** 1d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 328's comprehensive code-review audit (`docs/audits/328-code-review-comprehensive-report.md` finding **M-2**, severity **Medium**) surfaced convention drift in the auth substrate: three packages (`internal/auth/sessions`, `internal/auth/apikeystore`, `internal/auth/jwtmw`) use raw `time.Now()` for TTL calculations and expiry checks instead of injecting a clock function, which is the established pattern next door in `internal/board/*`, `internal/evidence/*`, `internal/drift`, `internal/api/oauth/token.go`, `internal/api/admintenants/*`.

### Established positive baseline

```go
// internal/board/generator.go:61
clock: func() time.Time { return time.Now().UTC() },

// internal/evidence/ingest/ingest.go:195
clock: func() time.Time { return time.Now().UTC() },

// internal/drift/drift.go:67
now: func() time.Time { return time.Now().UTC() },

// internal/api/admintenants/handler.go:185
func (h *Handler) WithClock(fn func() time.Time) *Handler { h.clock = fn; return h }
```

### Drift sites (target of this slice)

```go
// internal/auth/sessions/sessions.go:137
expiresAt := time.Now().UTC().Add(s.ttl)

// internal/auth/sessions/sessions.go:183
if !row.ExpiresAt.Valid || time.Now().UTC().After(row.ExpiresAt.Time) {

// internal/auth/sessions/sessions.go:188
newExpiry := time.Now().UTC().Add(s.ttl)

// internal/auth/apikeystore/apikeystore.go:165
retiresAt := time.Now().UTC().Add(s.rotationGrace)

// internal/auth/apikeystore/apikeystore.go:251
now := time.Now().UTC()

// internal/auth/apikeystore/apikeystore.go:293
expiresAt = pgtype.Timestamptz{Time: time.Now().UTC().Add(in.TTL), Valid: true}

// internal/auth/jwtmw/middleware.go:298
return time.Now()
```

The `internal/auth/keystore/fsstore/fsstore.go:236` raw `time.Now()` is acceptable in scope — it's used for directory naming, not for security-critical TTL math. Leave as-is.

### What ships

1. **Add `clock func() time.Time` field** to:

   - `internal/auth/sessions/Store`
   - `internal/auth/apikeystore/Store`
   - `internal/auth/jwtmw/Middleware` (already has `nowFn` — unify with the rest)

2. **Default each to `func() time.Time { return time.Now().UTC() }`** in `NewStore` / construction.

3. **Add `WithClock(fn)` method** to each Store (mirrors `internal/api/admintenants/handler.go:185`):

```go
// WithClock overrides the clock. Test-only.
func (s *Store) WithClock(fn func() time.Time) *Store {
    s.clock = fn
    return s
}
```

4. **Replace the ~8 raw `time.Now()` call sites** with `s.clock()`.

5. **Add one or more tests per store** that exercise a time-dependent edge via the injected clock — at minimum:
   - `sessions`: session-expires-at-exact-T test.
   - `apikeystore`: rotation-grace boundary test.
   - `jwtmw`: not-before / expires-at boundary tests.

The new tests must RED-first per Article III — write the test, observe it fail (because the raw `time.Now()` doesn't allow the precise expiry-boundary assertion), then add the clock injection and observe it pass.

### JUDGMENT calls

The engineer makes the following design calls and records them in `docs/audit-log/371-auth-clock-injection-substrate-decisions.md`:

- **Field name.** `clock` vs `now` — the codebase has both. Recommend `clock` to match the larger pool (board, evidence, admintenants).
- **Method name.** `WithClock` vs `SetClock` vs constructor variant `NewStoreWithClock`. Recommend `WithClock` — matches admintenants precedent + the slice-198 `AttachAuthPool` builder style.
- **Per-package or cross-cutting refactor?** Land all three packages in one PR (this slice), OR split per package? Recommend one PR — change shape is identical; review fatigue is bounded.
- **Test invariant lift?** Add per-package floor in `cmd/scripts/coverage-thresholds.json` since the new tests cover previously hard-to-reach edges? Recommend yes — write the tests AND lift the floor in the SAME PR per the CLAUDE.md testing discipline.

### Why this matters

1. **Test brittleness.** Sessions/expiry-at-exact-T tests today use real wall-clock + tolerance windows — flaky on CI under load. Clock injection makes them deterministic.
2. **Convention split.** Two patterns in the codebase invites continued drift. Convergence reduces "which pattern do I follow?" cognitive load.
3. **Coverage of expiry edges.** Today's tests cannot easily assert "session is invalid 1 nanosecond after ExpiresAt" — clock injection makes it trivial.
4. **Future security testing.** Slice 327's H-1 (OIDC nonce) added an integration test; future expiry-related security findings will likely want similar time-pinned tests.

### Why now

M-2 from the slice 328 audit. Independent of platform-side work. Small, mechanical, ~1d. Compose naturally with any auth-substrate work the maintainer schedules.

**Trigger:** filed 2026-05-28 from slice 328 audit.

## Threat model

Test infrastructure addition only. STRIDE pass:

- **S (Spoofing):** N/A — clock injection is a test-time-only hook.
- **T (Tampering):** N/A.
- **R (Repudiation):** N/A.
- **I (Information disclosure):** N/A.
- **D (Denial of service):** N/A.
- **E (Elevation of privilege):** **CONSIDERED — RULED OUT.** A `WithClock` method could in principle be misused at runtime to set a clock that makes all sessions appear valid forever. Mitigation: the method is documented test-only; production wiring code (`cmd/atlas/main.go`) does not call `WithClock`; the default constructor sets a real-time clock. The same risk exists today in board/evidence/drift packages — pattern is accepted across the codebase.

## Acceptance criteria

- [ ] **AC-1.** `internal/auth/sessions/Store`, `internal/auth/apikeystore/Store`, `internal/auth/jwtmw/Middleware` each carry a `clock func() time.Time` field defaulting to `func() time.Time { return time.Now().UTC() }`.
- [ ] **AC-2.** Each exposes a `WithClock(fn)` method (Store-style) or a `Clock` config field (Middleware-style).
- [ ] **AC-3.** The 8 raw `time.Now()` sites under those three packages migrate to `s.clock()`.
- [ ] **AC-4.** At least 3 new tests (one per package) exercise a time-dependent edge via the injected clock.
- [ ] **AC-5.** Per-package coverage floors in `cmd/scripts/coverage-thresholds.json` lift to reflect the new test coverage; the lift is in the SAME PR as the test additions (per CLAUDE.md testing discipline).
- [ ] **AC-6.** No production-wiring changes — `cmd/atlas/main.go` does not call `WithClock`; the default clock applies.
- [ ] **AC-7.** Decisions log records field-name, method-name, and bundle-vs-split choices.
- [ ] **AC-8.** `pre-commit run --all-files` passes; CI green; coverage gate passes.

## Constitutional invariants honored

- **Article III (Test-First Imperative).** New tests RED-first, then code change.
- **Article VII (Simplicity Gate).** Convergence on the established clock-injection pattern.
- **Convention discipline.** Resolves a documented convention drift across 3 packages.

## Canvas references

- Slice 328 audit report `docs/audits/328-code-review-comprehensive-report.md` finding M-2
- `internal/board/generator.go:61` (positive baseline)
- `internal/evidence/ingest/ingest.go:195` (positive baseline)
- `internal/api/admintenants/handler.go:185` (`WithClock` method precedent)

## Dependencies

- **#069** (testing discipline) — `merged`. Coverage gate + per-package floors.
- **#347** (vitest coverage ratchet) — `merged`. The lift-the-floor-in-the-same-PR discipline.

## Anti-criteria (P0 — block merge)

- **P0-371-1.** Does NOT change runtime behavior — clock injection adds a test-time hook; production wiring is unchanged.
- **P0-371-2.** Does NOT add a CLI surface for setting the clock — `WithClock` is test-time only.
- **P0-371-3.** Does NOT skip the test-floor lift — the new tests AND the threshold bump land in the SAME PR (CLAUDE.md testing discipline).
- **P0-371-4.** Does NOT touch `internal/auth/keystore/fsstore/fsstore.go:236` — that `time.Now().UTC()` is for directory naming, not security-critical TTL math; out of scope.
- **P0-371-5.** Does NOT auto-merge.

## Skill mix

- `tdd` — RED-first per package
- `simplify` — pre-PR quality pass on the migration

## Notes for the implementing agent

The three packages are independent; the change shape is identical:

1. Add `clock func() time.Time` to `Store` / `Middleware`.
2. Initialize in constructor: `s.clock = func() time.Time { return time.Now().UTC() }`.
3. Add `WithClock(fn) *Store` method (test-only doc).
4. Replace raw `time.Now()` sites with `s.clock()`.
5. Add RED-first test per package.
6. Lift per-package coverage floor in `cmd/scripts/coverage-thresholds.json` to match the new coverage.

Edge-case test suggestions:

- **sessions**: a session created at T=0 with TTL=1h should be:
  - VALID at clock returning T=3599s
  - INVALID at clock returning T=3600s (boundary)
  - INVALID at clock returning T=3601s
- **apikeystore**: a rotated key with rotation_grace=7d should:
  - return ErrUnknownKey at T+7d+1ns (one nanosecond past grace)
  - return the credential at T+7d (exactly grace)
- **jwtmw**: a JWT with `nbf=10, exp=100` should:
  - return 401 at clock T=9 (before nbf)
  - return 200 at clock T=10 (exactly nbf)
  - return 200 at clock T=99 (one second before exp)
  - return 401 at clock T=100 (exactly exp)

These tests would be flaky-prone under wall-clock today; with clock injection they become deterministic.

For coverage-floor lift: run `just coverage-go` (or equivalent) before and after the test additions; lift each affected package's floor to the new actual coverage minus a small grace (~0.5%) per the slice-069 floor discipline.
