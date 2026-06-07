# 521 — Azure connector: Key-Vault access-policy / RBAC evidence

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `blocked` (depends on #486 — base Azure connector — merged first)

## Narrative

Slice 486 shipped the base Azure connector (Entra ID + Storage). This slice adds
**Azure Key-Vault access-policy / RBAC evidence** — per-vault configuration: the
access model (legacy access policies vs Azure RBAC), purge-protection +
soft-delete state, public-network-access posture, and the access-policy /
role-assignment principals entitled to the vault — read read-only via Azure
Resource Manager (ARM Reader). "Prove our secret stores enforce purge protection
and least-privilege access" is a recurring SOC 2 / ISO key-management evidence
demand.

**The load-bearing scope discipline (P0):** this slice reads Key-Vault
**configuration + access-policy metadata ONLY**. It NEVER reads secret/key/
certificate VALUES (the Key-Vault _data plane_). The connector touches only the
ARM _management plane_ (vault properties + access policies), never the data-plane
`secrets.get` / `keys.get` operations. This is the same boundary slice 486's
P0-486-3 established for Storage (config, not blob contents).

slice-486 pattern verbatim: a new `internal/keyvault` collector + a new
`azure.keyvault_access_config.v1` evidence kind + schema with
`x-default-scf-anchors`, in `DefaultSeed`, faked ARM surface in tests. Push only
(invariant #3); `profiles_supported=[pull]`; honest interval.

## Threat model

Inherits the slice-486 connector-family threat model, with an extra-sharp
information-disclosure edge because Key-Vault is a secret store.

- **S — Spoofing.** Reuses the existing connector push credential + the read-only
  ARM **Reader** role; credential stays source-side.
- **T — Tampering.** sha256 content-hash per record; ingest validates.
- **R — Repudiation.** register-per-run + stable `actor_id`
  (`connector:azure:keyvault@<version>`) + documented `observed_at` granularity.
- **I — Information disclosure (DOMINANT).** Management-plane CONFIGURATION +
  access-policy metadata ONLY. The connector MUST NOT be granted any Key-Vault
  data-plane permission (`Key Vault Secrets User`, `Key Vault Crypto User`, etc.)
  and MUST NOT call any data-plane read. A test asserts no secret/key/certificate
  value can enter an evidence record.
- **D — Denial of service.** Bounded ARM list page + per-run cap + run timeout.
- **E — Elevation of privilege.** ARM **Reader** only — NEVER a data-plane role.
  The docs explicitly warn that granting a data-plane secrets/keys role to "read
  the vault" is the over-privilege trap.

## Acceptance criteria

- [ ] **AC-1.** A new `internal/keyvault` collector reads vault management-plane
      config + access policies via read-only ARM (Reader), faked in tests.
- [ ] **AC-2.** A new `azure.keyvault_access_config.v1` evidence kind + JSON
      Schema with `x-default-scf-anchors` lands in the schema-registry tree +
      `DefaultSeed`.
- [ ] **AC-3.** Each record pushes via the single `IngestEvidence` API — no wire
      change; sha256 content-hash; `profiles_supported=[pull]`.
- [ ] **AC-4.** A test asserts NO secret / key / certificate VALUE can enter a
      record (management-plane metadata only).
- [ ] **AC-5.** README documents the ARM-Reader-only requirement and explicitly
      warns against granting any data-plane Key-Vault role; decisions log +
      changelog updated.

## Anti-criteria (P0 — block merge)

- **P0-521-1.** Does NOT require or document any Key-Vault data-plane permission.
- **P0-521-2.** Does NOT read secret / key / certificate values (management plane
  only).
- **P0-521-3.** Does NOT widen Azure permissions beyond the existing ARM Reader
  role.
- **P0-521-4.** Does NOT widen the platform-side wire (push only).

## Dependencies

- **#486** (base Azure connector) — `merged`.
