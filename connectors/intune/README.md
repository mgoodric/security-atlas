# Intune connector

The Intune connector (slice 490) brings **managed-device compliance posture**
into the platform's evidence pipeline — the recurring SOC 2 CC6.7 / CC6.8 and
ISO A.8 auditor demand ("prove Windows / cross-platform endpoints are
disk-encrypted and compliant"), today a manual Intune export. It follows the
locked connector pattern verbatim: register-per-run, a stable `actor_id`, an
hour-truncated `observed_at`, scope minimums, and vendor-native read-only auth.
It emits one evidence kind:

| Kind                         | Profile | Source                                                 |
| ---------------------------- | ------- | ------------------------------------------------------ |
| `endpoint.device_posture.v1` | pull    | Microsoft Graph `GET /deviceManagement/managedDevices` |

The evidence shape is **shared** with the Jamf connector (the device-posture
field set is identical at the posture-summary altitude); `source_mdm` preserves
provenance.

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
```

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

## Follow-ons (out of v0 scope)

- software-inventory evidence (slice 555);
- configuration-profile detail evidence (slice 556);
- event-driven profile via MDM compliance-state-change webhooks (slice 557).
