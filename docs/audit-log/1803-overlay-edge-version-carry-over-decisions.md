# #1803 — overlay edges carry over to new framework versions (decisions log)

#1735 user story 24. When a super_admin approves an `exact_code` row in the
framework-version migration queue (ADR 0019), every tenant's active overlay
edges on the version-N requirement are copied to the N+1 requirement, in the
same transaction as the approval, one audit row per copy.

- detection_tier_actual: integration
- detection_tier_target: integration

The integration tier caught one fixture defect while building (a version
seeded with a status the enum does not have). No product defect surfaced.

## D1 — the approval copies every tenant's edges, each into its own tenant

The migration-queue approval is a global catalog act: a super_admin, no
tenant GUC. There is no "tenant A's carry-over" to scope it to. So one
approval carries every tenant's active overlay edges over, and each copy
keeps its source edge's `tenant_id`. A tenant's edges never land in another
tenant: the integration test gives tenants A and B their own edge on the
same requirement and checks each sees only its own copy, and that the N
edges are byte-identical before and after.

## D2 — a SECURITY DEFINER function, `carry_over_overlay_edges`

The overlay table is under `FORCE ROW LEVEL SECURITY` and the approval runs
on the app role with no tenant context, so the app role cannot read the edges
it needs to copy. The function runs as its owner (the BYPASSRLS migration
role, the same as `fn_metric_inputs_replicate`). It refuses any migration row
that is not `approved` + `exact_code` with both requirement ids, so calling it
can do no more than an approval already implies. EXECUTE is revoked from
PUBLIC and granted to `atlas_app` only.

## D3 — idempotent through the active-pair unique index

The insert is `ON CONFLICT (tenant, requirement, anchor) WHERE status =
'active' DO NOTHING`, and audit rows are written only for rows actually
inserted. A double approval is already refused (`ErrAlreadyDecided`), and a
direct re-run of the function copies nothing.

## D4 — the copy keeps `created_by`; the audit row names the approver

"Identical fields" covers the attestation: relationship, strength, rationale,
source and the person who attested it. The copy gets a new id and
`created_at`. The audit row (action `carry_over`, new in the CHECK) has the
reviewer as actor and records `carried_from_edge_id` and `migration_id` in
`after`, so the copy traces back to its source edge and the approval.

## D5 — only `exact_code` rows carry anything

Approving an `added` row (a requirement new in N+1) copies nothing, nor does
a `removed` row or a rejection. The approval response gains
`overlay_edges_copied` so the super_admin can see the effect.

## D6 — the down migration keeps history

Copied edges are ordinary overlay edges and stay. The audit log is
append-only, so the restored two-value CHECK is `NOT VALID` and existing
`carry_over` rows survive a rollback.
