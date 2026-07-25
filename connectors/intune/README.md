# Intune connector

The Intune connector (slice 490) brings **managed-device compliance posture**
into the platform's evidence pipeline — the recurring SOC 2 CC6.7 / CC6.8 and
ISO A.8 auditor demand ("prove Windows / cross-platform endpoints are
disk-encrypted and compliant"), today a manual Intune export. It follows the
locked connector pattern verbatim: register-per-run, a stable `actor_id`, an
hour-truncated `observed_at`, scope minimums, and vendor-native read-only auth.
It emits three evidence kinds:

| Kind                             | Profile | Source                                                                               |
| -------------------------------- | ------- | ------------------------------------------------------------------------------------ |
| `endpoint.device_posture.v1`     | pull    | Microsoft Graph `GET /deviceManagement/managedDevices`                               |
| `endpoint.software_inventory.v1` | pull    | Microsoft Graph `GET /deviceManagement/detectedApps`                                 |
| `endpoint.config_profile.v1`     | pull    | Microsoft Graph `GET /deviceManagement/managedDevices` (`deviceConfigurationStates`) |

All three evidence shapes are **shared** with the Jamf connector (the field sets
are identical at the summary altitude); `source_mdm` preserves provenance. The
software-inventory kind (slice 555) is the deliberate slice-490 over-collection
follow-on: the `detectedApps` inventory excluded from the posture summary is
collected here as a separate, scoped kind for patch-/vulnerability-management.
The config-profile kind (slice 556) reports WHICH configuration / compliance
profiles are deployed + their compliance-relevant settings, as evidence for
configuration-management controls (SCF `CFG-02` / `CFG-04`) — with a structural
secret-redaction boundary that keeps Wi-Fi PSKs, VPN shared secrets, certificate
private keys, SCEP challenges, and raw payload blobs out of every record.

The connector is **API-based**, not an in-host agent — consistent with the "no
closed proprietary collector agents on endpoints" anti-pattern. The on-device
agent is the customer's own MDM. The Graph credential stays source-side and never
enters an evidence record or a platform push (canvas invariant #3).

## The posture-summary boundary (the load-bearing guard)

An MDM holds device-owner PII, device geolocation, and a full installed-app
inventory. The connector collects **compliance posture SUMMARY + the
device→owner ASSIGNMENT identity only**:

**Collected (in scope):**

- disk-encryption state (BitLocker, via `isEncrypted`), screen-lock /
  passcode-policy compliance (folded into `complianceState`), OS version,
  management/enrollment state, and the MDM's compliance verdict;
- the device→owner **assignment identity** — the `userPrincipalName` (an opaque
  directory identity) + optional display name — needed to attribute the device
  for an access review.

**Never collected (out of scope):**

- device **geolocation**;
- the **detectedApps** installed-application inventory;
- device contents / browsing data;
- the owner's personal **phone / personal email**.

The decode boundary is the enforcement point: the connector requests an explicit
`$select` of posture-relevant properties ONLY (never `detectedApps`,
`phoneNumber`, or `emailAddress`) and the `RawDevice` struct has no field for
geolocation, app inventory, or owner contact detail, so those never enter memory
as connector data. A test
(`integration_test.go:TestEmittedRecords_NoGeoOrAppsOrPII`) asserts no
over-collection key/substring reaches an emitted payload (AC-10), the
client-level test asserts the `$select` never requests `detectedApps` /
`phoneNumber` / `emailAddress`, and another asserts the credential is never
logged (AC-11).

## Least-privilege credential (required minimum)

Set `INTUNE_TENANT_ID`, `INTUNE_CLIENT_ID`, and `INTUNE_CLIENT_SECRET` to an
Entra (Azure AD) **app registration** granted a single **read-only** Graph
application permission. Run `atlas-intune permissions` to print the canonical
minimum.

- Grant only `DeviceManagementManagedDevices.Read.All` (application,
  admin-consented).
- **NEVER** grant a write / management permission (no `...ReadWrite.All`, no
  `...PrivilegedOperations.All`). A write-capable MDM credential can
  **remote-wipe** or push configuration to employee endpoints — that is a
  remote-wipe risk and must never be used (threat-model E / P0-490-2).

The client secret is read from the environment, never a CLI flag (so it never
lands in shell history), and is never logged or placed into an evidence record.

For sovereign clouds, override `INTUNE_GRAPH_HOST` (e.g. `graph.microsoft.us`)
and `INTUNE_LOGIN_HOST` (e.g. `login.microsoftonline.us`).

## Profile + interval — honest, not "continuous monitoring"

The connector runs on the **pull** profile: each invocation is one bounded
read-and-push pass. It is **operator-scheduled** (cron / scheduler) — the
recommended cadence is **every 24h**. This is deliberately **not** "continuous
monitoring": the interval is named honestly. An event-driven profile (Intune
compliance-state-change notifications) is a documented follow-on, not part of v0.

## Usage

```sh
# Print the least-privilege permission requirement.
atlas-intune permissions

# Register the connector instance (profiles_supported = [pull]).
export SECURITY_ATLAS_ENDPOINT=atlas.example.com:443
export SECURITY_ATLAS_TOKEN=<platform bearer>
atlas-intune register

# Read managed-device compliance posture and push evidence.
export INTUNE_TENANT_ID=<entra tenant id>
export INTUNE_CLIENT_ID=<app registration id>
export INTUNE_CLIENT_SECRET=<app registration client secret>
atlas-intune run --environment prod

# Read detected-software inventory and push evidence (same read-only credential).
atlas-intune run-software --environment prod

# Read configuration-profile detail and push evidence (same read-only credential).
atlas-intune run-config-profiles --environment prod
```

## Software-inventory boundary (slice 555)

The `run-software` subcommand reads the detected-software inventory (the
`detectedApps` endpoint) for patch-/vulnerability-management evidence, using the
SAME read-only `DeviceManagementManagedDevices.Read.All` permission. Each item
carries the app **name** + **version** + Graph app **id** ONLY. It NEVER collects
executable **file paths**, per-user **app-usage telemetry**, **license keys**,
device contents, or owner contact detail. The read `$select`s `id,displayName,
version` and `$expand`s `managedDevices($select=id)` (never `sizeInByte`,
`deviceName`, or `userPrincipalName`), and the `RawSoftwareItem` struct has no
field for a path / usage / license key — a leak would be a compile error. The
per-device list is bounded (`MaxSoftwarePerDevice = 500`).

The read follows the Graph `@odata.nextLink` cursor (slice 590) so the FULL
`detectedApps` catalog is gathered before the app-centric graph is inverted to
the device-centric shape — without the walk, a device's apps that fall on later
pages would be silently dropped. The walk is bounded by a max-page cap
(`maxSoftwarePages = 50` at `$top = 200`) so a non-terminating nextLink chain
cannot drive an unbounded loop. The field allow-list above is unchanged by
pagination — only the page loop was added.

## Config-profile secret-redaction boundary (slice 556 — load-bearing)

The `run-config-profiles` subcommand reads configuration-profile DETAIL (the
`deviceConfigurationStates` expansion, plus the device `$select` of
`isEncrypted` + `complianceState`) for configuration-management evidence (SCF
`CFG-02` / `CFG-04`), using the SAME read-only
`DeviceManagementManagedDevices.Read.All` permission. Each profile carries the
**name** + **identifier** + **type** + assigned **scope** + **uuid** +
**last-modified**; per-setting enrichment (slice 595) adds each profile's
allow-listed `profile_assignment_state` (compliant / nonCompliant / conflict /
error) and a synthetic **Enforced Configuration Summary** profile whose `settings`
are the device-level enforced facts (`disk_encryption_enforced` from
`isEncrypted`, `device_compliant` from `complianceState`) — non-secret summaries,
NEVER the raw configuration payload. The `isEncrypted` + `complianceState`
properties are covered by that SAME read-only permission — no new scope. It NEVER
collects the secrets profiles routinely embed — Wi-Fi PSKs, VPN shared secrets,
certificate private keys, API tokens, SCEP challenges, or raw payload blobs.
Enforcement is structural at three layers: neither the device `$select` nor the
`deviceConfigurationStates` expansion carries the raw setting payload (never
`deviceName` / `userPrincipalName`); the `RawConfigSetting` struct has no field
for a credential — a leak would be a compile error; and the settings allow-list
(`cfgprofile.AllowedSettingKeys`) plus a credential deny-list drop any
secret-bearing key at both the normalizer and the record builder.

## Scope minimums

Every record is scoped to `service` (`intune`) and the required `--environment`.
Records carry `Result = INCONCLUSIVE`: the connector reports a descriptive
posture; the platform evaluator owns the final pass/fail per `(control, scope)`.

## Default SCF anchors (maintainer recheck — OQ #9)

The bundled schema carries default SCF-anchor hints, flagged for maintainer
accuracy recheck:

- `endpoint.device_posture.v1` → `END-04` (Endpoint Security), `CFG-02` (Secure
  Baseline Configurations) — consistent with the existing
  `osquery.host_posture.v1` anchors.
- `endpoint.software_inventory.v1` → `VPM-04` (Vulnerability Remediation
  Process), `AST-03` (Asset Inventory) — a deliberately different anchor set
  (the kind answers a different control question than posture).
- `endpoint.config_profile.v1` → `CFG-02` (Secure Baseline Configurations),
  `CFG-04` (Configuration Change Control) — configuration-management evidence at
  a finer grain than the posture verdict.

## Follow-ons (out of v0 scope)

- config-profile per-setting enrichment (populate `settings[]` through the same
  allow-list, slice 595);
- event-driven profile via MDM compliance-state-change webhooks (slice 557).
