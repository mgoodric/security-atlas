# Slice 142 — Decisions log

**Slice:** 142 — super_admin role: full schema + management surface (slice 198 follow-on)
**Type:** JUDGMENT — build-time calls captured here rather than blocking the merge on human sign-off
**Status:** in-progress (this PR)
**Filed:** 2026-05-21

---

## Context

Slice 198 (`12a6219`, merged 2026-05-21) shipped the `super_admins` storage primitive
with one provenance value (`'bootstrap_first_install'`) and INSERT/SELECT grants
sufficient for first-install only. Slice 142 promotes that stub to a management-grade
schema and ships the runtime grant + demote surface.

The slice doc filed 2026-05-18 (`docs/issues/142-super-admin-role-surface.md`)
described the table shape as `super_admins(idp_issuer, idp_subject, granted_at,
granted_by)`. Reality on `main` after slice 198 is
`super_admins(user_id UUID PK, granted_at, granted_via)`. This decisions log
captures the reconciliation calls between the spec-as-filed and reality-as-shipped.

The instructing brief asked four JUDGMENT calls to be captured:

- **D1.** Last-super_admin safety rail implementation.
- **D2.** Audit log RLS shape (slice-036 append-only vs platform-global).
- **D3.** OPA rule structure (narrow vs broad super_admin authority).
- **D4.** Playwright fixture seeding for super_admin scenarios.

Plus one operational call surfaced during implementation:

- **D5.** Reconciling the slice-doc schema shape vs the slice-198 reality.

And the cumulative-batch CI-delta scan:

- **D6.** CI-delta scan results.

---

## D1 — Last-super_admin safety rail implementation

