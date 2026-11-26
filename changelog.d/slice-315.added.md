- **test(coverage):** slice 315 — auth-substrate-v2 small-package
  coverage lift (`internal/auth/oauthclient` + `internal/auth/oauthcode`
  - `internal/auth/revocation` + `internal/auth/userprefs`). Round-3
    spillover from slice 312's coverage audit
    (`docs/coverage-audit-2026-05-round-3.md`); the audit identified
    four auth-substrate packages (each < 100 statements; all slice
    187 / 188 / 189 / 190 / 108 origins) below 70% merged coverage
    because none was enrolled in the CI integration job. Lifting
    strategy mirrors slices 290 / 297 / 310's "enrolment is the
    load-bearing move" playbook:
  1. Enrol `./internal/auth/oauthclient/...`,
     `./internal/auth/oauthcode/...`, `./internal/auth/revocation/...`,
     and `./internal/auth/userprefs/...` in the
     `Go · integration (Postgres RLS)` job's `-coverpkg` list
     (`.github/workflows/ci.yml`).
  2. New `internal/auth/oauthclient/integration_test.go` covering
     `Issue` (happy + duplicate-name + empty-name), `Verify` (happy +
     unknown-client-collapse + wrong-secret-collapse + disabled-
     collapse + empty-arg-collapse + cross-client-secret), `Lookup`
     (happy + unknown + disabled + empty).
  3. New `internal/auth/oauthcode/integration_test.go` covering
     `Insert` (happy + empty-code + non-S256 + RolesJSON / tenants
     default-fill), `ConsumeOnce` (happy + empty / unknown → ErrNotFound
     - twice → ErrAlreadyConsumed + expired → ErrExpired via WithClock),
       `SweepExpired` (deletes old), `RegisterRedirectURI` (happy +
       duplicate + empty-arg), `IsRedirectURIRegistered` (true / false /
       empty), `LookupRedirectURI` (match / miss / empty-arg).
  4. New `internal/auth/oauthcode/oauthcode_test.go` pure-Go unit tests
     covering `New` / `WithClock` / `PKCEMethodS256` / `DefaultTTL`
     constants.
  5. New `internal/auth/userprefs/integration_test.go` covering `Get`
     (empty → defaults; populated → merged), `Upsert` (full matrix +
     partial-merge + overwrite + unknown-event + unknown-channel +
     pre-flight no-partial-write), RLS isolation (Tenant A's write
     invisible to Tenant B).
  6. No new tests for `revocation` — slice 190's existing 190-line
     `integration_test.go` covers the substantive paths; enrolment
     alone lifts it from 18.2% to 79.5% merged.

  Coverage lift (local measurement on this branch):
  - `internal/auth/oauthclient`: 5.7% → 86.8% (floor `84`)
  - `internal/auth/oauthcode`: 0.0% → 87.6% (floor `85`)
  - `internal/auth/revocation`: 18.2% → 79.5% (floor `77`)
  - `internal/auth/userprefs`: 29.6% → 85.2% (floor `83`)

  Floors are set at `max(0, floor(measured - 2pp))` per slice 069's
  monotonic-ratchet methodology. All 4 floors are NEW entries (no
  existing floor lowered, P0-315-2). Tests + floors ship in the same
  PR (P0-315-1). `_STATUS.md` row 315 transitions `ready` → `in-review`
  via a separate commit on the slice branch per the per-slice template
  (P0-315-3). Slice 314 (`internal/api/oauth`, 921 stmts) remains a
  separate standalone spillover (P0-315-4 honored). Constitutional
  invariants honored: invariant 6 (RLS isolation — the `userprefs`
  suite verifies Tenant A's write is invisible to Tenant B through the
  canonical four-policy pattern); the AI-assist boundary (every test
  body written with explicit reference to the slice's anti-criteria —
  P0-188-3 plaintext-secret leak prevention, P0-189-3 one-shot codes,
  P0-190-4 revocation idempotency). No vendor-prefixed tokens in
  fixtures per `CLAUDE.md`'s hard rule; decisions log at
  `docs/audit-log/315-coverage-auth-substrate-small-decisions.md`.
