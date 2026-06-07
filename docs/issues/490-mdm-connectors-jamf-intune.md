# 490 — MDM connectors (Jamf + Intune) — endpoint posture evidence

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `ready`

## Narrative

The v1 connector roster ships the 7 MVP connectors (`aws`, `github`, `okta`,
`1password`, `osquery`, `jira`, `manual`; canvas §10.1, `connectors/`); the
planned layout (`CLAUDE.md`, "Planned repository layout") names `jamf` and
`intune` in the endpoint/MDM tier. For the platform's persona — a SaaS startup
security leader — **endpoint posture is a recurring SOC 2 CC6.7 / CC6.8 and ISO
A.8 evidence demand**: "prove laptops are disk-encrypted, have a screen-lock
policy, and are running endpoint protection." `osquery`/Fleet (already shipped)
covers fleet-managed posture, but most startups manage Macs with **Jamf** and
Windows/cross-platform with **Microsoft Intune**, and those MDMs are the system
of record auditors ask for. Today that is a manual export.

This slice clusters **two closely-related MDM connectors** — Jamf and Intune —
because they answer the same control question (managed endpoints meet the posture
baseline) and share an identical device-posture evidence shape, so one slice
keeps each a tracer-bullet while proving the endpoint-posture evidence-kind
family. Both follow the slice-004 / 442 connector template (stable `actor_id`,
stable optional fields, `observed_at` granularity, register-per-run, scope
minimums, vendor-native auth; `feedback_connector_patterns`). Consistent with
the "no proprietary collector agents on endpoints" anti-pattern, **these are
API-based connectors reading the MDM's management API** — the MDM agent on the
device is the customer's existing first-party MDM, not a security-atlas agent.

- **`connectors/jamf/`** collects **managed-device posture** (FileVault/disk-
  encryption state, screen-lock/passcode policy compliance, OS version,
  supervised/managed state) via the read-only Jamf Pro API.
- **`connectors/intune/`** collects **managed-device compliance posture**
  (BitLocker/disk-encryption state, compliance-policy result, OS version,
  enrollment state) via the read-only Microsoft Graph / Intune device-management
  API.

Both register `profiles_supported` per run and `Push` each record to the single
`IngestEvidence` API.

