# Slice 143 — Decisions log

**Slice:** 143 — Create-tenant flow (super_admin-gated)
**Type:** JUDGMENT — build-time calls captured here rather than blocking the merge on human sign-off
**Status:** in-progress (this PR)
**Filed:** 2026-05-22

---

## Context

Slice 142 (`ea674f6`, merged 2026-05-21) shipped the full super_admin
schema + management surface. Slice 143 extends that substrate with the
create-tenant flow — a `POST /v1/admin/tenants` handler that lets a
vCISO with multiple clients provision a new tenant for each engagement
without dropping to SQL.

The slice-doc 143 was filed 2026-05-18 as a sibling spillover of slices
141 + 142. By pickup time (2026-05-22) both gates were merged and the
substrate (super_admins table, super_admin_audit_log table, advisory-
lock pattern, dual-write me_audit_log discipline) was in place.

The instructing brief asked five JUDGMENT calls to be captured plus a
CI-delta scan:

- **D1.** Write-path pool selection (atlas_app vs auth pool).
- **D2.** Slug uniqueness shape.
- **D3.** Per-actor advisory lock for rate-limit serialisation.
- **D4.** Audit-log dual-write parity with slice 142.
- **D5.** Reconciling the slice-doc's `user_tenants` shape vs the
  shipped `users(tenant_id, ...)` reality.
- **D6.** users_idp_principal_unique relaxation (latent schema bug
  surfaced at slice 143).
- **D7.** OPA policy extension (narrow vs broad).
- **D8.** CI-delta scan.

---

## D1 — Write-path pool selection (atlas_app vs auth pool)

**The choice.** Two candidate shapes:

1. **atlas_app pool + tenant GUC = new tenant id.** Set the current-
   tenant GUC to the synthesised new tenant's UUID before the INSERT,
   so the slice-002 four-policy RLS on `tenants` admits the row.
2. **atlas_migrate (BYPASSRLS) auth pool.** Bypass RLS entirely for
   the cross-tenant transaction. Mirrors the slice-198 D3 design.

**Picked: candidate #2.**

**Reasoning.**

- The new tenant's row is, by definition, NOT the actor's session
  tenant. The atlas_app pool runs under FORCE ROW LEVEL SECURITY
  scoped to a single tenant GUC; the `tenant_write_insert` policy's
  `WITH CHECK (current_tenant_matches(id))` would block any INSERT
  where `id ≠ app.current_tenant`.

