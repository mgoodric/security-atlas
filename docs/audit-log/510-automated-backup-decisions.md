# 510 — Automated backup + scheduled restore-verification — decisions log

JUDGMENT slice. The maintainer iterates post-deployment; the build-time calls
below are made here and recorded, not blocked on a human sign-off (per the
slice-development JUDGMENT convention). The product-runtime AI-assist boundary is
separate and untouched (no AI surface in this slice).

Parent slice: `docs/issues/510-automated-backup-restore.md`.

---

## Decisions

### D1 — In-process scheduler inside `cmd/atlas`, NO new compose service.

The backup + restore-verification run as two in-process tick-loops inside the
existing `atlas` binary, mirroring the exception-expiry (`internal/exception`),
metrics (`internal/metrics/scheduler`), eval-recompute, and freshness/drift
schedulers already wired in `cmd/atlas/main.go`. They mount on the same
`DATABASE_URL` (BYPASSRLS migrator) guard the other cross-tenant sweeps use.

**Why:** the single-VM self-host target (canvas §9) wants no external cron. More
importantly, adding a _new compose service_ is exactly the class of change that
broke slice 473's self-host bundle (a new service's EXIT-trap exit-status leak +
a proxy-overlay network-membership gap broke bundled+external). An in-process
scheduler adds ZERO new services / networks / depends_on edges, so the four
self-host-bundle legs (bundled / external / proxy / migrate) keep their exact
topology. The only compose-surface change is one optional named volume + env vars
on the existing `atlas` service.

**Confidence:** high. **Detection-tier:** the self-host-bundle CI matrix would
catch a topology regression; an in-process scheduler is invisible to it by design.

### D2 — pg_dump: PURE-GO logical dump, NOT shelling out to `pg_dump`.

The atlas runtime image is `gcr.io/distroless/static-debian12:nonroot`
(`atlas.Dockerfile`) — no shell, no package manager, no `pg_dump` binary, and
adding the postgres-client would (a) break the distroless invariant the project
deliberately holds and (b) bloat a ~2 MB base by ~30 MB. So the dump is produced
in pure Go: enumerate user tables via `information_schema`/`pg_catalog` over the
BYPASSRLS migrator pool, and for each emit a `pg_dump --inserts`-style restorable
SQL stream (a `TRUNCATE`-free, `INSERT`-per-row data section preceded by the DDL
captured from the live schema). Restore = replay that SQL into a fresh database.

**Trade-off:** a hand-rolled dumper is less battle-tested than `pg_dump`. We bound
the risk by (a) the restore-verification job that actually replays every backup
into an ephemeral DB and smoke-checks it (a backup that doesn't restore fails
loudly), and (b) keeping the dump format plain SQL an operator can `psql -f` by
hand (cross-linked from the slice-432 runbook). The operator docs ALSO keep the
manual `pg_dump` path (slice 432) as the belt-and-suspenders for a full
catalog-fidelity dump; this feature is the _automated continuity_ layer, not a
replacement for `pg_dump` fidelity.

**Confidence:** medium. **Revisit-once-in-use:** if operators need exact
`pg_dump` catalog fidelity (extensions, sequences edge-cases, large objects), a
follow-up could ship a sidecar dump service with the postgres-client image — but
that re-introduces the new-service self-host risk D1 avoids, so it is deferred.
**Detection-tier:** integration (the full backup→restore→verify cycle, AC-4).

### D3 — Backup-target interface: `Target` (Put/List/Get/Delete).

One interface, two implementations: `LocalTarget` (default — writes to
`ATLAS_BACKUP_DIR`, a mounted named volume for the single-VM self-host target)
and `S3Target` (off-host durability — reuses the narrow `S3API` seam from
`internal/artifact`, integration-tested against the MinIO the harness already
provides). Mirrors the evidence object-storage abstraction (`internal/artifact`).

**Confidence:** high. **Detection-tier:** integration (AC-2, MinIO leg).

### D4 — Scheduling default: daily backup, daily restore-verification.

Backup cadence defaults to `24h` (`ATLAS_BACKUP_INTERVAL`); restore-verification
defaults to `24h` (`ATLAS_BACKUP_VERIFY_INTERVAL`), fired offset after the
backup. Both fire an immediate sweep on boot (matching every other scheduler) so
a fresh deploy gets first signal without waiting a full day. Daily matches the
slice-373 BCP/DR RPO tier for the solo-operator self-host posture and the
slice-432 runbook's daily-checkpoint guidance.

**Confidence:** high. **Detection-tier:** none (config default; unit-tested
parse).

### D5 — Retention default: keep 7 daily + 4 weekly.

