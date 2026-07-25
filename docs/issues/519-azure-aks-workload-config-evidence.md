# 519 — Azure connector: AKS workload configuration evidence

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `blocked` (depends on #486 — base Azure connector — merged first)

## Narrative

Slice 486 shipped the base Azure connector with two evidence surfaces (Entra ID
role assignments + Azure Storage account configuration), deliberately scoped to
the minimum that proves the connector is a first-class peer. This slice adds a
third evidence surface: **Azure Kubernetes Service (AKS) workload configuration**
— cluster hardening posture (RBAC enabled, private cluster, network policy,
authorized IP ranges, OS disk encryption, managed-identity vs service-principal)
read read-only via Azure Resource Manager (the same ARM Reader role the storage
kind uses).

This is the slice-486 pattern verbatim: a new `internal/aks` collector + a new
`azure.aks_cluster_config.v1` evidence kind + its schema with
`x-default-scf-anchors`, registered in `DefaultSeed`, faked ARM surface in tests.
No platform-side wire change (invariant #3 — push only); `profiles_supported`
stays `[pull]`; the interval stays honestly named.

**Scope discipline.** Cluster-level configuration only. It does NOT read
workload/pod manifests, secrets, or container images (that is osquery / a
Kubernetes-native connector's job). It does NOT add NSG / Key-Vault / Azure-Policy
evidence (sibling follow-ons 520 / 521).

## Threat model

Inherits the slice-486 connector-family threat model verbatim: a separate process
holding source-side ARM Reader credentials, emitting push-only to the platform.

- **S — Spoofing.** Reuses the existing connector push credential; Azure auth
  uses the same read-only ARM **Reader** role (no new scope). The credential
  stays source-side.
- **T — Tampering.** Each pushed record carries a sha256 content-hash; the ingest
  validates it.
- **R — Repudiation.** register-per-run + stable `actor_id`
  (`connector:azure:aks@<version>`) + documented `observed_at` granularity.
- **I — Information disclosure.** Cluster CONFIGURATION metadata only — never
  workload manifests, secrets, kubeconfig credentials, or container contents.
- **D — Denial of service.** Bounded ARM list page + per-run cap + run timeout
  (the slice-486 pattern); a large fleet of clusters needs the cursor-pagination
  follow-on (shared with 486 R3).
- **E — Elevation of privilege.** ARM **Reader** only — no Contributor, no
  cluster-admin kubeconfig retrieval (`listClusterAdminCredential` is explicitly
  NOT called; it returns admin kubeconfig and is a privilege escalation).

## Acceptance criteria

- [ ] **AC-1.** A new `internal/aks` collector reads AKS cluster configuration
      via read-only ARM (Reader role), faked in tests.
- [ ] **AC-2.** A new `azure.aks_cluster_config.v1` evidence kind + JSON Schema
      with `x-default-scf-anchors` lands in the schema-registry tree and
      `DefaultSeed`.
- [ ] **AC-3.** Each record pushes via the single `IngestEvidence` API — no wire
      change (invariant #3); sha256 content-hash; `profiles_supported=[pull]`.
- [ ] **AC-4.** A test asserts the connector reads cluster config metadata ONLY
      (no admin kubeconfig, no secrets, no workload manifests).
- [ ] **AC-5.** README + decisions log + changelog updated.

## Anti-criteria (P0 — block merge)

- **P0-519-1.** Does NOT call `listClusterAdminCredential` or any API returning
  admin kubeconfig / cluster-admin credentials (privilege escalation).
- **P0-519-2.** Does NOT widen Azure permissions beyond the existing ARM Reader
  role.
- **P0-519-3.** Does NOT read workload manifests, secrets, or container contents.
- **P0-519-4.** Does NOT widen the platform-side wire (push only).

## Dependencies

- **#486** (base Azure connector) — `merged`. The connector pattern + auth +
  schema-registration plumbing.
