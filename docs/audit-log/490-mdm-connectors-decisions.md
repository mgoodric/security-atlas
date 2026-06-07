# 490 — MDM connectors (Jamf + Intune): JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls (the
shared-vs-per-connector evidence-kind choice, the posture-field set, the
device→owner assignment-identity boundary, the `x-default-scf-anchors`, the
scope minimums, the per-MDM least-privilege auth scope, and the stable-field
choices). It does NOT block merge — the maintainer iterates post-deployment from
the "Revisit once in use" list.

- detection_tier_actual: none
- detection_tier_target: none

(No product-behavior bug surfaced during the build. Two self-inflicted test
bugs were caught at the green step and fixed in the same PR: (1) the Intune
`complianceOf` mapping lowercases its input, so the `inGracePeriod` switch case
had to be lowercased to `ingraceperiod` to match — a normalization-vs-case-
literal mismatch in MY code, caught by the table test; (2) the
`RequiredPermission` read-only assertion banned the substring "manage", which
collides with the legitimate Graph permission name `...ManagedDevices...` — a
test-authoring over-strictness, narrowed to ban only the real write markers
`readwrite` / `privilegedoperations`. Both are build-time authoring errors, not
defects in shipped behavior.)

## Decisions made

### D1 — ONE shared `endpoint.device_posture.v1` evidence kind across both connectors (THE clustering call)

- **Options considered:** (a) one shared `endpoint.device_posture.v1` kind with a
  `source_mdm` discriminator; (b) two per-connector kinds
  (`jamf.device_posture.v1` + `intune.device_posture.v1`); (c) reuse the existing
  `osquery.host_posture.v1` kind.
- **Chosen:** (a), one shared kind, built once in `connectors/mdm/devrecord`,
  normalized once in `connectors/mdm/devposture`.
- **Rationale:** the posture-summary field set is genuinely identical at the MDM
  altitude — disk-encryption (FileVault ≡ BitLocker), screen-lock/passcode
  compliance, OS version, managed/enrolment state, compliance verdict, and the
  device→owner assignment identity. The spec's grill output explicitly prefers a
  shared shape "if the posture field set is genuinely identical … split only if
  the vendor models diverge enough to make a shared shape lossy" — it does not
  diverge. This mirrors the slice-488 Datadog/Grafana shared-`monitoring.alert_
config.v1` precedent exactly. Option (c) was rejected: `osquery.host_posture.v1`
  is owned by the osquery/Fleet fleet-managed path and has a different field set
  (`host_uuid`, `firewall_enabled`, `mdm_enrolled` boolean) — folding MDM posture
  into it would muddy two distinct evidence sources and break the per-source
  provenance the `source_mdm` discriminator gives us. The shared-package layout
  (`connectors/mdm/{devposture,devrecord,idem}`) is the slice-488
  `connectors/monitoring/` pattern.

### D2 — Posture-summary-only field set + the device→owner assignment-identity boundary (THE load-bearing guard, threat-model I / P0-490-3)

- **Decision:** the record carries **posture/compliance summary + the
  device→owner ASSIGNMENT identity only** — disk-encryption state, screen-lock
  compliance, OS version, platform, managed/enrolled flags, compliance verdict,
  the opaque assigned-user id (Jamf `username` / Intune `userPrincipalName`), and
  an optional display name. It carries **no** device geolocation, **no**
  installed-app inventory, **no** device contents, and **no** owner personal
  contact detail (phone / personal email / address).
- **Enforcement:** structural, at three layers. (1) The shared
  `devposture.RawDevice` and the per-vendor `RawComputer` / `RawDevice` Go types
  have no field for geolocation, apps, or owner contact detail — a leak would be
  a compile error, not a runtime check. (2) The vendor clients request ONLY the
  posture-relevant data at the API boundary: Jamf asks for the inventory sections
  `GENERAL, OPERATING_SYSTEM, DISK_ENCRYPTION, SECURITY, USER_AND_LOCATION` (never
  `APPLICATIONS`, never the `location` GPS section); Intune sends an explicit
  `$select` of posture properties (never `detectedApps`, never `phoneNumber`,
  never `emailAddress`). (3) The record builder allow-lists payload keys. Tests:
  `TestEmittedRecords_NoGeoOrAppsOrPII` (both connectors) asserts only
  allow-listed keys + no banned substring reach a payload; the client tests
  assert the request itself never asks for the over-collection sections/props.
