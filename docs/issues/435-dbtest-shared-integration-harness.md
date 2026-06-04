# 435 — Shared integration-test DB/tenant harness package (`internal/dbtest`)

**Cluster:** Quality
**Estimate:** L
**Type:** JUDGMENT
**Status:** `ready` (no technical dependency — the harness extracts an
existing in-tree pattern)

## Narrative

The project keeps rediscovering the same integration-test boilerplate
per-package. Roughly 92 `_test.go` files under `internal/` open their own
connection pool (`pgxpool.New` / `sql.Open`), and ~30+ set `app.current_tenant`
or insert tenant rows inline before exercising an RLS-bound query. There is no
shared test-utility package: the only adjacent helpers are the narrow
`internal/api/testjwt` (JWT minting for handler tests) and
`internal/api/server_testing.go` (server wiring) — both API-package-local,
neither a general DB/tenant harness. The duphelper-lint analyzer (slices
369/391) guards only `internal/api/*` `writeJSON`/`writeError` duplication, not
test fixtures, so the test-side copy-paste is unguarded and proliferates.

Slice 353's Q-2 names this exact rediscovery cost — every package that lifts a
coverage floor re-derives "open a pool, set tenant context, seed a tenant."
This slice extracts a shared `internal/dbtest` package with the three
load-bearing primitives:

- `NewTestPool(t)` — opens the application-role pool (and, where needed, the
  admin/`atlas_migrate` pool for append-only-table cleanup), with `t.Cleanup`
  teardown.
- `SeedTenant(t, ...)` — inserts a tenant row and returns its id, using the
  same role/path the existing integration suites use.
- `WithTenantCtx(...)` — sets `app.current_tenant` (and any companion RLS
  context) on a connection/transaction, exactly preserving the semantics
  callers depend on.

Then it migrates a **representative subset** of packages to the shared helper
(not a big-bang rewrite of all 92 files) and documents the convention so new
integration suites reach for `dbtest` by default.

**Scope discipline.** This is NOT a big-bang migration of all 92 test files —
it ships the package plus a representative-subset migration that proves the
helper covers the real call shapes (a per-call-site `pgxpool.New`, a tenant
insert, an `app.current_tenant` set across at least the RLS-bound,
append-only, and BYPASSRLS-cleanup cases). The remaining migration is a
follow-on drain (mirror the integration-enrolment drain pattern of slices
390/402-408). It does NOT change any production code — only test scaffolding.
It does NOT relax the `-p 1` integration serialization or the no-retry policy.

## Threat model

This is the **highest-risk slice of the five** under the threat model, because
a test-harness refactor that weakens RLS/role assertions is a genuine
Information-disclosure / Elevation-of-privilege threat: tests are the project's
primary RLS-enforcement evidence (CLAUDE.md invariant #6 — tenant isolation is
enforced at the DB layer, and the integration tier is what _proves_ it holds).
A sloppy shared helper that sets the wrong role, skips the RLS context, or
seeds a tenant with over-broad privileges would silently weaken _every_ test
that adopts it — turning a green suite into a false-assurance suite.

**S — Spoofing.** N/A at runtime (no new endpoints). The relevant analogue:
`SeedTenant`/`WithTenantCtx` must not let a test impersonate a tenant context
it would not have under the real auth path — but since this is test-only
scaffolding feeding the same `app.current_tenant` GUC the production path uses,
the surface is the role/context fidelity, covered under E below.

**T — Tampering.** The helper must not mutate shared test state across tests
in a way that makes one test's seed leak into another's assertions.
Mitigation: every primitive takes `t` and registers `t.Cleanup`; seeds use
unique tenant ids per test; no package-global mutable pool that bleeds across
parallel tests. AC asserts cleanup runs.

**R — Repudiation.** N/A (test scaffolding leaves no product audit-log).

**I — Information disclosure (CRITICAL).** The whole point of the integration
tier is to prove RLS denies cross-tenant reads. If `WithTenantCtx` set the
context loosely (or `NewTestPool` handed back a BYPASSRLS pool where an
`atlas_app` pool was expected), a cross-tenant-leak test would pass against a
connection that _can't_ leak — a false green. Mitigation: `dbtest` MUST
preserve the exact role model in `internal/db/integration_test.go`
(`atlas_app` for RLS-bound queries, `atlas_migrate`/BYPASSRLS only for
append-only cleanup, `atlas_service_account` where used). AC-3 + AC-7 lock
this: the harness exposes the role explicitly, defaults to the
RLS-enforcing `atlas_app` role, and a negative test proves a cross-tenant read
is still DENIED through a `dbtest`-provided pool.

**D — Denial of service.** N/A — bounded representative subset; no unbounded
fan-out; respects `-p 1`.

**E — Elevation of privilege (CRITICAL).** The role boundary is the privilege
boundary. A helper that silently upgrades a test from `atlas_app` to
`atlas_migrate` would let RLS-bypass-only operations pass where the real path
forbids them. Mitigation: `NewTestPool` makes the role an explicit,
non-defaulted-to-privileged parameter (or ships two clearly-named
constructors: `NewAppPool` / `NewMigratePool`); the migrated subset uses the
app-role pool for assertions and the migrate pool only for cleanup, matching
the canonical model. AC-7 proves the app-role pool cannot bypass RLS.

**Verdict:** has-mitigations — the package is safe ONLY if it preserves the
exact `atlas_app`/`atlas_migrate`/`atlas_service_account` semantics. The two
load-bearing guards are AC-3 (explicit role, RLS-enforcing default) and AC-7
(negative cross-tenant test still denies through a `dbtest` pool). Ship-block
on either failing.

## Acceptance criteria