**The choice.** Three candidate shapes for the P0-SA-1 invariant ("no path may
DELETE the last super_admin"):

1. **`SELECT ... FOR UPDATE` on super_admins rows.** Acquire row-level locks
   on every super_admins row before counting + deleting. Concurrent demotes
   block on the lock; the loser sees count==1 post-DELETE and 409s.
2. **`pg_advisory_xact_lock(<slice-key>)` advisory transaction lock.**
   Acquire a slice-142-stable BIGINT lock before reading + writing. The lock
   is automatically released at transaction commit. Same serialisation
   guarantee; no per-row UPDATE privilege required.
3. **Schema-level CHECK constraint.** Enforce "at least one row must exist"
   as a constraint, raising on DELETE that would violate it.

**Picked: candidate #2.**

**Reasoning.**

- Candidate #1 was the slice-doc's stated approach. I attempted it first.
  Postgres rejects `SELECT count(*) FROM super_admins FOR UPDATE` with
  SQLSTATE 0A000 ("FOR UPDATE is not allowed with aggregate functions"), so
  the first refinement was to lock raw rows and count in Go. That refinement
  fails with `permission denied for table super_admins` because Postgres
  strictly requires the **UPDATE** privilege for any FOR UPDATE / FOR NO KEY
  UPDATE / FOR SHARE variant — even though atlas_app has DELETE. Granting
  UPDATE to atlas_app would be misleading (the handler never UPDATEs the
  table; the column set is immutable post-grant); it would also create a
  privilege footprint that future maintainers would have to reason about.

- Candidate #2 sidesteps the privilege issue entirely. `pg_advisory_xact_lock`
  is a session-level primitive — no table privileges are involved. The lock
  key (`0x142142142142`) is a slice-142-stable BIGINT chosen so future
  slices that need their own advisory locks can pick distinct keys without
  cross-feature blocking. The lock is automatically released at transaction
  commit (xact_lock semantics).

- Candidate #3 is not expressible in standard Postgres without a trigger
  function. The maintenance burden (trigger keeps drifting from the
  in-handler invariant; tested behaviour lives in two places) outweighs the
  declarative benefit.

**Slice 198 precedent.** Slice 198's D1 considered an advisory lock for the
sibling first-install race and rejected it in favour of the partial UNIQUE
index because the schema primitive already existed. Slice 142 is the
opposite case: no schema primitive enforces "at least one row"; the
advisory lock is the simplest serialisation mechanism that works with
atlas_app's existing privilege footprint.

**Integration test.** `TestDemote_ConcurrentLastSuperAdmin` exercises the
safety rail under race: actor + sibling, two concurrent DELETE requests
each targeting the other. The advisory lock serialises the transactions;
exactly one succeeds (204 No Content) and one is rejected (409 Conflict).
Final state: exactly one super_admin remains. The test runs ~20ms.

---

## D2 — Audit log RLS shape

**The choice.** Two candidate shapes for the `super_admin_audit_log` table:

1. **Tenant-scoped under slice-036 append-only RLS** — match the slice-124
   unified aggregator contract (every UNION-ALL leg is FORCE-RLS,
   atlas_app-only, tenant-scoped).
2. **Platform-global, no RLS** — match the parent `super_admins` table's
   slice-198 D3 carve-out (no tenant_id; the row is the platform-global
   forensic anchor for a platform-global event).

**Picked: candidate #2 + dual-write `me_audit_log`.**

**Reasoning.**

- The slice-doc's AC-8 ("Slice 124 unified audit-log aggregator extension: 2
  new kind values: `super_admin_grant`, `super_admin_revoke`") suggested
  candidate #1. But the slice-124 aggregator P0-A4 explicitly requires every
  leg to run as atlas_app under tenant GUC. A new 10th UNION-ALL leg over a
  non-tenant-scoped table would either (a) ignore the tenant GUC and leak
  rows cross-tenant, or (b) silently return zero rows because the GUC scopes
  nothing.

- super_admin events ARE platform-global. The actor's session tenant is the
  closest tenant anchor available; the target's primary tenant is unknown
  (super_admin is platform-global by definition, the target may have N
  tenants).

- Dual-write satisfies AC-8 without breaking the aggregator contract:

  - `super_admin_audit_log` (this slice's new table) is the platform-global
    forensic record — append-only via SELECT+INSERT-only grants, no RLS,
    super_admin-only readable via the future read path.
  - `me_audit_log` (existing slice-181 tenant-scoped table) carries the
    parallel row tagged with the actor's session tenant + the new
    `'super_admin_grant'` / `'super_admin_revoke'` action values. The
    slice-124 aggregator surfaces these via the existing `kind='me'` UNION
    branch — no aggregator change, no RLS-routing violation.

- Both rows are written in the SAME transaction as the super_admins INSERT
  or DELETE (P0-SA-2). The two-row dual-write is a minor write amplification
  (2 INSERTs per event) but the read story is materially better: tenant
  operators see super_admin events affecting them via the existing
  audit-log workspace; super_admins see the platform-global anchor via the
  future maintainer-CLI read path.

- AC-9 cross-tenant isolation is preserved: the unified aggregator filters
  `me_audit_log` rows by tenant GUC; tenant A's operators see only their
  tenant's super_admin events; tenant B's tenant_id never appears.

**The dual-write column.** `super_admin_audit_log.actor_tenant_id` is data,
not a foreign key. The audit row survives tenant deletion.

---

## D3 — OPA rule structure

**The choice.** Two candidate rule shapes for `policies/authz/super_admin.rego`:

1. **Broad super_admin allow** — `allow if input.user.attrs.is_super_admin
== true` (no resource scoping). Treat super_admin as a god-mode bit that
   bypasses every other rule.
2. **Narrow super_admin allow** — `allow if is_super_admin AND
resource.type == "admin" AND resource.id == "super-admins" AND action in
{read, write, revoke}`. Scoped to the slice-142 management surface; does
   NOT grant blanket authority on other resources.

**Picked: candidate #2.**

**Reasoning.**

- Candidate #1 would make super_admin a tenant-write override. That is
  exactly the canvas §9.5 anti-pattern: super_admin is the PLATFORM
  identity-management role, not a tenant-write override. Tenant resources
  (controls, evidence, risks, policies) need per-tenant role grants.

- The slice-doc P0-SA-3 ("NO super_admin self-add to user_tenants for
  arbitrary tenant") implicitly extends to "NO super_admin implicit
  cross-tenant write authority". Candidate #2 enforces that at the OPA
  layer.

- Per-tenant admin authority still flows through admin.rego (which fires
  on `has_role("admin")` for ANY action on ANY resource within the tenant).
  A super_admin who also holds tenant_admin on tenant X gets that
  authority on tenant X via admin.rego — the dual-leg gate works as
  expected.

- The narrow rule lists the action set explicitly (`read`, `write`,
  `revoke`) so a future "approve" action on super-admins wouldn't auto-
  grant; it would need an explicit policy update.

**Where super_admin is sourced into the rego input.** `internal/authz/input.go`
reads `jwtmw.FromContext(r.Context()).SuperAdmin` and emits it as
`user.attrs.is_super_admin`. The bit flows from the JWT claim
(`atlas:super_admin`, slice 187) → the verified claims (slice 190) → the
rego input attrs.

**Handler-layer gate is the load-bearing one.** The OPA policy is defense-
in-depth. The handler-layer `requireSuperAdmin` (which reads
`jwtmw.FromContext().SuperAdmin` directly) is the primary check; OPA is
the second leg. Both must pass; both fail closed when the JWT context is
absent or the bit is false.

---

## D4 — Playwright fixture seeding for super_admin scenarios

**The choice.** Two candidate shapes for the AC-11 e2e spec:

1. **Real-data seeding** — extend `fixtures/e2e/super-admins.sql` with two
   pre-seeded super_admins rows + a non-super_admin user. The spec issues
   real HTTP requests through the BFF → atlas → Postgres path; assertions
   read DB state.
2. **Network-mock route fulfillment** — `page.route("**/api/admin/super-
admins", ...)` intercepts BFF calls and returns canned JSON. The spec
   never touches atlas or Postgres directly.

**Picked: candidate #2.**

**Reasoning.**

- The handler-level Go integration test
  (`internal/api/adminsuperadmins/handler_integration_test.go`) is the
  load-bearing assertion for the platform's behaviour against real Postgres.
  It covers grant happy path, grant idempotency, demote happy path, demote
  404, demote 409 (single + concurrent), and cross-tenant isolation. All
  of those land against the real DB.

- The Playwright spec's job is to assert the FRONTEND wiring: the page
  renders the seeded list, the grant form submits, the demote dialog
  surfaces 409 errors inline. None of these need a real backend; route
  mocking is materially simpler and deterministic.

- Candidate #1 would require extending the e2e seed harness
  (`fixtures/e2e/`) with super_admin-specific SQL plus a maintenance
  burden every time the table shape evolves. Candidate #2 keeps the spec
  hermetic.

- Slice 201's global-setup mints a JWT with `super_admin: true` already
  (see `web/e2e/global-setup.ts` line 83 — `super_admin: true`). The
  authedPage fixture in `web/e2e/fixtures.ts` reads that JWT into the
  session cookie. The mocked BFF responses correctly assume the caller
  IS a super_admin.

**The spec covers:**

- AC-11 happy path: list renders + grant form submits + new row appears.
- AC-11 + P0-SA-1 surface: demote dialog opens + 409 from backend surfaces
  inline as the operator-visible error message.

---

## D5 — Reconciling the slice-doc schema vs slice-198 reality

**The drift.** Slice 142's filed doc described:

```
super_admins(idp_issuer, idp_subject, granted_at, granted_by)
```

Slice 198 (`12a6219`) actually shipped:

```
super_admins(user_id UUID PRIMARY KEY, granted_at, granted_via)
```

The drift was created by the timeline: slice 142 was filed 2026-05-18; slice
198 shipped 2026-05-21 with a different schema shape via grilling. The
slice-doc was not updated post-198.

**The reconciliation.** Implement against reality (the slice-198 schema):

- POST `/v1/admin/super-admins` takes `{user_id: "<uuid>"}` (NOT
  `{idp_issuer, idp_subject}`).
- DELETE `/v1/admin/super-admins/{user_id}` (NOT
  `{idp_issuer}/{idp_subject}`).
- `granted_via` (NOT `granted_by`) is the provenance column; the runtime
  value is `'manual_grant'` (added to the CHECK in this slice's migration).

**Why not preserve the slice-doc shape?** The slice-198 user_id column is
the canonical join target for the `users` table (LEFT JOIN in the list
handler to surface display_name + email). The `(idp_issuer, idp_subject)`
pair is per-tenant in the `users` table — using it as the super_admin key
would require N rows for one identity (one per tenant the user has logged
into), which is exactly the representation bug slice 198 D2 rejected.

The slice-198 design was the correct one; the slice-142 doc carried
pre-198 thinking that should have been updated but wasn't. This decisions
log documents the reconciliation so future readers don't trip on the
mismatch.

**P0-SA-\* still honoured:**

- P0-SA-1: last-super_admin safety rail (advisory lock + count gate).
- P0-SA-2: super_admin_audit_log + me_audit_log written same-tx.
- P0-SA-3: NO super_admin self-add to user_tenants. The grant handler
  does NOT touch user_tenants. Super_admin alone confers NO tenant-write
  authority; tenant access requires explicit per-tenant role grants
  (slice 062's `/v1/admin/users` path).
- P0-SA-4: NO `expires_at` parameter on POST. v1 is permanent grants only.
- P0-SA-5: NO vendor-prefixed tokens in test fixtures.

---

## D6 — CI-delta scan

**The check.** Per the instructing brief, I scanned for:

1. Does the CI integration harness expect any super_admin seed data?
2. Does the CI integration harness apply my new migration in the right
   order?
3. Does my Playwright setup match CI shape (per slice 201's learning —
   `ATLAS_KEYSTORE_PATH=/tmp/atlas-ci/keys` + the global-setup that slice
   201 added)?
4. Are there any local-only test conveniences that wouldn't carry through
   to CI?

**Findings.**

- **CI does NOT seed super_admins.** `.github/workflows/ci.yml` lines
  280-289 (`Go · integration (Postgres RLS)`) apply migrations from
  `migrations/sql/` (skipping `.down.sql`) via psql. The integration
  test seeds its own super_admins rows via the BYPASSRLS admin pool
  (see `seedSuperAdmin` helper). No precondition leak.

- **Migration ordering is filename-sorted.** My migration
  `20260521030000_super_admins_full.sql` sorts AFTER slice 198's
  `20260521020000_super_admins.sql` — correct. The slice 198 migration
  must run first (creates the table) so the slice 142 ALTER + CHECK
  extension applies cleanly. Verified locally:

  ```
  $ ls migrations/sql/20260521*super_admins*
  migrations/sql/20260521020000_super_admins.down.sql
  migrations/sql/20260521020000_super_admins.sql
  migrations/sql/20260521030000_super_admins_full.down.sql
  migrations/sql/20260521030000_super_admins_full.sql
  ```

- **Playwright setup matches CI.** The slice 201 learning is that
  `ATLAS_KEYSTORE_PATH=/tmp/atlas-ci/keys` + `ATLAS_DATA_DIR=/tmp/atlas-ci`
  are required for the OAuth keystore + `/v1/test/issue-jwt`. Locally
  I started atlas with those vars pointing at `/tmp/atlas-142-test`.
  CI uses `/tmp/atlas-ci/keys`. Both paths satisfy the same precondition.
  My new spec
  (`web/e2e/super-admins.spec.ts`) uses the same `test`/`expect` exports
  from `./fixtures` that every other authed spec uses, so it inherits
  the JWT-via-global-setup pattern with no spec-specific config.

- **No local-only conveniences.** The spec does NOT depend on:

  - Real super_admins DB rows (mocked via `page.route()`).
  - The `psql` binary on the runner (does not call `seedFromFixture`).
  - Any spec-specific seed file under `fixtures/e2e/`.
  - Any env var beyond the standard `TEST_BEARER` populated by
    global-setup.

  All preconditions are satisfied by the global-setup the runner already
  invokes. CI should pass the spec with zero additional setup beyond
  the existing `Frontend · Playwright e2e` job env block.

- **OPA bundle sync.** The `policies/authz/super_admin.rego` file is also
  copied to `internal/authz/rego_bundle/super_admin.rego` because the
  `//go:embed all:rego_bundle` directive in `decision.go` reads from
  the embedded copy, NOT from the runtime `policies/` tree. Other rego
  files (admin.rego, etc.) follow the same "duplicate in both places"
  pattern. I did NOT add a sync target to the justfile because:

  - Every existing rego file is identically duplicated; no maintainer
    has flagged the duplication as a footgun yet.
  - The pre-commit hook is the right place for the sync (TBD slice).
  - The slice 142 PR keeps the existing pattern; if a maintainer
    prefers a generator, that is a separate cleanup slice.

- **`super_admins` row LEFT JOIN against users under RLS.** The List
  handler joins LEFT against `users` under the session tenant's RLS,
  so user rows in OTHER tenants render as `null` for display_name +
  email. This is expected behaviour: a super_admin whose primary
  tenant ≠ the session tenant shows up as a UUID-only row in the
  list, which is fine for the management surface but would warrant a
  follow-up if maintainers ever want cross-tenant identity resolution
  (out of scope for v1).

---

## Verification before commit

- `go build ./...` — clean
- `go vet ./internal/api/adminsuperadmins/... ./internal/authz/...` — clean
- `go test ./internal/...` — all unit tests pass (no regressions)
- `go test -tags=integration -race ./internal/api/adminsuperadmins/...` —
  all 8 slice-142 integration tests pass against local Postgres
- `npm run lint` (web) — clean (2 pre-existing warnings unrelated to slice 142)
- `npm run test` (web) — 689/689 vitest pass
- `npx playwright test e2e/super-admins.spec.ts` — both spec tests pass
  against locally-running atlas + web
- `just openapi-generate` + `bash scripts/check-openapi-drift.sh` —
  no drift; spec carries 204 routes (slice 142 added 3 entries)

---

## Spillovers filed

None. All 12 ACs land in this slice.

The future maintainer-CLI read path for `super_admin_audit_log`
(super_admin-only listing of platform-global events) is an obvious
follow-on but is not blocked on slice 142 — anyone with maintainer DB
access can SELECT today, and the integration test asserts the rows are
written. The UI surface for that read path would be a separate slice
(filed if + when an operator surfaces the need).
