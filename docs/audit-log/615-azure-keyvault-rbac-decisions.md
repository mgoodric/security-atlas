# 615 — Azure Key-Vault RBAC role-assignment enumeration: JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls for
slice 615 — the read-shape choice (how the per-vault role assignments were
scoped + where the second read lives), the role-name-resolution scope-minimum,
and THE load-bearing call carried over from slice 521 (the structural
over-collection boundary that keeps every secret / key / certificate VALUE out
of the record, now extended to the RBAC path). It does NOT block merge; the
maintainer iterates post-deployment from the "Revisit once in use" list.

Parent: slice 521 (`docs/audit-log/521-azure-keyvault-decisions.md`). This slice
closes the gap slice 521 deferred (521 D3/D5 + spillover): for an RBAC vault,
521 reported the access **model** flag but did not enumerate the per-vault
`Microsoft.Authorization/roleAssignments`.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** unit. The one build-time correction that surfaced
  during the slice — the package coverage dipping to 88.9% (below the 90 floor)
  after the new error-path branches landed — was caught at the unit tier by
  `go test -cover` before push, and fixed in the SAME change by adding the
  missing branch tests (roleAssignments decode error, roleDefinitions
  HTTP-error / bad-json fallback, per-vault truncation, missing-principal skip,
  empty-roleDefinitionId, cache-hit). No product-behavior bug surfaced.
- **detection_tier_target:** unit. A coverage-floor regression is exactly the
  class of issue the per-package unit gate exists to catch; it was caught there.

There were no fix-forward defects: the second read is a self-contained extension
of `Client.ListVaults` with no seam / collector / wire / schema change, so the
existing `cmd` seam + integration tests passed unmodified (they already carried
`rbac_role_assignment` fixtures from slice 521).

## Decisions made

### D1 — Read shape: the second read lives INSIDE `Client.ListVaults`, not in `Inspect` or a new seam method

- **Options:** (a) issue the roleAssignments read inside the concrete
  `Client.ListVaults` and merge the entries into each `RawVault.AccessEntries`
  before returning; (b) widen the `keyvault.API` interface with a second method
  (`ListVaultRoleAssignments`) that `Inspect` calls per RBAC vault; (c) do the
  merge in the `cmd` collector layer.
- **Chosen:** (a). An RBAC vault's principals come from roleAssignments exactly
  as a legacy vault's come from `accessPolicies` — both are management-plane
  access metadata for the same vault, fetched by the same client during the same
  list. Populating `RawVault.AccessEntries` from both sources inside
  `ListVaults` makes the two access models **symmetric** at the seam: `Inspect`,
  the `keyvault.API` interface, the `cmd` seam fakes, `buildKeyVaultRecord`, and
  `keyVaultAccessPayload` are all byte-for-byte unchanged. The slice-521 fakes
  (`fakeARM`, `fakeKeyVaultForIntegration`) already return `RawVault` with
  `AccessEntries` populated, so the test surface that exercises `Inspect` /
  `cmd` continues to work without touching them.
- **Rejected (b):** widening the interface would force every fake + the
  collector + the seam harness to learn a second call, for no behavioral gain —
  the roleAssignments are an implementation detail of "how the ARM client
  resolves an RBAC vault's principals", not a separate collector concept.
- **Rejected (c):** the `cmd` layer has no ARM client and no business issuing a
  second Azure read; the management-plane read belongs in the client.

### D2 — Over-collection guard mechanism: the slice-521 structural pin, unchanged, extended to the new path

- **Mechanism:** the guard is **structural, by construction** — the
  `keyvault.AccessEntry` struct has NO field capable of holding a secret / key /
  certificate VALUE. The RBAC path writes only `PrincipalID` (a GUID),
  `PrincipalType="rbac_role_assignment"`, and `RoleName` (a human-readable role
  name like "Key Vault Reader"). There is physically nowhere for secret material
  to land, so the RBAC path cannot over-collect even if a future ARM field
  exposed a value.
