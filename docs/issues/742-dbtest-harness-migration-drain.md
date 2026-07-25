# 742 — Migrate remaining integration suites to `internal/dbtest` (drain)

**Cluster:** Quality
**Estimate:** L (drain — split into batches)
**Type:** JUDGMENT
**Status:** `ready` (no technical dependency — the harness already exists on
`main` from slice 435; this is mechanical adoption)

> Surfaced during slice 435 (shared `internal/dbtest` harness). Slice 435
> shipped the harness plus a representative-subset migration
> (`internal/scope`, `internal/risk`, `internal/freshnessdrift`) and
> explicitly deferred the remaining ~80 suites to this follow-on drain,
> mirroring the slice-390 / 402-408 integration-enrolment drain pattern.

## Narrative

~80 integration suites under `internal/` still re-derive the
pool/DSN/tenant-seed/context boilerplate that slice 435 extracted into
`internal/dbtest` (`NewAppPool` / `NewMigratePool` / `SeedTenant` /
`WithTenantCtx`). The canonical copy-paste idiom is the
`appDSN` / `adminDSN` / `openPool` / `freshTenant` helper block (plus inline
`tenancy.WithTenant`), present in ~80 files:

```
$ grep -rl 'func openPool' internal/ | grep _test.go | wc -l   # ~80
```

This slice drains that backlog by migrating the remaining suites to
`dbtest`, in batches, with **no behavior change** (same assertions, same RLS
expectations). It is a ratchet: each batch removes the inline boilerplate
from its files and the suite continues to pass under
`go test -tags=integration -p 1 ./internal/...`.

## Scope discipline

- **No production (`!_test.go`) code changes** — test scaffolding only.
- **No behavior change** — same assertions, same RLS expectations; the
  migrated suite is a pure refactor.
- **Batch it** — do not big-bang all ~80 in one PR. A sensible batch is a
  cluster (e.g. all `internal/auth/*`, or all `internal/api/*` read
  handlers). Each batch is independently mergeable.
- **Preserve the role model** — `NewAppPool` for RLS-bound assertions,
  `NewMigratePool` only for append-only cleanup / cross-tenant seeding. Never
  default an assertion to the privileged pool (slice 435 AC-3 / the EoP
  guard). A suite that has its own self-contained, differently-named helpers
  (e.g. `internal/risk/slice053_integration_test.go`'s `slice053OpenPool`)
  may be migrated too, but it is lower priority — it does not depend on the
  shared symbols, so it is not a compile-blocker for anything.

## Acceptance criteria

- [ ] **AC-1.** A batch of integration suites (≥1 cluster) has its inline
      `openPool` / `appDSN` / `adminDSN` / `freshTenant` /
      `tenancy.WithTenant` boilerplate replaced by `dbtest` calls.
- [ ] **AC-2.** The migrated suites pass under
      `go test -tags=integration -p 1 ./internal/...` with no behavior change.
- [ ] **AC-3.** RLS-bound assertions run through `dbtest.NewAppPool`; the
      privileged `dbtest.NewMigratePool` is used only for append-only cleanup
      / cross-tenant seeding.
- [ ] **AC-4.** No production (`!_test.go`) code is modified.
- [ ] **AC-5.** The `-p 1` serialization and the integration no-retry policy
      are unchanged.
- [ ] **AC-6.** When the backlog is fully drained, the `grep -rl 'func
openPool' internal/ | grep _test.go` count reaches zero (or the
      remaining entries are documented as intentionally distinct).

## Dependencies

- **#435** — the `internal/dbtest` harness (the package this slice adopts).
  Lands first; this is pure adoption.

## Anti-criteria (P0 — block merge)

- Does NOT change any production (non-test) code.
- Does NOT default an RLS assertion to the BYPASSRLS pool.
- Does NOT loosen `app.current_tenant` / RLS semantics in any migrated suite.
- Does NOT relax `-p 1` or the integration no-retry policy.
