# 442 — GCP connector (highest-demand missing cloud)

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `ready`

## Narrative

The v1 connector roster ships the 7 MVP connectors: `aws`, `github`, `okta`,
`1password`, `osquery`, `jira`, `manual` (canvas §10.1; `connectors/`). Roadmap
§10.2 names GCP as the next cloud to add. For the slice's persona — a SaaS
startup security leader — GCP is the highest-demand missing cloud after AWS:
many startups run on GCP, and "prove our GCP IAM + storage is locked down" is a
recurring SOC 2 / ISO evidence demand the platform currently can't serve
without manual upload.

The connector pattern is locked by slice 004 (AWS exemplar) and the
`feedback_connector_patterns` memory: stable `actor_id` format, stable optional
fields, `observed_at` granularity, register-per-run, scope minimums,
vendor-native auth. This slice ships **one vertical GCP connector** following
that template: collect GCP **IAM** evidence (policy bindings / service-account
inventory) + **Cloud Storage** bucket configuration evidence (encryption,
logging, public-access state) via read-only GCP APIs, register the connector's
`profiles_supported` per run, and `Push` each record to the platform's single
inbound `IngestEvidence` API.

**Scope discipline.** **One connector, two evidence surfaces** (IAM + Cloud
Storage), the minimum that demonstrates the GCP connector is a real first-class
peer. It does **not** ship GKE / Cloud Logging / VPC / org-policy evidence
(follow-ons), does **not** ship a subscribe/event-driven profile (pull-profile
only in v0 — name the interval honestly), and does **not** add any
platform-side wire change (the wire is always push — invariant #3). **Follow-on
slices:** GKE config evidence; org-policy / VPC firewall evidence; event-driven
profile via GCP audit-log sink.

## Threat model (STRIDE) — connector family (source-credential heavy)

A connector is a separate process holding **source-side credentials** (here, a
GCP service-account / ADC identity with read access to the customer's GCP org).
The dominant risks are credential handling (over-broad scopes, credential
leakage) and ensuring the connector emits only push to the platform (no inbound
surface widening).

**S — Spoofing.** The connector authenticates TO the platform via its
push credential (the existing connector auth — API key / OAuth client_credentials
per slice 191) and TO GCP via a service-account/ADC identity. Risk: a stolen
push credential impersonating the connector, or a GCP credential with more than
read scope.
**Mitigation:** push uses the existing connector credential boundary (no new auth
scheme); GCP auth uses a **read-only** service account (least-privilege roles:
`roles/iam.securityReviewer` + `roles/storage.objectViewer`-class), documented
as the required minimum. The connector holds the GCP credential source-side;
the platform never sees it (invariant #3).

**T — Tampering.** Evidence records carry a sha256 content-hash; a tampered
record is detectable.
**Mitigation:** each pushed record is content-hashed (v1 evidence-integrity
primitive); the platform's ingest validates the hash. The connector does not
accept inbound data — it only reads GCP + pushes.

**R — Repudiation.** Which connector run produced which evidence must be
traceable.
**Mitigation:** register-per-run records the connector identity + run; each
evidence record carries a stable `actor_id` (the GCP connector + run context)
and `observed_at` at a documented granularity (slice 004 pattern).

**I — Information disclosure.** GCP IAM + bucket config is tenant-confidential.
Risk: the connector logs raw credentials or over-collects (e.g. bucket object
contents rather than config).
**Mitigation:** the connector collects **configuration metadata only** (IAM
bindings, bucket encryption/logging/public-access flags) — NOT object contents,
NOT secret values; credentials are never logged; the GCP credential stays
source-side and never enters an evidence record.

**D — Denial of service.** A GCP org with thousands of buckets / service
accounts could make a run unbounded.
**Mitigation:** the connector paginates GCP API reads with bounded page sizes +
a per-run cap; the pull profile runs on a named interval (honest, not
"continuous"); a run timeout caps a hung collection.

**E — Elevation of privilege.** Risk: the GCP service account is granted broad
write/admin roles "to be safe."
**Mitigation:** the connector requires read-only roles only; the docs name the
exact minimal roles and explicitly warn against broad grants. The connector has
no platform-side privilege beyond push (invariant #3).

## Acceptance criteria

**Connector — collection**

- [ ] **AC-1.** A `connectors/gcp/` connector lands following the slice-004
      template (register-per-run, stable `actor_id`, `observed_at` granularity,
      scope minimums).
- [ ] **AC-2.** It collects GCP **IAM** evidence (policy bindings / service-
      account inventory) via read-only GCP APIs.
- [ ] **AC-3.** It collects Cloud Storage **bucket configuration** evidence
      (encryption, logging, public-access state) via read-only GCP APIs.
- [ ] **AC-4.** It authenticates to GCP via vendor-native auth (ADC /
      service-account key) and requires only read-only roles, documented as the
      minimum.

**Connector — push**

- [ ] **AC-5.** Each collected record is pushed to the platform's single
      `IngestEvidence` (`Push`) API — no platform-side wire change (invariant #3).
- [ ] **AC-6.** Each record carries a sha256 content-hash + stable optional
      fields per the connector pattern.
- [ ] **AC-7.** The connector registers its `profiles_supported` (`pull` in v0)
      per run; the pull interval is named honestly.

**Evidence schema**

- [ ] **AC-8.** The GCP IAM + Cloud Storage evidence_kind schemas land in
      `internal/api/schemaregistry/schemas/` (or the schemas tree) with
      `x-default-scf-anchors` set, per the schema-governance rules (OQ #9).

**Tests**

- [ ] **AC-9.** Connector unit/integration tests cover the collect → push
      round-trip against a mocked GCP API surface (the connector's own GCP
      client is faked; the push receipt is asserted).
- [ ] **AC-10.** A test asserts the connector emits ONLY config metadata (no
      object contents / secret values / credentials).
- [ ] **AC-11.** A test asserts the connector never logs the GCP credential.

**Docs / JUDGMENT artifact**

- [ ] **AC-12.** A connector README documents the minimal read-only GCP roles,
      the pull interval, and the evidence kinds.
- [ ] **AC-13.** A decisions log
      (`docs/audit-log/442-gcp-connector-decisions.md`) records the
      evidence-kind shape + scope-minimum + stable-field JUDGMENT calls.
- [ ] **AC-14.** A changelog entry.

## Constitutional invariants honored

- **#3 — Single canonical inbound API (`IngestEvidence` / `Push`).** The
  connector is a first-class peer holding source-side credentials and emitting
  via push; the platform-side wire is push-only.
- **Licensing — no closed proprietary connectors.** The GCP connector is OSS,
  in-tree, read-only-API-based (no proprietary agent).
- **Evidence integrity.** sha256 content-hash per record (v1 primitive).
- **Anti-pattern: honest intervals.** The pull profile names its interval; no
  "continuous monitoring" framing.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.1 — Evidence SDK, connectors,
  `profiles_supported`, push wire.
- `Plans/canvas/10-roadmap.md` §10.2 — connector roster grows (GCP named).
- `Plans/EVIDENCE_SDK.md` — full SDK contract incl. push profile.

## Dependencies

- **#003** (Evidence SDK proto + push client + CLI) — `merged`. The push
  surface.
- **#004** (AWS connector exemplar) — `merged`. The connector pattern template.
- **#191** (SDK OAuth client_credentials migration) — `merged`. Connector push
  credential.

## Anti-criteria (P0 — block merge)

- **P0-442-1.** Does NOT widen the platform-side wire — push only (invariant #3).
- **P0-442-2.** Does NOT require or document broad/write GCP roles — read-only
  least-privilege only (threat-model E).
- **P0-442-3.** Does NOT collect object contents / secret values — config
  metadata only (threat-model I).
- **P0-442-4.** Does NOT log or transmit the GCP credential into the platform.
- **P0-442-5.** Does NOT ship a closed/proprietary collector — OSS, read-only
  API (licensing).
- **P0-442-6.** Does NOT label the pull profile "continuous monitoring" — honest
  interval.
- **P0-442-7.** Does NOT implement GKE / VPC / org-policy evidence — follow-ons.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (collect→push round-trip; no live GCP in CI) ·
`security-review` (source-credential handling + scope minimums) · `simplify` ·
`changelog-generator`.

## Notes for the implementing agent

- **Phase-2 grill output:** the connector is the slice-004 pattern verbatim —
  the work is GCP-API-specific collection + the two evidence-kind schemas, not
  re-inventing the connector framework. Mirror the AWS connector's structure.
- **JUDGMENT calls you own:** the exact evidence-kind field shapes, the
  `x-default-scf-anchors` per kind, and the scope minimum. Record in the
  decisions log; the maintainer re-checks anchor accuracy (OQ #9 load-bearing).
- Reuse the `feedback_connector_patterns` conventions: stable actor_id, stable
  optional fields, observed_at granularity, register-per-run, scope minimums,
  vendor-native auth.
- Detection-tier: `none` unless a bug surfaces during the build.
