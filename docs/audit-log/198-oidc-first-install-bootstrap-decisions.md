# Slice 198 — Decisions log

**Slice:** 198 — OIDC first-install bootstrap (closes slice 192 AC-11/AC-12)
**Type:** JUDGMENT — build-time calls captured here rather than blocking the merge on human sign-off
**Status:** in-progress (this PR)
**Filed:** 2026-05-21

---

## Context

Slice 192 (multi-tenant switch + frontend switcher) shipped at `b0b5280` with
AC-11/AC-12 (the bootstrap branch) explicitly deferred to a follow-on slice
per the 191 D6 partial-cutover-with-spillover pattern. The slice 192
user_resolver carries an explicit hand-off comment:

> `super_admin = false (no super_admins table at v2; spillover slice 198
ships the OIDC-first-install bootstrap path).`

Slice 198 lands the deferred pieces:

1. The `super_admins` storage primitive.
2. The bootstrap branch that writes to it atomically alongside tenants /
   users / user_roles / me_audit_log when `count(*) FROM tenants == 0`.
3. The DBUserResolver lookup that consults the new table.

The instructing brief asked four JUDGMENT calls to be captured:

- **D1.** Race-handling shape.
- **D2.** super_admin storage shape.
- **D3.** Atomicity transaction shape.
- **D4.** Any CI-delta discovered.

Plus one bonus call surfaced during implementation:

