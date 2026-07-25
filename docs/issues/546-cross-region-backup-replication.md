# 546 — Cross-region backup replication (off-region durability)

**Cluster:** Infra
**Estimate:** M
**Type:** JUDGMENT
**Status:** `not-ready` (a hosted-offering concern — OQ #03)
**Parent:** slice 510 (automated backup + restore-verification)

## Narrative

Slice 510's v1 durability story is an **off-HOST** S3-compatible backup target
(the backup leaves the single VM). That survives a host loss but not a region
loss. A hosted offering (and stricter slice-373 BCP/DR tiers) will want
**cross-region replication**: the backup artifact lands in a second region so a
regional outage does not take recovery with it.

This slice would add cross-region replication of the slice-510 backup target —
either via S3 cross-region replication (CRR) bucket policy (no app change, a
documented bucket configuration) or an app-side second-target fan-out (write the
same dump to two regional buckets). The decision (rely on storage-layer CRR vs.
app-side dual-write) is the JUDGMENT call.

## Scope (when picked up)

- Either a documented S3 CRR posture for the backup bucket (preferred — no app
  change), OR an app-side multi-target writer extending the slice-510 `Target`
  interface to a `MultiTarget` fan-out.
- Restore-verification + the runbook updated to name the secondary region and a
  region-failover recovery procedure.
- Cross-link to OQ #03 (hosted-offering durability) resolution.

## Threat model

Cross-region replication **multiplies** the crown-jewel copies (the full-DB dump
now exists in two regions), so the **information-disclosure** blast radius
doubles: BOTH buckets MUST be encrypted at rest + access-controlled, and the
replication role MUST be scoped to the two backup buckets only (a CRR role with
broad cross-account access is an exfiltration vector). If app-side dual-write is
chosen, the second target's credentials are deployment config, never
tenant-reachable (slice 510's containment). The secondary region's access policy
must be at least as strict as the primary — a weaker secondary is the path of
least resistance for an attacker.

## Dependencies

- **#510** (automated backup + restore-verification) — the `Target` interface +
  off-host target this extends.
- **OQ #03** (hosted offering durability) — the open question this resolves a
  piece of.
- **#373** (BCP/DR plan) — the region-loss RTO/RPO tier this enables.