**Scope discipline.** **Two connectors, one evidence surface each** (device
posture/compliance summary), the minimum that demonstrates the endpoint-posture
evidence family is a real first-class peer set. Neither ships **installed-app
inventory / device geolocation / user-content / remote-action capability**
(deliberately — posture summary only, and the connector is read-only), neither
ships a webhook/event-driven profile (pull-profile only in v0 — honest interval),
and neither changes the platform-side wire (push-only — invariant #3).
**Follow-on slices:** software-inventory evidence; configuration-profile
detail evidence; event-driven profile via MDM compliance-state change webhooks.

## Threat model (STRIDE) — connector family (source-credential heavy)

Each connector is a separate process holding **source-side credentials** (a Jamf
Pro API client / an Entra app-registration with Intune Graph read permission).
The dominant risks are credential handling (over-broad MDM scopes, credential
leakage), over-collection (MDM holds device-owner PII + location + app
inventory), and keeping the platform wire push-only. **MDM is especially
sensitive** because a write-capable MDM credential can remote-wipe or push
configuration to employee endpoints — so the read-only constraint is
load-bearing.

**S — Spoofing.** Each connector authenticates TO the platform via its push
credential (the existing connector auth — OAuth client_credentials per slice 191) and TO its MDM via the vendor credential. Risk: a stolen push credential,
or an MDM credential with management/write scope.
**Mitigation:** push reuses the existing connector credential boundary; Jamf auth
uses an API role/client with **read-only** privileges (device-read only), Intune
auth uses an Entra app with **read-only** Graph permission
(`DeviceManagementManagedDevices.Read.All`) — documented as the required
minimum. Credentials stay source-side; the platform never sees them (invariant
#3).

**T — Tampering.** Evidence records carry a sha256 content-hash.
**Mitigation:** each pushed record is content-hashed (v1 evidence-integrity
primitive); ingest validates the hash. The connectors do not accept inbound
data — they only read the MDM + push.

**R — Repudiation.** Which connector run produced which evidence must be
traceable.
**Mitigation:** register-per-run records the connector identity + run; each
record carries a stable `actor_id` (the jamf/intune connector + run context) and
`observed_at` at a documented granularity (slice 004 pattern).

**I — Information disclosure.** MDM holds device-owner PII, device location, and
full installed-app inventory. Risk: the connector copies device geolocation,
owner personal detail beyond the assignment identity, or full app inventory into
an evidence record, or logs the MDM credential.
**Mitigation:** the connectors collect **posture/compliance summary** — disk-
encryption state, screen-lock/passcode-policy compliance, OS version,
enrollment/compliance result, and the device→owner _assignment identity_ needed
to attribute the device — NOT geolocation, NOT full app inventory, NOT owner
personal contact detail. A test asserts no geolocation / app-inventory / owner
PII-beyond-assignment enters an evidence record. Credentials are never logged.

**D — Denial of service.** A large fleet (thousands of devices) could make a run
unbounded.
**Mitigation:** paginated MDM-API reads with bounded page sizes + a per-run cap;
pull on a named interval (honest, not "continuous"); run timeout.

**E — Elevation of privilege (PRIMARY for MDM).** Risk: the MDM credential is
granted management/write scope "to be safe," giving the connector remote-wipe /
config-push capability over employee endpoints.
**Mitigation:** the connectors require read-only device-read scope only; the docs
name the exact minimal scope per MDM and explicitly warn that a write/management
MDM credential is a remote-wipe risk and must never be used. No platform-side
privilege beyond push (invariant #3).

## Acceptance criteria

**Connectors — collection**

- [ ] **AC-1.** `connectors/jamf/` and `connectors/intune/` connectors land,
      each following the slice-004 / 442 template (register-per-run, stable
      `actor_id`, `observed_at` granularity, scope minimums).
- [ ] **AC-2.** Jamf collects **managed-device posture** (disk-encryption state,
      screen-lock/passcode compliance, OS version, managed state) via the
      read-only Jamf Pro API.
- [ ] **AC-3.** Intune collects **managed-device compliance posture** (disk-
      encryption state, compliance-policy result, OS version, enrollment state)
      via the read-only Microsoft Graph / Intune API.
- [ ] **AC-4.** Each authenticates via vendor-native read-only auth (Jamf
      read-only API role; Intune Entra app with read-only Graph permission),
      documented as the minimum, with an explicit warning against
      management/write scope.

**Connectors — push**

- [ ] **AC-5.** Each collected record is pushed to the single `IngestEvidence`
      (`Push`) API — no platform-side wire change (invariant #3).
- [ ] **AC-6.** Each record carries a sha256 content-hash + stable optional
      fields.
- [ ] **AC-7.** Each connector registers `profiles_supported` (`pull` in v0) per
      run; the pull interval is named honestly.

**Evidence schema**

- [ ] **AC-8.** A shared endpoint-posture evidence_kind (or one per connector)
      lands in the schema-registry schemas tree with `x-default-scf-anchors` set
      (OQ #9). Reuse a common shape across the two connectors where the field set
      is identical.

**Tests**

- [ ] **AC-9.** Per-connector unit/integration tests cover collect → push
      against a mocked MDM API (no live Jamf/Intune in CI).
- [ ] **AC-10.** A test asserts neither connector emits device geolocation /
      full app inventory / owner PII-beyond-assignment — posture summary only
      (threat-model I).
- [ ] **AC-11.** A test asserts neither connector logs its MDM credential.

**Docs / JUDGMENT artifact**

- [ ] **AC-12.** Each connector README documents the minimal read-only scope
      (with the write/management-scope remote-wipe warning), the pull interval,
      and the evidence kinds.
- [ ] **AC-13.** A decisions log
      (`docs/audit-log/490-mdm-connectors-decisions.md`) records the
      shared-vs-per-connector evidence-kind choice, the posture-field set, the
      device→owner assignment-identity boundary, `x-default-scf-anchors`,
      scope-minimum, and stable-field JUDGMENT calls.
- [ ] **AC-14.** A changelog entry.

## Constitutional invariants honored

- **#3 — Single canonical inbound API (`IngestEvidence` / `Push`).** Both are
  first-class peer connectors holding source-side credentials; push-only wire.
- **Anti-pattern — no proprietary endpoint collector agent.** These are
  API-based MDM connectors; the on-device agent is the customer's own MDM, not a
  security-atlas agent (consistent with the osquery/Fleet/read-only-API posture).
- **Licensing — no closed proprietary connectors.** OSS, in-tree, read-only API.
- **Evidence integrity.** sha256 content-hash per record (v1 primitive).
- **Anti-pattern: honest intervals.** Each pull profile names its interval.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.1 — Evidence SDK, connectors,
  `profiles_supported`, push wire.
- `CLAUDE.md` "Planned repository layout" — `connectors/jamf/`,
  `connectors/intune/` named; "no proprietary endpoint collector" anti-pattern.
- `Plans/EVIDENCE_SDK.md` — full SDK contract incl. push profile.

## Dependencies

- **#003** (Evidence SDK proto + push client + CLI) — `merged`. The push surface.
- **#004** (AWS connector exemplar) — `merged`. The connector pattern template.
- **#191** (SDK OAuth client_credentials migration) — `merged`. Connector push
  credential.
- **#486** (Azure connector) — Intune shares the Entra app-registration auth
  pattern; NOT a hard dep (both follow the slice-004 template independently).

## Anti-criteria (P0 — block merge)

- **P0-490-1.** Does NOT widen the platform-side wire — push only (invariant #3).
- **P0-490-2.** Does NOT require or document management/write MDM scope —
  read-only least-privilege only; write scope is a remote-wipe risk
  (threat-model E).
- **P0-490-3.** Does NOT collect device geolocation / full app inventory / owner
  PII-beyond-assignment — posture summary only (threat-model I).
- **P0-490-4.** Does NOT log or transmit an MDM credential into the platform.
- **P0-490-5.** Does NOT ship a closed/proprietary on-device agent — API-based,
  OSS, read-only (anti-pattern: proprietary endpoint collectors).
- **P0-490-6.** Does NOT label a pull profile "continuous monitoring."
- **P0-490-7.** Does NOT implement software-inventory / config-profile-detail /
  event-driven evidence — follow-ons.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (collect→push round-trip; mocked MDM APIs) ·
`security-review` (source-credential + remote-wipe-scope + PII over-collection
risk) · `simplify` · `changelog-generator`.

## Notes for the implementing agent

- **Phase-2 grill output:** both connectors are the slice-004 / 442 pattern;
  cluster them because the posture evidence shape is identical. The defining risk
  is twofold: the write-scope remote-wipe hazard (scope minimum) and PII
  over-collection (geolocation / app inventory). Both are load-bearing guards.
- **JUDGMENT call: shared vs per-connector evidence_kind.** Prefer one shared
  `endpoint.device_posture.v1` shape if the posture field set is genuinely
  identical across Jamf + Intune; split only if the vendor models diverge enough
  to make a shared shape lossy. Record the call.
- **Other JUDGMENT calls you own:** posture field set, device→owner
  assignment-identity boundary, `x-default-scf-anchors`, scope minimum per MDM.
  Record in the decisions log; the maintainer re-checks anchor accuracy (OQ #9).
- Reuse `feedback_connector_patterns` conventions across both connectors.
- Detection-tier: `none` unless a bug surfaces during the build.
