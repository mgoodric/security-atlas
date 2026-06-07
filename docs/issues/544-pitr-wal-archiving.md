# 544 — Point-in-time recovery (WAL archiving / PITR) for self-host

**Cluster:** Infra
**Estimate:** L
**Type:** JUDGMENT
**Status:** `not-ready` (depends on operator demand for sub-dump RPO)
**Parent:** slice 510 (automated backup + restore-verification)

## Narrative

Slice 510 ships logical-dump-based automated backup as the v1 RPO mechanism: a
daily (configurable) `pg_dump`-equivalent logical dump + scheduled
restore-verification. That bounds data loss to the backup interval. Some
operators (higher transaction volume, stricter slice-373 BCP/DR RPO tiers) will
want **sub-dump RPO** — recovery to an arbitrary point in time, not just the
last scheduled dump.

This slice would add **continuous WAL (write-ahead log) archiving + PITR**: the
Postgres `archive_command` (or `pg_receivewal`) streams WAL segments to the
off-host backup target between base backups, and a documented restore procedure
replays WAL to a chosen recovery target time. It is a heavier ops posture than
logical dumps (a base backup + a continuous WAL stream + a restore-to-timestamp
runbook), which is why slice 510 deliberately scoped it OUT (logical-dump first;
PITR when demand surfaces).

## Scope (when picked up)

- A `pgbackrest` / `wal-g` integration OR a documented native
  `archive_command` + base-backup posture for the docker-compose single-VM
  target.
- A PITR restore runbook (recover to timestamp) cross-linked from the
  slice-432 backup-restore runbook and the slice-373 BCP/DR plan.
- Restore-verification extended to assert a WAL-replay recovery target.

## Threat model

PITR moves MORE of the database off-host (a continuous WAL stream vs. periodic
dumps), so the **information-disclosure** surface from slice 510 widens: the WAL
archive is a continuous full-fidelity copy of every committed change, including
all tenant evidence, and MUST be encrypted at rest + access-controlled with the
same (or stricter) posture as the logical-dump target. The `archive_command`
runs with cluster privilege; a compromised archive command could exfiltrate the
WAL stream — it must be operator-set deployment config, never tenant-reachable
(same containment as slice 510's deployment-privilege boundary). PITR restore is
a high-privilege operation (replays WAL into a cluster) and must stay
deployment-privileged, never exposed to any tenant role.

## Dependencies

- **#510** (automated backup + restore-verification) — `merged`/in-progress.
  The off-host target abstraction + status ledger this builds on.
- **#373** (BCP/DR plan) — the RPO tiers a sub-dump RPO would let operators meet.
- **#432** (backup-restore runbook) — the manual procedure a PITR runbook extends.
