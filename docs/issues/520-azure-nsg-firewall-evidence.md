# 520 — Azure connector: NSG / firewall rule evidence

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `blocked` (depends on #486 — base Azure connector — merged first)

## Narrative

Slice 486 shipped the base Azure connector (Entra ID + Storage). This slice adds
**Network Security Group (NSG) / Azure Firewall rule evidence** — the inbound /
outbound security-rule posture of NSGs (and, optionally, Azure Firewall policy
rule collections) read read-only via Azure Resource Manager (the existing ARM
Reader role). "Prove no NSG allows 0.0.0.0/0 inbound on management ports" is a
recurring SOC 2 / PCI network-segmentation evidence demand.

slice-486 pattern verbatim: a new `internal/nsg` collector + a new
`azure.nsg_rule.v1` evidence kind + schema with `x-default-scf-anchors`,
registered in `DefaultSeed`, faked ARM surface in tests. Push only (invariant
#3); `profiles_supported=[pull]`; honest interval.

**Scope discipline.** NSG security rules + (optional) Azure Firewall network/app
rule collections only. NOT route tables, NOT VNet peering topology, NOT
Key-Vault / Azure-Policy evidence (sibling follow-ons).

## Threat model

Inherits the slice-486 connector-family threat model verbatim.

- **S — Spoofing.** Reuses the existing connector push credential + the read-only
  ARM **Reader** role; credential stays source-side.
- **T — Tampering.** sha256 content-hash per record; ingest validates.
- **R — Repudiation.** register-per-run + stable `actor_id`
  (`connector:azure:nsg@<version>`) + documented `observed_at` granularity.
- **I — Information disclosure.** NSG/firewall RULE configuration only — never
  packet captures, flow logs, or traffic contents.
- **D — Denial of service.** Bounded ARM list page + per-run cap + run timeout; a
  large estate needs the cursor-pagination follow-on (shared with 486 R3).
- **E — Elevation of privilege.** ARM **Reader** only — no Network Contributor,
  no rule mutation (read-only list of rules).

## Acceptance criteria

- [ ] **AC-1.** A new `internal/nsg` collector reads NSG security rules (and
      optionally Azure Firewall rule collections) via read-only ARM, faked in
      tests.
- [ ] **AC-2.** A new `azure.nsg_rule.v1` evidence kind + JSON Schema with
      `x-default-scf-anchors` lands in the schema-registry tree + `DefaultSeed`.
- [ ] **AC-3.** Each record pushes via the single `IngestEvidence` API — no wire
      change; sha256 content-hash; `profiles_supported=[pull]`.
- [ ] **AC-4.** A test asserts the connector reads rule configuration ONLY (no
      flow logs / packet data).
- [ ] **AC-5.** README + decisions log + changelog updated.

## Anti-criteria (P0 — block merge)

- **P0-520-1.** Does NOT widen Azure permissions beyond the existing ARM Reader
  role.
- **P0-520-2.** Does NOT read flow logs / packet captures / traffic contents.
- **P0-520-3.** Does NOT mutate any network resource (read-only).
- **P0-520-4.** Does NOT widen the platform-side wire (push only).

## Dependencies

- **#486** (base Azure connector) — `merged`.
