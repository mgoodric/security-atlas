# 510 — Automated backup + scheduled restore-verification (operationalizing the slice-432 runbook)

**Cluster:** Infra
**Estimate:** M (2-3d)
**Type:** JUDGMENT (backup-target abstraction + scheduling default)
**Status:** `ready`

## Narrative

**WHY.** Slice 432 (backup/restore/upgrade runbooks, merged) documents the
**manual** procedure: take a pre-upgrade checkpoint, run a restore drill. It
explicitly scopes automation OUT — "It does NOT add a backup tool, a scheduler,
or an automated backup feature (that would be a code slice)" (P0-432-4). And
slice 373 (BCP/DR plan, merged) commits RTO/RPO tiers that a _manual-only_ backup
cannot credibly meet for a solo operator: a runbook the operator forgets to run
produces no backup at all, and an RPO of hours is meaningless if backups are
ad-hoc. **This is the code slice slice 432 named.** It turns the documented
procedure into a running feature so the solo operator's recovery posture matches
the BCP/DR commitments without depending on manual diligence.

**WHAT this slice ships.**

1. **Scheduled backup job.** A backup operation (Postgres logical dump + object
   -storage evidence-artifact sync) on an operator-configurable schedule (default
   daily), implemented as an in-process scheduled job (matching the existing
   exception-expiry / metrics-scheduler pattern — no external cron dependency for
   the self-host single-VM target).
2. **Backup-target abstraction.** Backups write to a configured destination:
   local volume (default for single-VM self-host) or an S3-compatible bucket
   (for off-host durability, satisfying the BCP/DR off-site tier). One interface,
   pluggable target — consistent with the evidence object-storage abstraction.
3. **Retention + rotation.** Operator-configurable retention (default keep N
   daily + M weekly); old backups rotate out automatically. No unbounded growth.
4. **Scheduled restore-verification.** The load-bearing half: a periodic job that
   restores the latest backup into an ephemeral throwaway database and runs a
   smoke check (schema migrates clean, row counts non-zero, a sentinel query
   returns) — then reports pass/fail. A backup you have never restored is not a
   backup; this makes slice 432's "restore drill" continuous instead of a manual
   AC-14 one-shot.