`ATLAS_BACKUP_KEEP_DAILY=7`, `ATLAS_BACKUP_KEEP_WEEKLY=4`. After each backup the
rotation pass deletes backups outside the window: keep the most-recent N dailies,
plus the most-recent backup of each of the last M ISO-weeks. Bounded growth
(P0-510-3). Pure-Go selection logic (helpers-tested), target deletion
integration-tested.

**Confidence:** high. **Detection-tier:** unit (selection) + integration
(deletion / no-unbounded-growth, AC-3).

### D6 — Ephemeral DB mechanism: `CREATE DATABASE` / replay / `DROP DATABASE`.

Restore-verification connects to the cluster's maintenance DB (`postgres`) via
the migrator role, issues `CREATE DATABASE atlas_restore_verify_<unix-ns>`,
opens a fresh pool to it, replays the dump SQL, runs the smoke check
(table-count > 0, row counts non-zero on a sentinel table, a sentinel SELECT
returns), then `DROP DATABASE ... WITH (FORCE)` in a `defer` (P0-510-2 — no
standing second copy, even on smoke-check failure). The ephemeral DB name carries
a unix-nanosecond suffix so concurrent verifications never collide.

**Confidence:** medium-high. **Revisit-once-in-use:** on a managed Postgres where
the role cannot `CREATE DATABASE`, verification self-disables with a loud status
row rather than failing the platform; documented in `SELF_HOSTING.md`.
**Detection-tier:** integration (AC-4 full cycle + the teardown assertion).

### D7 — sha256 integrity, recompute-before-verify.

Every backup carries a sha256 over the dump bytes, stored on the `backup_runs`
status row and alongside the artifact (a `<name>.sha256` sidecar for the
local/S3 target). Restore-verification recomputes the hash over the fetched
artifact and compares BEFORE replaying; a mismatch fails verification loudly
(status row `outcome=failed`, reason `hash mismatch`) and never replays a
tampered dump. Mirrors `internal/artifact`'s server-side recompute (never trust a
stored hash) (AC-5).

**Confidence:** high. **Detection-tier:** integration (corrupted-artifact test,
AC-5).

### D8 — Status table `backup_runs`: DEPLOYMENT-scope, NOT tenant-scoped.

A backup is a full cross-tenant operation — it has no single `tenant_id`, so a
tenant-scoped RLS table is the wrong shape. `backup_runs` is a deployment-scope
append-only table: `GRANT SELECT, INSERT ON backup_runs TO atlas_migrate` ONLY —
it is **NOT granted to `atlas_app`** (the RLS-enforced tenant-reachable role).
This is the schema-level enforcement of P0-510-1 / AC-7: no tenant-facing API
(all of which run through `atlas_app`) can read or write a backup record, because
the role they run as has no privilege on the table. Append-only: INSERT + a
narrow UPDATE for outcome transition (running→succeeded/failed), no DELETE grant.

**Confidence:** high. **Detection-tier:** integration (AC-7: assert atlas_app
cannot read backup_runs + no HTTP route exposes it).

### D9 — Failure alerting composes with slice 445, does NOT duplicate it.

On a backup OR verification FAILURE the scheduler writes the status row
(`outcome=failed`) and emits a deployment-level alert. The in-app notification +
the slice-445 email channel are the sinks. Because slice 445's channel is
tenant-scoped (per-user opt-in digest) and a backup failure is deployment-level
(no tenant), the alert path notifies the deployment's super-admins via the
existing notification store under the bootstrap/admin tenant context, which the
slice-445 digest then delivers through its normal opted-in path. The backup
scheduler is a NOTIFICATION PRODUCER here (unlike 445, which is a sink) — it
writes one notification per distinct failure, deduped on the run id so a flapping
job does not spam (AC-6).

**Confidence:** medium. **Revisit-once-in-use:** the precise super-admin
fan-out target may want a dedicated deployment-ops notification type rather than
riding the tenant notification store; recorded as a candidate. **Detection-tier:**
integration (failure → status row + notification written).

---

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_target`: integration — the load-bearing ACs (4/5/7) are
  Postgres+MinIO integration tests by nature (real DB create/drop, real artifact
  round-trip, real RLS-role grant boundary).
- `detection_tier_actual`: none — no defect surfaced during the build that
  escaped its target tier. (Updated if CI surfaces one.)

## Constitutional conflicts surfaced

None. Invariant #6 (RLS) is deliberately crossed by a full dump — the slice doc
and CLAUDE.md both name this as a DEPLOYMENT-privileged operation contained at the
deployment tier (D8 enforces it at the grant level), NOT a tenant-reachable
surface. This is containment, not a violation.
