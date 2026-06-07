# 486 — Azure connector (Entra ID + Storage) — cloud parity with AWS/GCP

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `ready`

## Narrative

The v1 connector roster ships the 7 MVP connectors (`aws`, `github`, `okta`,
`1password`, `osquery`, `jira`, `manual`; canvas §10.1, `connectors/`), and the
roadmap names the cloud roster growing (`gcp` filed as slice 442). **Azure is
the third major cloud** and the last of the big-three a security-product startup
must cover for replacement-grade parity: a meaningful share of startups run on
Azure, and an even larger share use **Entra ID** (Azure AD) as their identity
provider even when their workloads run elsewhere. "Prove our Entra ID / Azure
RBAC + storage is locked down" is a recurring SOC 2 / ISO evidence demand the
platform cannot serve today without manual upload.

The connector pattern is locked by slice 004 (AWS exemplar), reused by slices
442 (GCP) and 443 (Slack), and codified in the `feedback_connector_patterns`
memory: stable `actor_id` format, stable optional fields, `observed_at`
granularity, register-per-run, scope minimums, vendor-native auth. This slice
ships **one vertical Azure connector** following that template: collect **Entra
ID** evidence (directory-role assignments / service-principal + app-registration
inventory) + **Azure Storage account** configuration evidence (encryption,
secure-transfer/TLS, public-blob-access state) via read-only Microsoft Graph +
Azure Resource Manager APIs, register `profiles_supported` per run, and `Push`
each record to the platform's single inbound `IngestEvidence` API.