5. **Backup status surface + alerting.** A status endpoint + in-app notification
   (composing with slice 445's email substrate) when a backup or restore
   -verification fails — so a silently-broken backup is loud, not discovered at
   recovery time.

**SCOPE DISCIPLINE — what's deliberately out.**

- **The runbook docs** — slice 432 owns them; this slice operationalizes and
  cross-links, it does not rewrite them.
- **The BCP/DR governance plan** — slice 373 owns RTO/RPO tiers; this slice is
  the mechanism that lets a solo operator _meet_ them, not a re-statement.
- **Point-in-time recovery (WAL archiving / PITR).** Logical-dump + artifact-sync
  is the v1 mechanism (matches the single-VM self-host target + the
  manual-dump-restore guidance already in `SELF_HOSTING.md`). Continuous WAL
  archiving / PITR is a heavier ops posture — a future slice for operators who
  need sub-dump RPO.
- **Helm/K8s-native backup operators** (Velero, CloudNativePG backups). The K8s
  SaaS deployment can layer those; this slice targets the docker-compose
  single-VM self-host operator (the v1 primary).
- **Cross-region replication.** Off-host S3 target is the v1 durability story;
  multi-region is a hosted-offering concern (OQ #03).

## Threat model (STRIDE)

A backup system **reads the entire database and writes it to a durable store** —
the backup artifact is a complete copy of all tenant data, so it is a
crown-jewel-sensitivity artifact, and the restore-verification path executes a
restore (a high-privilege operation).

**S — Spoofing.** A forged backup destination config could exfiltrate the whole
DB to an attacker bucket. **Mitigation:** the backup-target config is operator
-set (env / admin-only config), not user-supplied at runtime; destination
credentials live in the deployment config, never in tenant-reachable surfaces.

**T — Tampering.** A tampered backup would silently corrupt recovery.
**Mitigation:** each backup carries a sha256 content hash (consistent with the
evidence-integrity pattern); the restore-verification recomputes + checks it
before the smoke test; a hash mismatch fails verification loudly.

**R — Repudiation.** "Did the backup run? Did the restore verify?" must be
answerable. **Mitigation:** every backup + verification run writes an append-only
audit/status record with outcome + timestamp; the status surface is the durable
record.

**I — Information disclosure (PRIMARY).** A backup artifact is a full cross-tenant
data copy — it deliberately spans the RLS boundary (a full dump cannot be
RLS-scoped). **Mitigation:** backup artifacts are encrypted at rest at the
storage layer (S3 SSE / volume encryption per the deploy guidance); the backup
runs as a deployment-level operation (not a tenant-reachable feature — no tenant
can trigger or read a backup); the restore-verification uses an ephemeral
throwaway DB that is destroyed after the smoke check (no standing second copy).

**D — Denial of service.** A backup job could contend with production load, and a
runaway restore-verification could exhaust disk. **Mitigation:** the backup runs
on a schedule with a bounded window; the restore-verification uses a
size-bounded ephemeral DB and tears it down; retention/rotation prevents
unbounded disk growth.

**E — Elevation of privilege.** The backup job reads all data and the restore
path writes a full DB — both must be deployment-privileged, not tenant-privileged.
**Mitigation:** both run in the deployment's service context, unreachable from any
tenant-facing API; no tenant role can invoke backup or restore.

## Acceptance criteria

- [ ] **AC-1.** A scheduled backup job (Postgres dump + artifact sync) runs on an
      operator-configurable schedule (default daily), in-process (no external cron
      dependency for self-host).
- [ ] **AC-2.** Backups write through a pluggable target interface supporting
      local volume + S3-compatible (integration test against MinIO).
- [ ] **AC-3.** Retention/rotation keeps a configurable window and rotates old
      backups out (integration test asserts rotation; no unbounded growth).
- [ ] **AC-4.** Scheduled restore-verification restores the latest backup into an
      ephemeral DB, runs a smoke check, and reports pass/fail; the ephemeral DB is
      destroyed after (integration test exercises a full backup->restore->verify
      cycle).
- [ ] **AC-5.** Each backup carries a sha256 hash; restore-verification checks it;
      a mismatch fails verification (integration test with a corrupted artifact).
- [ ] **AC-6.** Every backup + verification run writes an append-only status
      record; a failure raises an in-app notification (composing with slice 445).
- [ ] **AC-7.** Backup + restore are deployment-privileged only — no tenant-facing
      API can trigger or read a backup (asserted: no route exposes them).

## Anti-criteria (P0 — block merge)

- **P0-510-1.** Does NOT expose backup/restore to any tenant-facing role.
- **P0-510-2.** Does NOT leave a standing second copy of data after restore
  -verification (ephemeral DB torn down).
- **P0-510-3.** Does NOT grow backup storage unbounded (retention/rotation
  enforced).
- **P0-510-4.** Does NOT rewrite the slice-432 runbook or the slice-373 BCP/DR
  plan — operationalizes + cross-links only.

## Dependencies

- **#432** (backup/restore/upgrade runbooks) — `merged`. The procedure this
  automates; P0-432-4 explicitly names this as the code slice.
- **#373** (BCP/DR plan) — `merged`. The RTO/RPO commitments this lets a solo
  operator actually meet.
- **#445** (email/SMTP delivery substrate) — `ready`. The failure-alert sink.
- **Metrics-scheduler / exception-expiry tick-loop pattern** — `merged`. The
  in-process scheduled-job precedent this reuses.

## Canvas references

- `Plans/canvas/09-tech-stack.md` (object storage, evidence-integrity sha256,
  docker-compose single-VM self-host target)
- `docs/issues/432-backup-restore-upgrade-runbooks.md` (P0-432-4: automation is
  a separate code slice — this one)

## Constitutional invariants honored

- **#6** RLS tenant isolation — backup is a deployment-level operation,
  unreachable from any tenant API; the full-dump RLS-boundary crossing is
  contained at the deployment privilege tier + encrypted at rest.
- **Evidence-integrity (sha256)** — backups carry + verify a content hash,
  consistent with the per-record evidence-integrity pattern.
- **AI-assist boundary** — N/A (no AI surface).