- **Rationale:** the MDM is the highest-PII-cost source in the roster (it holds
  device location, full app inventory, and owner contact detail). The assignment
  identity is the minimum that makes the evidence meaningful to an auditor ("which
  managed device belongs to whom?"); everything past that is over-collection. The
  assigned-user id is treated as an OPAQUE directory identity, not a contact
  channel.

### D3 — Read-only least-privilege auth per MDM + the remote-wipe warning (threat-model E / P0-490-2)

- **Decision:** each connector requires a **read-only device-read** credential
  only, documented as the exact minimum, with an explicit remote-wipe warning.
  - **Jamf:** an API client (client_credentials) bound to an API role granting
    only the read-inventory privileges (Read Computers, Read Mobile Devices).
  - **Intune:** an Entra app registration granted only the read-only Graph
    application permission `DeviceManagementManagedDevices.Read.All`.
- **Enforcement:** the `permissions` subcommand prints the canonical minimum and
  the remote-wipe warning; the auth-package tests
  (`TestRequiredRole_IsReadOnly` / `TestRequiredPermission_IsReadOnly`) hold the
  documented scope to read-only (no `write`/`readwrite`/`privilegedoperations`
  markers); the cmd `permissions` tests assert the rendered output carries the
  "NEVER grant … remote-wipe" warning. The connectors issue only HTTP `GET`s
  against the inventory/device endpoints.
- **Rationale:** an MDM is uniquely dangerous because a write/management
  credential can remote-wipe or push configuration to employee endpoints. The
  read-only constraint is the single most load-bearing security decision in the
  slice; the docs name it explicitly so an operator cannot "grant write to be
  safe."

### D4 — `x-default-scf-anchors = [END-04, CFG-02]` (maintainer recheck, OQ #9)

- **Decision:** the shared schema defaults to `END-04` (Endpoint Security) and
  `CFG-02` (Secure Baseline Configurations).
- **Rationale:** these are exactly the anchors the existing
  `osquery.host_posture.v1` schema uses for endpoint-posture evidence — keeping
  the MDM posture kind consistent with the fleet-managed posture kind. Both are
  present in the SCF sample catalog (`migrations/fixtures/scf-sample.json`).
  Flagged for maintainer accuracy recheck per OQ #9.

### D5 — Scope minimums: `service` + required `--environment`

- **Decision:** every record is scoped to `service` (the literal `jamf` /
  `intune`) and an operator-supplied `--environment` (required; the run fails
  fast if unset). `Result = INCONCLUSIVE`.
- **Rationale:** matches the slice-004 / 488 scope-minimum convention. The
  connector reports descriptive posture; the platform evaluator owns the pass/fail
  per `(control, scope)`. Environment is required so records are never emitted
  unscoped.

### D6 — Stable fields: `actor_id`, hour-truncated `observed_at`, stable-optional payload

- **Decision:** `actor_id = connector:<jamf|intune>:devices@<version>`;
  `observed_at` truncated to the UTC hour; the idempotency key
  `sha256("endpoint.device_posture|<mdm>/<device_id>|<hour>")`; optional payload
  fields (`device_name`, `os_version`, `platform`, `owner_assignment_id`,
  `owner_display_name`) omitted entirely when empty rather than emitted as empty
  strings.
- **Rationale:** matches the cross-connector `feedback_connector_patterns` / slice
  004 conventions (stable actor_id, observed_at granularity, stable optional
  fields). Hour truncation collapses same-device same-hour re-runs into one ledger
  row (verified by `TestRun_DedupesWithinHour`).

### D7 — pull profile only; honest interval (P0-490-6 / P0-490-7)

- **Decision:** `profiles_supported = [pull]`, recommended cadence 24h, named as
  "operator-scheduled — NOT continuous monitoring." No webhook/event-driven
  profile; no software-inventory / config-profile-detail evidence. v0 reads the
  first bounded page (bounded page size + run timeout, threat-model D); cursor
  pagination across the full fleet is a follow-on.
- **Rationale:** the spec's anti-criteria forbid "continuous monitoring" labels
  (P0-490-6) and scope software-inventory / config-detail / event-driven evidence
  out (P0-490-7). Follow-ons filed as slices 555–557.

## Revisit once in use (maintainer)

- **Anchor accuracy (OQ #9):** confirm `END-04` + `CFG-02` are the right SCF
  anchors for MDM endpoint posture, or refine.
- **Pagination:** v0 reads one bounded page per MDM. A fleet larger than the page
  size (Jamf `page-size=200`, Intune `$top=200`) needs cursor pagination — filed
  as a follow-on consideration when a large-fleet adopter appears.
- **Passcode/screen-lock fidelity:** Jamf exposes `screenLockGracePeriodEnforced`
  (a proxy for an enforced lock policy); Intune folds passcode compliance into
  the overall `complianceState`. If an adopter needs a dedicated passcode-policy
  signal distinct from overall compliance, that is a schema-additive follow-on.

## Spillover slices filed (parent 490)

- **555** — Jamf + Intune software-inventory evidence (separate evidence kind;
  the deliberately-excluded `APPLICATIONS` / `detectedApps` surface).
- **556** — Jamf + Intune configuration-profile detail evidence.
- **557** — MDM event-driven (webhook) profile via compliance-state-change.