- **D5.** Whether to also wire the auth pool into `httpserver.go`'s
  `meapi`-side `users.Store` (the `/v1/me` handlers' instance).

---

## D1 — Race-handling shape

**The choice.** Two candidate shapes were on the table:

1. **LOWER(name) UNIQUE collision retry** — rely on slice 144's existing
   `idx_tenants_bootstrap_singleton` partial UNIQUE index. The bootstrap
   branch wraps the writes in one transaction; concurrent first-installers
   race; the loser trips SQLSTATE 23505 on the tenants INSERT, rolls back,
   re-enters the loop, sees `count(*) > 0` on the second pass, and returns
   `Bootstrapped: false`.
2. **Advisory lock + count recheck** — Postgres `pg_advisory_xact_lock` on a
   well-known key; the first writer holds the lock through transaction
   commit; the second writer blocks on the lock and then sees the inserted
   row on its count check.

**Picked: candidate #1.**

**Reasoning.**

- Slice 144's migration explicitly provisioned the primitive for this slice.
  The migration header (line 44-48) is unambiguous:

  > `is_bootstrap_tenant` carried forward from slice 141's intended schema.
  > Partial unique index on the `WHERE is_bootstrap_tenant = true` predicate
  > serializes first-install races (slice 141 P0-ELEVATE-2). v1 ships the
  > column inert — the slice-141 OIDC bootstrap branch landed via the OAuth
  > substrate (slice 192) and does not write to this table; the column is
  > here so the slice that does (**future slice 198 OIDC-first-install
  > bootstrap**) can switch it on without a migration round-trip.

  Picking candidate #2 would orphan that primitive.

- Candidate #2 adds a second concurrency-control mechanism the maintainer
  has to reason about (an advisory lock with no schema footprint, easy to
  forget when reading the bootstrap path). Candidate #1 is purely
  declarative — the schema enforces the invariant.

- The retry loop in candidate #1 is bounded to ONE retry (`attempt < 2`) —
  past one retry the race has resolved deterministically (either we won OR
  someone else did + we see count > 0 on the second pass).

**The losing path.** When the partial UNIQUE index trips, the transaction
rolls back automatically (the INSERT raises 23505 before any other writes
land). The retry loop re-enters; the second `count(*)` sees the winner's
row; returns `Bootstrapped: false`; caller falls through to UpsertOIDC.

**Code reference.** `internal/auth/users/users.go::bootstrapAttempt` lines
~165-210. The 23505 sentinel is detected via `pgconn.PgError.Code ==
"23505"`.

---

## D2 — super_admin storage shape

**The choice.** Three candidate shapes:

1. **Dedicated `super_admins` table** — single column `user_id UUID PRIMARY
KEY` + provenance/timestamp metadata. No tenant_id; platform-global by
   design. Not under RLS.
2. **Boolean column on `users`** — `ALTER TABLE users ADD COLUMN super_admin
BOOLEAN NOT NULL DEFAULT false`.
3. **Role `'super_admin'` in existing `user_roles` table** — extend the
   slice 035 enum CHECK to include `'super_admin'`.

**Picked: candidate #1.**

**Reasoning.**

- **Tenant scoping mismatch.** `users` and `user_roles` are tenant-scoped
  tables (under FORCE RLS). super_admin is platform-global by definition
  (the JWT claim's name literally encodes the scope). Storing the flag on a
  tenant-scoped row creates an awkward N-row representation: one OIDC
  identity → N tenants → N `users` rows → N candidate "super_admin = true"
  slots. Either we replicate the flag on every row (a synchronization
  problem) or we pick one row arbitrarily (an authority problem).

- **Enum semantic muddling.** The slice 035 `user_roles.role` enum has a
  clear "per-tenant role" semantic: `'admin'`, `'grc_engineer'`,
  `'control_owner'`, `'auditor'`, `'viewer'`. Adding `'super_admin'` to the
  enum would muddle the meaning — `'super_admin'` is not a per-tenant role,
  it's a platform-global escalation.

- **Provenance.** Candidate #1's table carries `granted_via TEXT NOT NULL`
  with a CHECK constraint admitting `'bootstrap_first_install'` at v2.
  Future maintainer-CLI grants extend the CHECK in their own slice's
  migration; the bootstrap-vs-cli-grant distinction is auditable inline on
  the table. Candidates #2 + #3 would need a separate provenance column or
  a parallel audit-log lookup.

- **Future-proofing without YAGNI.** The dedicated table is the simplest
  shape that admits future maintainer-CLI grants (just INSERT a row with a
  different `granted_via` value). The other shapes would need schema
  changes the moment a non-bootstrap grant lands.

**Schema details.** No tenant_id column (P0-198-5). No RLS (platform-global
by definition). Grants: SELECT to `atlas_app` (read-side); SELECT + INSERT
to `atlas_migrate` (the bootstrap branch writes via the BYPASSRLS auth
pool); no UPDATE/DELETE grants (functionally append-only).

---

## D3 — Atomicity transaction shape

**The choice.** Two candidate shapes:

1. **Single BYPASSRLS transaction** — `atlas_migrate` pool wraps all five
   INSERTs (tenants + users + user_roles + super_admins + me_audit_log) in
   one BEGIN/COMMIT. RLS doesn't apply since atlas_migrate bypasses it.
2. **Multi-step with compensating writes** — separate transactions for each
   table; on partial failure, run cleanup logic to undo previous steps.

**Picked: candidate #1.**

**Reasoning.**

- Candidate #2 is a distributed-systems pattern for environments where
  multi-statement atomicity isn't available (e.g., writes across multiple
  storage backends). Postgres gives us multi-statement atomicity for free
  within a single connection.

- The transaction is short — five INSERTs touching five distinct tables, no
  long-running computation, no external I/O. Commit happens in milliseconds.
  Lock contention is minimal because the bootstrap path runs exactly once
  per install lifetime.

- Compensating writes have a maintenance liability: every future schema
  change touching one of the five tables would need a parallel update to
  the compensating logic. The single-transaction shape is monotonically
  simpler.

- The atlas_migrate pool is already wired in `cmd/atlas/main.go` via
  `srv.AttachAuthPool(authPool)` — slice 198 just plumbs it into
  `users.NewStoreWithAuthPool(pool, authPool)`. No new pool to manage.

**Why not run via the RLS-bound atlas_app pool?** The bootstrap branch
runs when zero tenants exist — there's no tenant_id to set as the
`app.current_tenant` GUC, so RLS policies that key on `current_tenant_matches`
would block all writes. The atlas_migrate pool bypasses RLS by design (the
same pool is already used by `apikeystore` for cross-tenant auth lookups,
slice 034 D2). Reusing it here matches the established pattern for
platform-global writes during identity bootstrap.

---

## D4 — CI-delta discovered

**The check.** Per the slice doc Notes section, I scanned for:

1. Does the CI integration harness seed any tenants? (If yes, my bootstrap
   integration test's "empty tenants on entry" precondition would fail.)
2. Does the CI integration harness apply my new migration in the right
   order?
3. Are there any local-only test conveniences that wouldn't carry through
   to CI?

**Findings.**

- **CI does NOT seed tenants.** `.github/workflows/ci.yml` lines 280-289
  apply migrations from `migrations/sql/` (skipping `.down.sql`) via psql,
  plus `migrations/bootstrap/01-roles.sql` for role setup. No tenant
  seeding. The `fixtures/e2e/*.sql` files are frontend Playwright fixtures
  applied LATER, only for the `Frontend · Playwright e2e` job, not the `Go
· integration` job. The `Go · integration` job starts with an empty
  `tenants` table. ✓

- **Migration ordering.** The CI migration loop is `for f in migrations/sql/*.sql`
  with a shell wildcard glob — alphabetical order. My migration
  `20260521020000_super_admins.sql` sorts AFTER `20260521010000_tenants_rename.sql`
  (the partial UNIQUE index that my migration depends on). ✓

- **Race-test reliability.** `TestBootstrap_ConcurrentFirstInstallers_SerializesViaUniqueIndex`
  uses N=5 goroutines with a channel barrier — runs deterministically green
  on my local Postgres (10 retries during dev). CI has noisier scheduling
  but the partial UNIQUE index guarantees serialization regardless of
  scheduling jitter. ✓

- **Cleanup discipline.** The integration tests run `go test -p 1` per the
  ci.yml runtime, but each sub-test calls `cleanupBootstrap(t)` at the top
  AND registers a `t.Cleanup` callback. Sub-tests are isolated; running them
  in any order produces the same result. ✓

**No CI-delta to fix.** All local PASS conditions reproduce on the CI
harness shape.

---

## D5 — bonus: do we also wire the auth pool into httpserver.go's `meapi`-side userStore?

**The choice.** The codebase has two `users.Store` instances at startup:

- `cmd/atlas/main.go::userStore` — passed to `authapi.New(...)`; backs the
  OIDC callback handler. This IS the bootstrap branch's caller.
- `internal/api/httpserver.go::usersStore` — passed to the `meapi` profile +
  preferences handlers (slice 108 `/v1/me`, `/v1/me/preferences`,
  `/v1/me/sessions`). This Store does NOT call BootstrapFirstInstallOrUpsert
  — it's read-only via `GetByID` + write via `UpdateProfile`.

Question: do we wire the auth pool into BOTH instances for consistency?

**Picked: only the OIDC-callback-side instance.**

**Reasoning.**

- The auth pool's only consumer in `users.Store` is
  `BootstrapFirstInstallOrUpsert`. The meapi-side instance never calls
  that method (the `/v1/me` handlers always have a valid tenant context;
  they're post-login).
- Wiring the auth pool into the meapi-side instance would suggest the meapi
  handlers might use it — a false signal. Better to keep the auth pool
  scoped to exactly where it's needed.
- If a future slice extends `users.Store` with another auth-pool-requiring
  method, we revisit; v2 ships with the smaller surface.

**Tradeoff acknowledged.** A future maintainer who reads main.go's userStore
construction without context might wonder why one Store has the auth pool
and the other doesn't. The httpserver.go construction site now has a comment
clarifying the choice; see the slice 198 modification to
`cmd/atlas/main.go` line ~588.

---

## Closing notes

- All 5 integration tests PASS locally against a clean Postgres 16 with the
  full migrations applied via psql.
- No regressions in adjacent test suites (`internal/auth/...`,
  `internal/api/auth/...`, `internal/api/oauth/...`) — verified before
  push.
- Two pre-existing FAILs in
  `internal/api/oauth/device_code_integration_test.go` are confirmed
  unrelated to slice 198 (they fail on `1affd09` main without my changes;
  verified by stash + re-test).
- This slice closes slice 192 AC-11/AC-12. The `_STATUS.md` row for 198
  flips from `ready` → `in-progress` → `in-review` → `merged` via the
  standard batch-N rhythm.