**Scope discipline.** **One connector, two evidence surfaces** (Entra ID +
Storage), the minimum that demonstrates the Azure connector is a real
first-class peer. It does **not** ship AKS / Network-Security-Group / Key-Vault
/ Azure-Policy / Activity-Log evidence (follow-ons), does **not** ship a
subscribe/event-driven profile (pull-profile only in v0 — name the interval
honestly), and does **not** add any platform-side wire change (the wire is
always push — invariant #3). **Follow-on slices:** AKS workload config; NSG /
firewall evidence; Key-Vault access-policy evidence; event-driven profile via
Azure Event Grid / Activity-Log diagnostic settings.

## Threat model (STRIDE) — connector family (source-credential heavy)

A connector is a separate process holding **source-side credentials** (here, an
Entra ID app-registration / managed-identity with read access to the customer's
Azure tenant + subscriptions). The dominant risks are credential handling
(over-broad Graph/RBAC scopes, credential leakage) and ensuring the connector
emits only push to the platform (no inbound surface widening).

**S — Spoofing.** The connector authenticates TO the platform via its push
credential (the existing connector auth — OAuth client_credentials per slice 191) and TO Azure via an Entra app-registration / managed identity. Risk: a
stolen push credential impersonating the connector, or an Azure credential with
more than read scope.
**Mitigation:** push reuses the existing connector credential boundary (no new
auth scheme); Azure auth uses a **read-only** app-registration (least-privilege:
Graph `Directory.Read.All` + `Application.Read.All` application permissions, and
the ARM **Reader** role on the in-scope subscriptions), documented as the
required minimum. The Azure credential stays source-side; the platform never
sees it (invariant #3).

**T — Tampering.** Evidence records carry a sha256 content-hash; a tampered
record is detectable.
**Mitigation:** each pushed record is content-hashed (v1 evidence-integrity
primitive); the platform's ingest validates the hash. The connector does not
accept inbound data — it only reads Azure + pushes.

**R — Repudiation.** Which connector run produced which evidence must be
traceable.
**Mitigation:** register-per-run records the connector identity + run; each
evidence record carries a stable `actor_id` (the Azure connector + run context)
and `observed_at` at a documented granularity (slice 004 pattern).

**I — Information disclosure.** Entra ID directory data + storage config is
tenant-confidential. Risk: the connector logs raw credentials, collects user PII
beyond role-assignment facts, or reads blob/object contents rather than account
config.
**Mitigation:** the connector collects **configuration + role-assignment
metadata only** (directory-role + RBAC assignments, service-principal/app
inventory, storage-account encryption/TLS/public-access flags) — NOT blob
contents, NOT Key-Vault secret values, NOT user mailbox/profile PII beyond the
identity needed for an assignment record; credentials are never logged; the
Azure credential stays source-side and never enters an evidence record.

**D — Denial of service.** A large tenant (thousands of service principals,
many subscriptions/storage accounts) could make a run unbounded.
**Mitigation:** the connector paginates Graph + ARM reads with bounded page
sizes + a per-run cap; the pull profile runs on a named interval (honest, not
"continuous"); a run timeout caps a hung collection.

**E — Elevation of privilege.** Risk: the Entra app-registration is granted
broad write/admin permissions (e.g. `Directory.ReadWrite.All`, Owner role) "to
be safe."
**Mitigation:** the connector requires read-only Graph permissions + the ARM
Reader role only; the docs name the exact minimal permissions and explicitly
warn against broad grants (and against the over-privileged `Global Reader` vs
the narrower app-permission set where the narrower set suffices). The connector
has no platform-side privilege beyond push (invariant #3).

## Acceptance criteria

**Connector — collection**

- [ ] **AC-1.** A `connectors/azure/` connector lands following the slice-004 /
      442 template (register-per-run, stable `actor_id`, `observed_at`
      granularity, scope minimums).
- [ ] **AC-2.** It collects **Entra ID** evidence (directory-role assignments /
      service-principal + app-registration inventory) via read-only Microsoft
      Graph APIs.
- [ ] **AC-3.** It collects **Azure Storage account** configuration evidence
      (encryption, secure-transfer/TLS, public-blob-access state) via read-only
      Azure Resource Manager APIs.
- [ ] **AC-4.** It authenticates to Azure via vendor-native auth (Entra
      app-registration client-credentials / managed identity) and requires only
      read-only Graph permissions + the ARM Reader role, documented as the
      minimum.

**Connector — push**

- [ ] **AC-5.** Each collected record is pushed to the platform's single
      `IngestEvidence` (`Push`) API — no platform-side wire change (invariant #3).
- [ ] **AC-6.** Each record carries a sha256 content-hash + stable optional
      fields per the connector pattern.
- [ ] **AC-7.** The connector registers its `profiles_supported` (`pull` in v0)
      per run; the pull interval is named honestly.

**Evidence schema**

- [ ] **AC-8.** The Entra-ID + Azure-Storage evidence_kind schemas land in the
      schema-registry schemas tree with `x-default-scf-anchors` set, per the
      schema-governance rules (OQ #9).

**Tests**

- [ ] **AC-9.** Connector unit/integration tests cover the collect → push
      round-trip against a mocked Graph + ARM surface (the connector's own Azure
      client is faked; the push receipt is asserted).
- [ ] **AC-10.** A test asserts the connector emits ONLY config /
      role-assignment metadata (no blob contents / Key-Vault secret values / user
      PII beyond assignment identity).
- [ ] **AC-11.** A test asserts the connector never logs the Azure credential.

**Docs / JUDGMENT artifact**

- [ ] **AC-12.** A connector README documents the minimal read-only Azure
      permissions (Graph app permissions + ARM Reader), the pull interval, and
      the evidence kinds.
- [ ] **AC-13.** A decisions log
      (`docs/audit-log/486-azure-connector-decisions.md`) records the
      evidence-kind shape + scope-minimum + stable-field JUDGMENT calls.
- [ ] **AC-14.** A changelog entry.

## Constitutional invariants honored

- **#3 — Single canonical inbound API (`IngestEvidence` / `Push`).** First-class
  peer connector holding source-side credentials; the platform-side wire is
  push-only.
- **Licensing — no closed proprietary connectors.** OSS, in-tree,
  read-only-API-based (no proprietary agent).
- **Evidence integrity.** sha256 content-hash per record (v1 primitive).
- **Anti-pattern: honest intervals.** The pull profile names its interval; no
  "continuous monitoring" framing.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.1 — Evidence SDK, connectors,
  `profiles_supported`, push wire.
- `Plans/canvas/10-roadmap.md` §10.2 — connector roster grows (Azure named in
  the planned layout).
- `Plans/EVIDENCE_SDK.md` — full SDK contract incl. push profile.

## Dependencies

- **#003** (Evidence SDK proto + push client + CLI) — `merged`. The push surface.
- **#004** (AWS connector exemplar) — `merged`. The connector pattern template.
- **#191** (SDK OAuth client_credentials migration) — `merged`. Connector push
  credential.
- **#442** (GCP connector) — sibling cloud connector; NOT a hard dep (both follow
  the slice-004 template independently).

## Anti-criteria (P0 — block merge)

- **P0-486-1.** Does NOT widen the platform-side wire — push only (invariant #3).
- **P0-486-2.** Does NOT require or document broad/write Azure permissions —
  read-only least-privilege only (threat-model E).
- **P0-486-3.** Does NOT collect blob contents / Key-Vault secret values / user
  PII beyond assignment identity — config + role-assignment metadata only
  (threat-model I).
- **P0-486-4.** Does NOT log or transmit the Azure credential into the platform.
- **P0-486-5.** Does NOT ship a closed/proprietary collector — OSS, read-only
  API (licensing).
- **P0-486-6.** Does NOT label the pull profile "continuous monitoring" — honest
  interval.
- **P0-486-7.** Does NOT implement AKS / NSG / Key-Vault / Azure-Policy evidence
  — follow-ons.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (collect→push round-trip; no live Azure in CI) ·
`security-review` (source-credential handling + scope minimums) · `simplify` ·
`changelog-generator`.

## Notes for the implementing agent

- **Phase-2 grill output:** the connector is the slice-004 / 442 pattern
  verbatim — the work is Graph/ARM-API-specific collection + the two
  evidence-kind schemas, not re-inventing the connector framework. Mirror the
  AWS + GCP connector structure.
- **JUDGMENT calls you own:** the exact evidence-kind field shapes, the
  `x-default-scf-anchors` per kind, and the scope minimum. Record in the
  decisions log; the maintainer re-checks anchor accuracy (OQ #9 load-bearing).
- The Graph-permission vs ARM-role split is the subtle scope-minimum call:
  identity evidence needs Graph app permissions, storage evidence needs the ARM
  Reader role — document both and the narrowest sufficient set for each.
- Reuse `feedback_connector_patterns`: stable actor_id, stable optional fields,
  observed_at granularity, register-per-run, scope minimums, vendor-native auth.
- Detection-tier: `none` unless a bug surfaces during the build.