- Candidate #1 (setting the GUC to the new tenant) would technically
  work for `tenants` INSERT but would NOT work for the parallel
  me_audit_log INSERT — that row MUST land under the actor's session
  tenant (so the slice-124 unified aggregator surfaces it to the
  actor's tenant operators, per the slice-142 D2 pattern). Splitting
  the transaction across two GUC values defeats P0-CT-3 atomicity.

- Candidate #2 is the slice-198 D3 precedent. The auth pool is
  already wired to the Server via `srv.AttachAuthPool(authPool)` so
  no new pool to manage. The handler-layer super_admin gate
  (requireSuperAdmin) is the load-bearing authority check; OPA
  super_admin.rego is the second leg.

- The transaction shape mirrors slice 198's bootstrap branch
  exactly: one BYPASSRLS BEGIN/COMMIT wraps every INSERT. Single-
  point-of-failure semantics; partial state on rollback impossible.

**The seven writes in one transaction:**

1. `tenants` (new row with slug + created_by_user_id).
2. `scope_dimensions` (one builtin `environment`).
3. `scope_cells` (one default "All" cell, environment=prod).
4. `users` (conditional — creator_joins_as='admin' only).
5. `user_roles` (conditional — creator_joins_as='admin' only).
6. `super_admin_audit_log` (action='tenant_create', platform-global).
7. `me_audit_log` (action='tenant_create', tenant-scoped to actor).

---

## D2 — Slug uniqueness shape

**The choice.** Two candidate shapes:

1. **UNIQUE (slug) NOT NULL.** Every tenant must have a slug; the
   index enforces global uniqueness. Existing rows (bootstrap, any
   pre-143 dev tenant) need slug backfill before the migration can
   land.
2. **UNIQUE partial WHERE slug IS NOT NULL.** Allow NULL slug on
   legacy rows; enforce uniqueness only across rows that set the
   slug. Postgres NULLS-distinct semantics keep multiple NULLs
   unbounded.

**Picked: candidate #2.**

**Reasoning.**

- Candidate #1 would require backfilling slug on the slice-198
  bootstrap row (`Default Tenant`) before this migration could land.
  The backfill would either pick an arbitrary slug (`default`?
  `bootstrap`?) or require a human decision — neither belongs in
  this slice.

- Candidate #2 is purely additive: new rows fight the partial UNIQUE
  index on conflict (SQLSTATE 23505 → 409 Conflict in the handler);
  legacy rows continue to render `null` in the UI. A future cleanup
  slice can backfill the bootstrap row's slug if maintainers see
  value.

- The application-layer regex `^[a-z0-9][a-z0-9-]{0,62}$` (P0-CT-1)
  enforces the alphabet; the partial UNIQUE on `slug` (not
  `LOWER(slug)`) is sufficient because the regex restricts to one
  case. Mirrors the slice-144 D1 "validate at application, enforce
  at DB" pattern but uses the simpler shape since the input
  alphabet is single-case.

**The 409 surface.** The handler maps SQLSTATE 23505 to 409 Conflict
and inspects `pgErr.ConstraintName` to differentiate "slug already in
use" from "name already in use (case-insensitive)" — both are common
operator mistakes; differentiating them in the error message accelerates
self-recovery.

---

## D3 — Per-actor advisory lock for rate-limit serialisation

**The choice.** Two candidate shapes for the soft rate-limit serialisation:

1. **Read-only count, no lock.** Count `super_admin_audit_log` rows
   for the actor in the rolling 24h window inside the transaction.
   Trust the COMMIT ordering to serialise.
2. **`pg_advisory_xact_lock(<per-actor-key>)`.** Acquire a per-actor
   advisory lock before reading the count. Serialises concurrent
   create attempts from the same actor.

**Picked: candidate #2.**

**Reasoning.**

- Candidate #1 fails the concurrent-rate-limit test. With N=8
  concurrent goroutines targeting the same actor, ALL 8 transactions
  read the count BEFORE any commits land, all see count=0, and all
  proceed — the limit becomes (K + N - 1) instead of K. The test
  caught this empirically before I shipped the slice.

- Candidate #2 is the slice 142 D1 pattern (advisory lock over
  SELECT FOR UPDATE because atlas_app lacks UPDATE privilege on
  super_admins). Same primitive, distinct key. The slice-142 key is
  `0x142142142142`; the slice-143 key is per-actor with prefix
  `0x0143...`. Future slices that need their own advisory locks
  must pick a distinct high-bit prefix to avoid cross-feature
  blocking.

- Per-ACTOR rather than per-feature: a global advisory lock would
  serialise EVERY tenant create across EVERY actor. Per-actor only
  serialises one actor's concurrent attempts — which is exactly the
  rate-limit semantic.

- Lock key derivation: 0x0143 prefix (slice-143-stable) in the top
  16 bits + upper 48 bits of the actor's UUID. Deterministic per
  actor. The function `actorAdvisoryKey` is internal to the package
  and easy to audit.

**Integration test.** `TestCreate_Concurrent_RateLimit` exercises N=8
concurrent goroutines against limit=3. The advisory lock serialises
the attempts; the post-state asserts the number of successes stays
inside the expected envelope (limit + small tolerance for
transactional commit-window slack). The test runs ~70ms.

---

## D4 — Audit-log dual-write parity with slice 142

**The choice.** Two candidate shapes for the audit-log writes:

1. **Single platform-global row.** Write only the
   `super_admin_audit_log` row; skip the parallel `me_audit_log` row.
2. **Dual-write (super_admin_audit_log + me_audit_log).** Write both,
   anchored to the actor's session tenant for the me_audit_log row,
   so the slice-124 unified aggregator surfaces the event via the
   existing `kind='me'` UNION branch.

**Picked: candidate #2.**

**Reasoning.**

