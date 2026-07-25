# 615 — Azure connector: Key-Vault RBAC role-assignment enumeration

**Cluster:** Connectors
**Estimate:** S (0.5-1d)
**Type:** JUDGMENT (read-shape choice + scope-minimum)
**Status:** `ready` (depends on #521 — Azure Key-Vault access-policy evidence — merged first)

## Narrative

Slice 521 added Key-Vault access-policy / RBAC evidence
(`azure.keyvault_access_config.v1`) to the Azure connector. For a **legacy
access-policy** vault, slice 521 populates the `access_entries` list in full
(each principal + its granted keys/secrets/certificates permission verbs). For a
vault in **Azure RBAC authorization** mode (`rbac_authorization=true`), slice 521
reports the access **model** flag + the vault config but does NOT enumerate the
per-vault role assignments — because v0's read is the bounded single
`Microsoft.KeyVault/vaults` list page only (slice 521 decisions log D3 / D5 +
the spillover note).

This slice closes that gap: for an RBAC-mode vault, enumerate the per-vault
`Microsoft.Authorization/roleAssignments` scoped to the vault resource id
(principal object id + role definition name, e.g. `Key Vault Reader`,
`Key Vault Secrets User`) and populate the same `access_entries` list with
`principal_type=rbac_role_assignment` entries. This makes the least-privilege
read symmetric across both vault access models, completing the IAC-21
(privileged-account-management) signal for RBAC vaults.

slice-521 pattern verbatim: extend `connectors/azure/internal/keyvault` (the ARM
client gains a second read against the Authorization RP, scoped to each vault id;
the collector merges the role-assignment entries into the existing
`AccessEntries` list). NO schema change is required — the
`azure.keyvault_access_config.v1` schema already carries the discriminated
`access_entries[].principal_type` / `role_name` shape for exactly this case. Push
only (invariant #3); same ARM **Reader** role; honest interval.

## Scope discipline (the slice-521 over-collection guard, unchanged)

- Reads `Microsoft.Authorization/roleAssignments` METADATA only (principal id +
  role definition id/name) at the vault-resource scope — the SAME
  management-plane boundary slice 521 established.
- NEVER reads any secret/key/certificate VALUE and NEVER touches the Key-Vault
  data plane (`vault.azure.net`). The `keyvault.AccessEntry` struct still has no
  field for secret material — the slice-521 structural over-collection test
  continues to pin this.
- ARM **Reader** suffices (Reader can read role assignments at a resource scope);
  NO new Azure scope and NO Key-Vault data-plane role.

## Threat model

Inherits the slice-521 / slice-486 connector-family threat model verbatim.

- **S — Spoofing.** Reuses the existing connector push credential + read-only ARM
  **Reader**; credential stays source-side.
- **T — Tampering.** sha256 content-hash per record; ingest validates.
- **R — Repudiation.** register-per-run + stable `actor_id`
  (`connector:azure:keyvault@<version>`) + documented `observed_at` granularity.
- **I — Information disclosure.** Role-assignment METADATA only (principal +
  role name) — NEVER a secret/key/certificate value; never the data plane.
- **D — Denial of service.** Bounded per-vault role-assignment read + per-run
  cap + run timeout; a large estate needs the shared cursor-pagination follow-on
  (486 R3).
- **E — Elevation of privilege.** ARM **Reader** only — NEVER a Key-Vault
  data-plane role (the slice-521 over-privilege trap, P0-521-1, still applies).

## Acceptance criteria

- [ ] **AC-1.** For an RBAC-authorization vault, the `keyvault` collector reads
      the per-vault `Microsoft.Authorization/roleAssignments` via read-only ARM
      (Reader), faked in tests, and merges them into the `access_entries` list as
      `principal_type=rbac_role_assignment` entries (principal_id + role_name).
- [ ] **AC-2.** No schema change (the existing
      `azure.keyvault_access_config.v1` `access_entries` shape already supports
      RBAC entries); the schema-drift bijection still passes.
- [ ] **AC-3.** Each record pushes via the single `IngestEvidence` API — no wire
      change; sha256 content-hash; `profiles_supported=[pull]`.
- [ ] **AC-4.** The slice-521 structural + integration over-collection tests
      still hold (no secret/key/certificate value can enter a record); a test
      pins an RBAC vault now carries its role assignments.
- [ ] **AC-5.** README + decisions log + changelog updated; coverage floor for
      the `keyvault` package held or raised.

## Anti-criteria (P0 — block merge)

- **P0-615-1.** Does NOT require or document any Key-Vault data-plane permission.
- **P0-615-2.** Does NOT read secret / key / certificate values (management plane
  only).
- **P0-615-3.** Does NOT widen Azure permissions beyond the existing ARM Reader
  role.
- **P0-615-4.** Does NOT widen the platform-side wire (push only).

## Dependencies

- **#521** (Azure Key-Vault access-policy evidence) — `merged`. Parent slice;
  this is the RBAC role-assignment enumeration slice 521 deferred as out-of-scope
  (decisions log D3 / D5 + spillover).
