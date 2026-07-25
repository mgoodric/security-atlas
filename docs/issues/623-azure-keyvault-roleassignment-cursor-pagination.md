# 623 — Azure Key-Vault: roleAssignments cursor pagination for large estates

**Cluster:** Connectors
**Estimate:** S (0.5-1d)
**Type:** STANDARD (mechanical — follow the ARM `nextLink` continuation)
**Status:** `ready`
**Parent:** #615 (and #521 / #486 R3). Spun off from slice 615's decisions-log
D4 / "Revisit once in use" — the RBAC role-assignment read consumes only a
bounded first page.

## Narrative

Slice 615 added the per-vault `Microsoft.Authorization/roleAssignments` read for
RBAC-mode Key Vaults, but the read is a single **bounded** page truncated to
`maxRoleAssignmentsPerVault` (200), with a run-wide cap of
`maxRoleAssignmentsPerRun` (2000). A vault with more than 200 role assignments
(or an estate whose total exceeds the run cap) is silently truncated — the
connector under-reports the RBAC principal set with no error. This was an
explicit deferral in slice 615 (and is the same unbounded-read concern flagged
by slice 486 R3 / slice 521 for the vault list itself).

This slice adds ARM list pagination to the roleAssignments read: follow the
`nextLink` continuation URL from each list response until the server returns no
`nextLink`, accumulating entries across pages, bounded by a sane page cap + the
existing run timeout. Ideally the helper is shared so the existing bounded vault
list read (`Client.ListVaults`) and any sibling ARM list in the connector can
adopt the same `nextLink`-following reader.

## Acceptance criteria

- [ ] **AC-1.** The per-vault roleAssignments read follows the ARM `nextLink`
      continuation to completion (bounded by a page cap + the existing run
      timeout), faked in tests with a multi-page server.
- [ ] **AC-2.** The run-wide cap (`maxRoleAssignmentsPerRun`) still applies as a
      DoS backstop; crossing it truncates honestly (documented), never loops.
- [ ] **AC-3.** The `keyvault.AccessEntry` over-collection guard is unchanged —
      still principal id + role name metadata only; no secret/key/cert value.
- [ ] **AC-4.** ARM **Reader** only; no new Azure scope, no data-plane role.

## Anti-criteria (P0)

- Does NOT widen the platform-side wire — push only (invariant #3).
- Does NOT widen Azure permissions beyond the existing ARM Reader role.
- Does NOT read any secret/key/certificate value or touch the data plane.
- Does NOT change the `azure.keyvault_access_config.v1` schema.

## Dependencies

- **#615** — the bounded roleAssignments read this paginates.
- **#521** / **#486** — the Key-Vault collector + ARM client.