- [ ] **AC-1.** New package `internal/dbtest` exports `NewTestPool(t)` (or
      role-explicit `NewAppPool`/`NewMigratePool`), `SeedTenant(t, ...)`, and
      `WithTenantCtx(...)`, each `t`-scoped with `t.Cleanup` teardown.
- [ ] **AC-2.** `WithTenantCtx` sets `app.current_tenant` (and any companion
      RLS GUC the existing suites set) with semantics identical to the inline
      pattern it replaces — verified by a behavioral test, not just a compile.
- [ ] **AC-3.** The app-role pool constructor connects as the RLS-enforcing
      role (`atlas_app`), and the privileged (`atlas_migrate`/BYPASSRLS)
      constructor is separately named — the helper never silently hands back a
      privileged pool where an app-role pool is expected.
- [ ] **AC-4.** A representative subset of integration suites (≥3 packages
      spanning RLS-bound read, tenant-seed, and append-only-cleanup shapes) is
      migrated to `dbtest`, with the inline pool/tenant/context boilerplate
      removed.
- [ ] **AC-5.** The migrated suites pass under
      `go test -tags=integration -p 1 ./internal/...` with no behavior change
      (same assertions, same RLS expectations).
- [ ] **AC-6.** The convention is documented (in `CONTRIBUTING.md`
      "Integration-test enrolment" or a new `internal/dbtest/README.md`): new
      integration suites use `dbtest` rather than re-deriving the pool/tenant
      boilerplate.
- [ ] **AC-7.** A negative test in `internal/dbtest` (or a migrated suite)
      proves a cross-tenant read is still DENIED when issued through a
      `dbtest`-provided app-role pool — the RLS-fidelity guard.
- [ ] **AC-8.** `internal/dbtest` is enrolled in the integration job's package
      list (per the slice-345 discovery primitive) if it ships `integration`-
      tagged tests; otherwise its `KNOWN_UNENROLLED` ratchet entry is added
      with a one-line reason.
- [ ] **AC-9.** No production (`!_test.go`) code is modified by this slice.
- [ ] **AC-10.** The `-p 1` serialization and the integration no-retry policy
      are unchanged (the harness does not introduce a parallel pool that races
      bring-up).

## Constitutional invariants honored

- **Invariant #6 (tenant isolation via RLS).** The harness preserves — and
  AC-7 re-proves — that RLS denies cross-tenant reads; the refactor cannot
  weaken the project's primary RLS-enforcement evidence.
- The `atlas_app`/`atlas_migrate`/`atlas_service_account` role model
  (`internal/db/integration_test.go`) is preserved exactly, with the role made
  an explicit harness parameter.
- Honors the slice-353 Q-2 pure-Go-first / integration-as-safety-net
  convention and the slice-345 integration-enrolment policy (AC-8).
- Style: no emojis; Go house style (`gofmt`/`goimports`/`golangci-lint`).

## Canvas references

- `Plans/canvas/05-scopes.md` §5.4 — tenant isolation enforced at the DB layer
  via RLS (the invariant the harness must not weaken).
- CLAUDE.md "Testing discipline" + "Test-tier conventions" (Q-2, Q-7) — the
  convention this slice formalizes a shared helper for.

## Dependencies

- **#345** — `merged`. Integration-enrolment discovery primitive (AC-8 enrols
  the new package or ratchets it).
- **#391** / **#369** — `merged`. duphelper-lint precedent (this slice is the
  test-fixture analogue of the API-handler dedup those slices addressed).
- No technical code dependency — the harness extracts an existing pattern.

## Anti-criteria (P0 — block merge)

- Does NOT default `NewTestPool` to a BYPASSRLS/`atlas_migrate` pool — the
  RLS-enforcing `atlas_app` role is the default; privilege is opt-in and
  explicitly named (the load-bearing EoP guard).
- Does NOT skip or loosen the `app.current_tenant` RLS context in
  `WithTenantCtx` — a migrated test must enforce RLS exactly as its inline
  predecessor did (the load-bearing Information-disclosure guard; AC-7 proves
  it).
- Does NOT big-bang-migrate all 92 files — representative subset only; the
  remainder is a follow-on drain.
- Does NOT modify any production (non-test) code.
- Does NOT relax `-p 1` or the integration no-retry policy.

## Skill mix (3-5)

- `database-schema-designer` / `sql-database-assistant` — confirm the
  role/RLS semantics of the seed + context primitives.
- `tdd` — write the RLS-fidelity negative test (AC-7) first.
- `migration-architect` — plan the representative-subset migration + the
  follow-on drain shape.
- `security-review` — mandatory; this touches RLS test scaffolding (the
  project's RLS-enforcement evidence).
- `simplify` — collapse the migrated suites' boilerplate.

## Notes for the implementing agent

The canonical role model lives in `internal/db/integration_test.go`'s
`TestMain` — read it first. It opens _two_ pools: the `atlas_app` pool for
RLS-bound queries and the `atlas_migrate` (BYPASSRLS) pool for cleaning
append-only tables the app role intentionally cannot delete from. `dbtest`
must reproduce both, named so a caller cannot reach for the privileged pool by
accident. The single most important test in this slice is AC-7: stand up two
tenants, write a row under tenant A, switch context to tenant B via
`dbtest.WithTenantCtx`, and assert the read returns zero rows — through a
`dbtest`-provided app-role pool. If that test passes, the harness preserves the
invariant; if it can't be made to fail by deliberately weakening the helper,
the test is wrong. Surface the role-naming choice (single `NewTestPool(role)`
vs two constructors) in the decisions log — pattern-match to whichever reads
clearest at the migrated call sites.