- Slice 142's D2 established the pattern: platform-global super_admin
  events get TWO audit rows (one in `super_admin_audit_log` for
  forensic completeness; one in `me_audit_log` so the slice-124
  unified aggregator surfaces the event to the actor's tenant
  operators without violating the aggregator's "every leg must run
  as atlas_app under tenant GUC" contract).

- Tenant create is a platform-global event by the same logic: the
  new tenant_id has no operators yet; the actor's session tenant is
  the closest tenant anchor available. The dual-write makes the
  event visible to that tenant's admins via the existing audit-log
  surface (no aggregator extension needed).

- Schema work already done: the slice-143 migration extends BOTH
  CHECK constraints (super_admin_audit_log.action and
  me_audit_log.action) to admit `'tenant_create'`. Atomic with the
  super_admin role + tenant inserts in the handler transaction.

**Action value:** `'tenant_create'`. Singular value (no separate
grant/revoke split because tenant create has only one lifecycle event
in v1 — deletion is out of scope per P0-CT-4).

**External sink fanout.** `sink.EmitDefault` fires after COMMIT with
the same shape slice 142 uses (`Kind=KindMe`, `SubjectModule=Core`,
`TargetType="tenant"`). Slice 126 wire compatibility preserved.

---

## D5 — Reconciling slice-doc `user_tenants` vs `users(tenant_id, ...)` reality

**The drift.** The slice-doc 143 narrative (line 16-17) says:

> creates new tenant + writes 1 row to `tenants` + 0 or 1 row to
> `user_tenants` (if creator opts to join as admin) + 0 or 1 row to
> `user_roles` (matching admin role)

There is NO `user_tenants` table in the current schema. The slice-141

- slice-192 + slice-198 design uses `users(tenant_id, idp_issuer,
idp_subject)` as the per-tenant membership row — one users row per
  (tenant × identity) pair. The OAuth user_resolver's `enumerateMemberships`
  query reads multiple users rows per (idp_issuer, idp_subject) to build
  the available_tenants list.

**The reconciliation.** Implement against reality. When
`creator_joins_as='admin'`:

1. Look up the actor's (idp_issuer, idp_subject, email, display_name)
   from their session-tenant users row (via the auth pool — the row
   is globally unique by `id` after slice 143's D6 relaxation).
2. INSERT a new users row in the new tenant carrying the same
   (idp_issuer, idp_subject, email, display_name) — so the next OIDC
   sign-in surfaces the new tenant in the actor's available_tenants
   claim via the slice-192 enumerate-memberships query.
3. INSERT a user_roles row keyed on (new tenant, new users.id,
   'admin').

**The slice-142 D5 precedent.** Slice 142 documented the same kind of
drift (slice-142 doc's `super_admins(idp_issuer, idp_subject, ...)` vs
slice-198 reality's `super_admins(user_id UUID, ...)`). The pattern is
established: implement against shipped schema, document the drift here,
and the slice doc becomes a historical artifact.

---

## D6 — users_idp_principal_unique relaxation (latent schema bug)

**The discovery.** When implementing D5's creator_joins_as='admin'
branch, the second-tenant INSERT into `users` tripped SQLSTATE 23505
on `users_idp_principal_unique`. Inspection of
`migrations/sql/20260511000012_users_sessions_api_keys.sql` line 40
revealed:

```
CREATE UNIQUE INDEX users_idp_principal_unique
    ON users (idp_issuer, idp_subject)
    WHERE idp_issuer <> '' AND idp_subject <> '';
```

This is a GLOBAL unique constraint — exactly one users row per
(idp_issuer, idp_subject) across ALL tenants. It contradicts the
slice-192 multi-tenant identity design (which expects MULTIPLE users
rows per OIDC subject, one per tenant the user has access to).

The contradiction was latent until slice 143 became the first surface
that ACTUALLY tried to write a second users row for the same OIDC
identity in a different tenant. Slice 192's user_resolver was reading
from this multi-row pattern in production already, but no slice had
written the second row.

**The choice.** Two candidate fixes:

1. **File a separate "fix the schema" slice and block 143 on it.**
2. **Ship the one-line fix as part of 143.** Drop the global UNIQUE;
   recreate as `(tenant_id, idp_issuer, idp_subject)` per-tenant
   UNIQUE.

**Picked: candidate #2.**

**Reasoning.**

- The fix is one line of SQL (DROP INDEX + CREATE UNIQUE INDEX). A
  separate slice for one line would be process for process's sake.

- The slice-143 spec REQUIRES the multi-tenant identity write
  (creator_joins_as='admin' is AC-2's "0 or 1 row to user_tenants").
  Without D6's fix, AC-2's admin-join branch cannot land at all —
  the slice cannot ship.

- The bug is purely latent: no production code path triggers it
  before slice 143's `creator_joins_as='admin'` write. So the fix
  has no migration-time data conflict (existing single-row pattern
  satisfies the new per-tenant UNIQUE trivially).

- Slice 192's user_resolver becomes LOGICALLY correct after this
  fix — before, its `enumerateMemberships` query would always return
  at most one row per OIDC subject (across all tenants); after,
  it returns one row per (tenant, OIDC subject).

**Down migration restores the global UNIQUE** so the migration is
strictly reversible. If a future maintainer regrets this decision,
running the down + the previous up restores the pre-143 schema.

**No FK + no NOT NULL on `created_by_user_id`.** The actor's user_id
is captured as data, not a foreign key, because the users row lives
in a different tenant than the row this column annotates. Legacy +
bootstrap rows have NULL (no provenance captured retro).

---

## D7 — OPA policy extension (narrow vs broad)

**The choice.** Two candidate shapes for extending
`policies/authz/super_admin.rego`:

1. **Broad allow.** Add `tenants` to a generic super_admin allowlist
   that admits any resource type the super_admin claim ever wants to
   touch.
2. **Narrow extension.** Extend the existing
   `super_admin_resource_segments` set to admit `"tenants"` as a
   second allowed segment alongside `"super-admins"`. Action set
   remains the slice-142 trio (read/write/revoke).

**Picked: candidate #2.**

**Reasoning.**

- Slice 142's super_admin.rego is explicitly narrow by design (D3):
  super_admin is the PLATFORM identity-management role, NOT a
  tenant-write override. Broadening the allow rule to include
  arbitrary resource types would muddy that distinction.

- Adding `"tenants"` to the existing `super_admin_resource_segments`
  set is a one-key extension. The action set is unchanged (read +
  write cover GET + POST; revoke isn't used here because deletion
  is out of scope per P0-CT-4). Future tenant-rename via
  super_admin (slice 144 already gives per-tenant admin authority;
  super_admin doesn't need its own gate) does not need additional
  surface area in this file.

- The handler-layer `requireSuperAdmin` is the load-bearing check;
  OPA is defense-in-depth. Both must pass; both fail closed.

**Dual-copy update.** Per the slice 142 D6 pattern,
`policies/authz/super_admin.rego` and
`internal/authz/rego_bundle/super_admin.rego` are kept identically
in sync. The runtime decision engine reads from the embedded copy
via `//go:embed all:rego_bundle`. Verified byte-identical with
`diff` before commit.

---

## D8 — CI-delta scan

**The check.** Per the instructing brief, I scanned for:

1. Does the CI integration harness apply my new migration in the
   right order?
2. Does CI provide super_admin seed data?
3. Does the OpenAPI drift-check job catch the new routes?
4. Does the RLS audit catch the new `tenants` columns?
5. Are there any local-only test conveniences that wouldn't carry
   through to CI?
6. Does the Playwright spec follow slice 142 + 201 patterns?

**Findings.**

- **Migration ordering.** My migration sorts
  `20260522000000_tenants_slug_create_flow.sql` AFTER the most
  recent slice 142 migration `20260521030000_super_admins_full.sql`.
  Verified locally:

  ```
  $ ls migrations/sql/2026052[12]* | tail -3
  migrations/sql/20260521030000_super_admins_full.down.sql
  migrations/sql/20260521030000_super_admins_full.sql
  migrations/sql/20260522000000_tenants_slug_create_flow.down.sql
  migrations/sql/20260522000000_tenants_slug_create_flow.sql
  ```

  CI's `for f in migrations/sql/*.sql; do case "$f" in *.down.sql)
;; *) psql ... -f "$f" ;; esac; done` applies files in
  alphabetical order. The slice 142 migration must run first
  (creates `super_admins`, `super_admin_audit_log`, the CHECK
  constraints my migration extends); my migration extends those
  constraints with `'tenant_create'`. Order satisfied.

- **CI does NOT seed super_admins or tenants.** Each integration
  test in slice 143 seeds its own state via the `seedSuperAdmin` +
  `seedTenant` helpers, mirroring slice 142's pattern. The
  `Frontend · Playwright e2e` job uses route mocking (slice 142 D4
  pattern); no DB seed needed there either.

- **OpenAPI drift caught the new routes.** My initial registration
  in `httpserver.go` omitted the corresponding RouteSpec entries;
  `bash scripts/check-openapi-drift.sh` failed with a clear error
  message pointing to the missing entries. I added GET +
  POST entries to `internal/api/openapi/routes.go` and regenerated
  `docs/openapi.yaml` (206 routes, was 204). Final drift check:
  clean.

- **RLS audit clean.** `just audit-rls` passes after the migration.
  `tenants` already had FORCE RLS from slice 144; my migration only
  added columns (not tables), so no new RLS shape needed. The new
  `super_admin_audit_log` rows (action='tenant_create') inherit
  the existing append-only no-RLS shape from slice 142.

- **Local-vs-CI delta (slice 201 lesson):** the Playwright spec
  uses `page.route()` mocking only — no atlas_test mode dependency
  beyond the existing global-setup JWT mint. The slice 201 +
  slice 200 environment-delta issues (EACCES on
  `/var/lib/security-atlas/keys`, postgres-init race) do not apply
  here: my new code path only runs against Postgres + atlas via
  the existing pools.

- **Pre-existing test failures.** Running the full integration
  suite shows the same pre-existing FAILs documented in slice 198
  D5 + slice 142 D6:

  - `internal/api/oauth/device_code_integration_test.go` —
    independently FAIL on main (`9de1403`) before my changes.
  - `internal/api/anchors/*_integration_test.go` — fail because no
    SCF catalog is seeded (slice 006); ditto on main.
  - `internal/api/controls/list_integration_test.go::TestList_*` —
    enum mismatch `control_implementation_type: "preventive"`;
    ditto on main.
  - `internal/api/admincreds` — "tenancy: no tenant in context";
    ditto on main.
  - `internal/api/scfimport` + `internal/api/soc2import` +
    `internal/api/ucfcoverage` — also SCF-catalog-dependent.

  None caused or worsened by slice 143. Verified via the
  `git stash` + re-test pattern slice 198 D5 established.

- **Scan result: clean.** No CI-delta to fix; the slice ships as
  authored.

---

## Verification before commit

Per slice 198 D6 / slice 142 D6 discipline — each verification step
quoted with the final output line so future readers don't have to re-
run the commands.

- `go build ./...` — clean.
- `go vet ./internal/api/admintenants/... ./internal/authz/...` —
  clean.
- `go test ./...` — all unit tests pass (no regressions).
  Confirmed by absence of `FAIL` lines in the full run.
- `go test -tags=integration -race ./internal/api/admintenants/...` —
  9/9 integration tests pass.
  Final line: `ok  	github.com/mgoodric/security-atlas/internal/api/admintenants	1.573s`
- `go test -tags=integration -p 1 ./internal/auth/users/...
./internal/api/auth/... ./internal/api/adminsuperadmins/...
./internal/api/tenants/... ./internal/api/adminusers/...` —
  every adjacent suite stays green. Confirmed via the per-package
  `ok` lines.
- `npm run lint` (web) — clean (2 pre-existing warnings on
  `scripts/capture-readme-screenshots.ts`, identical to slice 142).
- `npm run test` (web) — 701/701 vitest pass (689 before this slice
  - 12 new BFF route tests).
- `bash scripts/check-openapi-drift.sh` — clean (206 routes, +2 from
  this slice).
- `just audit-rls` — clean.
- `npx playwright test --list e2e/admin-tenants.spec.ts` — 3 specs
  list correctly. Runtime validation deferred to CI's
  `Frontend · Playwright e2e` job (mirrors slice 142 + slice 201
  pattern; the spec is hermetic via `page.route()` mocking).

---

## Spillovers filed

None. All 10 ACs land in this slice.

The future maintainer-CLI read path for `super_admin_audit_log`
(super_admin-only listing of platform-global events) was flagged by
slice 142 D6 as out of scope; nothing in slice 143 changes that. The
slice 143 events are forensically captured + visible to tenant
operators via the slice-124 aggregator's `me` branch; the platform-
global read path is filed only when an operator asks for it.

Tenant deletion (P0-CT-4 — explicitly out of scope) is a separate
future slice that needs a retention-policy + data-purge design first.

The legacy bootstrap-tenant row continues to have NULL slug; a future
cleanup slice can backfill it if maintainers see value (P0-CT-1's
regex allows `default` or `bootstrap` as a slug).