- **Tests pinning it:**
  - the slice-521 reflection test
    `TestStructs_MetadataOnly_NoSecretValueFields` already reflects over
    `AccessEntry` / `VaultConfig` / `RawVault` and fails the build on any field
    name hinting at secret material (`value`, `secret`, `token`, …) — `RoleName`
    / `PrincipalID` / `PrincipalType` trip none of the banned tokens;
  - new `TestRBACRoleAssignment_MetadataOnly_PinsNewPath` pins the PATH that now
    populates the struct: an `rbac_role_assignment` entry carries a principal id
    plus a role name plus NO permission verbs, exercised end-to-end through
    `Inspect`;
  - the client test `TestClient_ListVaults_RBACMergesRoleAssignments` asserts the
    connector never issues a roleAssignments call for a legacy vault and never
    touches the data plane (`vault.azure.net`);
  - the slice-521 integration over-collection test (descends into
    `access_entries` applying the `kvAccessAllowed` allow-list, which already
    contains `role_name`) continues to hold.
- The read targets ARM **Reader** only (`roleAssignments` + `roleDefinitions`
  are readable at a resource scope by Reader) — NO new Azure scope, NO
  data-plane role (P0-615-2 / P0-615-3).

### D3 — Scope-minimum: role-NAME resolution via a bounded, cached `roleDefinitions` lookup, guid fallback

- **The problem:** ARM `roleAssignments` return `properties.roleDefinitionId` (a
  `.../roleDefinitions/<guid>` resource id), NOT the friendly name. The slice
  asks for the role **name** (e.g. "Key Vault Reader", "Key Vault Secrets User").
- **Options:** (a) emit the bare role-definition guid and stop; (b) resolve each
  guid to its `roleName` via a second read-only `roleDefinitions` GET, cached
  per run so each distinct guid is fetched at most once; (c) bundle a static
  guid→name table for the well-known built-in Key-Vault roles.
- **Chosen:** (b) with (a) as the fallback. A per-run `map[guid]name` cache means
  N assignments sharing a role definition cost ONE roleDefinitions lookup
  (pinned by `TestClient_ListVaults_RoleNameCachedAcrossAssignments`). When the
  lookup fails (HTTP error, bad JSON, empty name) or the per-run lookup cap is
  reached, the entry falls back to the bare guid — the evidence still names the
  role unambiguously by its stable id, just not by its display name. Reader can
  read roleDefinitions, so this needs NO additional scope.
- **Rejected (a):** a bare guid is opaque to an auditor reading the board pack;
  the friendly name is the whole point of the least-privilege signal.
- **Rejected (c):** a bundled table goes stale as Azure adds roles and silently
  mis-labels custom roles; the live lookup is authoritative and the guid
  fallback already handles the unresolvable case.

### D4 — DoS guard: bounded per-vault read + per-run cap + lookup cap; pagination deferred

- The per-vault roleAssignments read is a single bounded page truncated to
  `maxRoleAssignmentsPerVault` (200); the run-wide total is capped at
  `maxRoleAssignmentsPerRun` (2000); distinct role-definition lookups are capped
  at `maxRoleDefinitionLookupsPerRun` (100). The existing HTTP client timeout
  bounds wall-clock. This is the slice-521 / 486-R3 threat-model-D posture
  carried verbatim — a bounded first read, never an unbounded cursor walk.
- Cursor pagination for a vault whose role-assignment count legitimately exceeds
  the cap is OUT OF SCOPE here and filed as **spillover slice 623** (the shared
  cursor-pagination follow-on already noted by 486 R3 / 521). A truncated read is
  honest (the README documents the bound) and never blocks the rest of the run.

### D5 — Fail-soft on a per-vault read error → INCONCLUSIVE, not a run failure

- A roleAssignments read that errors (e.g. a throttled 429 / 403 on one vault)
  sets `RawVault.ReadError`, which the existing `verdict()` path renders as
  INCONCLUSIVE for that vault. The run continues; the other vaults still report.
  This mirrors the slice-521 contract — one bad vault must not blind the
  connector to the estate. Pinned by
  `TestClient_ListVaults_RoleAssignmentReadErrorMarksInconclusive`.

## Revisit once in use

- **Cursor pagination (spillover 623).** Wire the shared ARM cursor-pagination
  follow-on through the roleAssignments read once an operator hits the per-vault
  cap on a real estate.
- **`$expand=roleDefinition` on the roleAssignments call.** Some ARM API
  versions can expand the role definition inline, which would eliminate the
  per-guid second lookup entirely. Deferred — the cached two-call shape is
  simpler to reason about and the cache already collapses the cost to one lookup
  per distinct role; revisit if the lookup count ever shows up in profiling.
