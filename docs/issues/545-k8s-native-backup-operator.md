# 545 — Helm/K8s-native backup integration (Velero / CloudNativePG)

**Cluster:** Infra
**Estimate:** M
**Type:** JUDGMENT
**Status:** `not-ready` (depends on the Helm/K8s SaaS deployment maturing)
**Parent:** slice 510 (automated backup + restore-verification)

## Narrative

Slice 510 targets the **docker-compose single-VM self-host** operator (the v1
primary persona) with an in-process logical-backup scheduler. The Helm/K8s SaaS
deployment path is a different operational world: a Kubernetes cluster typically
already runs a backup operator (Velero for volume/namespace snapshots,
CloudNativePG for Postgres-native base-backup + WAL), and an in-process app-side
backup duplicates (and can conflict with) that layer.

This slice would document + wire the K8s-native backup story: when running under
the Helm chart, the operator can **disable the in-process backup scheduler**
(via the existing config — set the interval to a no-op / a new
`ATLAS_BACKUP_ENABLED=false`) and rely on CloudNativePG backups + Velero, with a
documented restore-verification hook so the slice-510 verification discipline
(a backup you have never restored is not a backup) still applies in the K8s
posture.

## Scope (when picked up)

- A Helm values toggle to disable the app-side backup scheduler when a
  cluster-native operator owns backups.
- Documented CloudNativePG `Backup`/`ScheduledBackup` + Velero schedule for the
  atlas namespace, cross-linked from the slice-432 runbook.
- A K8s-flavored restore-verification job (CronJob) reusing the slice-510
  verifier semantics against a CloudNativePG-restored instance.

## Threat model

A cluster-native operator (Velero/CloudNativePG) takes the same crown-jewel
full-DB copy slice 510 produces, so the **information-disclosure** posture is
identical: backup destinations (object storage) MUST be encrypted at rest +
access-controlled, and the backup ServiceAccount / IRSA role MUST be
least-privilege (write to the backup bucket only — never broad cluster-admin).
The restore path stays cluster-operator-privileged, never tenant-reachable
(slice 510's containment carries over). A misconfigured Velero schedule that
snapshots the wrong namespace could leak data across deployments — the runbook
must pin the namespace + bucket explicitly.

## Dependencies

- **#510** (automated backup + restore-verification) — the verification
  semantics this reuses; the `ATLAS_BACKUP_*` config this extends with an enable
  toggle.
- The Helm chart (`deploy/helm/`) — the K8s deployment this targets.
- **#373** / **#432** — the BCP/DR plan + runbook this extends for the K8s posture.
