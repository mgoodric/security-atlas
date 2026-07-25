# 595 — MDM config-profile per-setting enrichment (Jamf + Intune): JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls: WHICH
per-setting keys to surface (extending the slice-556 allow-list), the source-field
choice (the load-bearing scope call), the value-shape choice, the secret-drop
guard extension, and the detection-tier classification. It does NOT block merge;
the maintainer iterates post-deployment from the "Revisit once in use" list.

Parent: slice 556 (`docs/audit-log/556-mdm-config-profile-decisions.md`). This
slice populates the `settings[]` field that slice 556 wired end-to-end but left
empty at v0. No new evidence kind, no schema change — `endpoint.config_profile.v1`
(`1.0.0`) shape is unchanged.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** unit. The only build-time corrections were the
  expected consequence of populating `settings[]`: the per-vendor
  `TestClient_ListConfigProfiles_DecodesMetadataOnly_NoSecrets` assertions
  (`len(profiles[0].Settings) != 0` → now expects the synthetic summary profile +
  per-state settings) and the profile-count change (literal profiles + 1 synthetic
  summary). Caught at the unit tier by the existing client tests; neither a
  product-behavior defect.
- **detection_tier_target:** unit. Per-setting projection from a fixed inventory
  shape is pure decode/map logic — fully exercised by the faked-API client tests
  and the `cfgprofile` normalizer tests. No integration/Postgres surface is
  touched (the connectors are out-of-process peers; the platform-side wire is
  unchanged push).

## Decisions made

### D1 — Source the per-setting values from metadata-grade ENFORCED-STATE inventory fields, NOT the raw profile payload (THE LOAD-BEARING SCOPE CALL)

- **Options the slice spec surfaced:**
  - (a) Jamf classic `/JSSResource/osxconfigurationprofiles/id/{id}`
    general/payload summary; Intune `deviceConfigurations` /
    `deviceCompliancePolicies` per-setting projection.
  - (b) Derive per-setting values from the **effective enforced-state inventory
    fields the connector already reads under the existing read-only scope** — for
    Jamf the posture sections `OPERATING_SYSTEM` / `DISK_ENCRYPTION` / `SECURITY`
    (FileVault status, Gatekeeper status, screen-lock-grace enforcement,
    supervised/managed); for Intune the device-level `isEncrypted` +
    `complianceState` plus the per-profile assignment `state` already in the
    `deviceConfigurationStates` expansion.
- **Chosen:** (b).
- **Rationale (load-bearing, P0-556):** option (a) reads the **raw
  configuration-profile payload** (Jamf `payloadContent` / Intune
  `deviceConfigurations` setting payloads), which is exactly where the secrets
  live — Wi-Fi PSKs, VPN shared secrets, certificate private keys, SCEP
  challenges, arbitrary `<data>` blobs. Even with the allow-list dropping the
  secret keys, **requesting** that payload pulls the secret bytes into connector
  memory (a needless exposure) and, for Jamf, the classic
  `osxconfigurationprofiles` endpoint typically needs a broader read privilege.
  Option (b) reads only the **effective enforced compliance state** — the same
  metadata-grade fields the slice-490 posture read already requests under the
  same read-only credential — so:
  1. **No new scope.** Jamf: the `OPERATING_SYSTEM` / `DISK_ENCRYPTION` /
     `SECURITY` sections are covered by the existing "Read Computers" read-only
     API role (the posture `run` already reads them). Intune: `isEncrypted` +
     `complianceState` are covered by the existing
     `DeviceManagementManagedDevices.Read.All` permission (the posture `run`
     already `$select`s both). The slice-490 remote-wipe guard (no
     write/management scope) is unchanged.
  2. **No raw payload ever requested or decoded.** The connector never asks any
     API surface for the configuration payload, so a secret never enters memory.
  3. **Honest semantics.** The values report the device's effective ENFORCED
     state (is disk encryption actually on? is the device compliant? is the
     profile assignment in conflict?) — which is precisely the
     configuration-management evidence an auditor wants ("show the enforced
     disk-encryption configuration"), at the per-device grain the kind already
     uses.
- **Shape:** the enforced facts are projected onto a single synthetic
  **"Enforced Configuration Summary"** profile per device (a connector-derived
  roll-up, NOT a literal MDM profile — its `profile_type` is `compliance`), so the
  operator sees the enforced hardening facts as compliance-relevant settings
  without conflating them with a specific named profile. Intune additionally
  attaches the allow-listed `profile_assignment_state` to each literal profile
  from its already-read assignment `state`.

### D2 — Allow-list extension: four new compliance-relevant hardening keys

Added to `cfgprofile.AllowedSettingKeys`:

| Key                        | Source                                              | Value shape                                                                 | Compliance relevance                                                                                                                                                        |
| -------------------------- | --------------------------------------------------- | --------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `device_compliant`         | Intune `complianceState == "compliant"`             | `"true"` / `"false"`                                                        | The overall enforced-policy verdict — the core configuration-management fact.                                                                                               |
| `device_supervised`        | Jamf `general.supervised`                           | `"true"` / `"false"`                                                        | Supervision is the precondition for enforcing the strongest MDM restrictions; auditors check it for managed-baseline coverage.                                              |
| `device_managed`           | Jamf `general.managed` / `remoteManagement.managed` | `"true"` / `"false"`                                                        | Whether the device is under active management (config enforcement is meaningful only on a managed device).                                                                  |
| `profile_assignment_state` | Intune `deviceConfigurationStates[].state`          | enum: `compliant` / `nonCompliant` / `conflict` / `error` / `notApplicable` | Per-profile deployment health — a `conflict` / `error` state means the intended configuration is NOT actually enforced; load-bearing for "is the baseline really deployed?" |

The pre-existing allow-listed keys the enrichment also populates:
`disk_encryption_enforced`, `gatekeeper_enabled`, `screen_lock_enforced`.

**Keys NOT added (deliberately):** anything that could carry or imply a credential
payload. The deny-list (`IsBannedSettingKey`) substrings (`password`, `secret`,
`psk`, `privatekey`, `token`, `challenge`, `payloadcontent`, `certificate`,
`data`, `pin`, …) remain the belt-and-braces second check; the
`TestAllowListHasNoBannedKey` self-consistency guard proves none of the four new
keys is flagged by the deny-list (they are not — none contains a banned
substring).

### D3 — Value-shape: normalized non-secret summary computed from enforced-state, never a copied payload value

- **Decision:** every setting value is either a boolean rendered `"true"`/`"false"`
  (via the connector-local `boolStr` helper) or a small normalized state enum
  (via `normalizeAssignmentState`). A value is **computed** from an enforced-state
  field — it is never a raw string copied out of a profile payload.
- **Rationale:** even for an allow-listed key, a value could in principle carry a
  secret if it copied a payload string (e.g. an allowed `wifi_ssid` key sitting
  next to a `wifi_password` — the spec's example). By deriving values only from
  typed booleans / known enums, the value channel structurally cannot transport a
  secret: there is no code path that copies a payload string into a value. The
  `TestClient_ListConfigProfiles_NoSecretValueLeaks` test (both vendors) serves a
  payload with fake secret siblings next to the enforced-state fields and asserts
  no setting value contains a secret marker.

### D4 — No new read scope (P0-556 anti-criteria, re-affirmed)

- Jamf: extends the config-profile read's `section` list with
  `OPERATING_SYSTEM` / `DISK_ENCRYPTION` / `SECURITY` — all read by the posture
  `run` under the existing "Read Computers" read-only role. No new privilege.
- Intune: extends the device `$select` with `isEncrypted` + `complianceState` —
  both read by the posture `run` under the existing
  `DeviceManagementManagedDevices.Read.All` permission. No new permission.
- `cmd_permissions` output and `RequiredRole` / `RequiredPermission` are
  **unchanged** (correctly — no new scope to document). The `run-config-profiles`
  `Long` help and both READMEs are updated to state explicitly that the
  enrichment reads add no new scope.

### D5 — Coverage ratchet (b228): shared mdm sub-package lines tested in the same change

- The shared `connectors/mdm/cfgprofile` allow-list grew by four keys; the same
  change adds `TestNormalize_AllowsSlice595EnrichmentKeys` (every new key
  surfaces) and `TestNormalize_ValueSanitization_DropsSecretSiblingNextToAllowedKey`
  (the secret-drop / value-sanitization guard). The per-vendor client packages
  each gain the enrichment-decode assertions + a `NoSecretValueLeaks` secret-drop
  test. No coverage floor was lowered.

## The required secret-drop assertions (where they live)

- **Normalizer (shared):**
  `cfgprofile.TestNormalize_ValueSanitization_DropsSecretSiblingNextToAllowedKey`
  — allowed `disk_encryption_enforced` survives; sibling `wifi_password` (banned)
  and `wifi_ssid` (off-list) are dropped; the secret value appears nowhere.
  Plus the pre-existing `TestNormalize_DropsSecretBearingSettings`.
- **Jamf client:** `TestClient_ListConfigProfiles_NoSecretValueLeaks` — payload
  carries fake `payloadContent` / `wifiPassword` / `vpnSharedSecret` /
  `certificatePrivateKey` next to the posture fields; no setting value contains a
  secret marker.
- **Intune client:** `TestClient_ListConfigProfiles_NoSecretValueLeaks` — payload
  carries fake `settingPayload` / `wifiPassword` / `vpnSharedSecret` /
  `certificate` next to the assignment state; no setting value contains a secret
  marker.
- **Record builder (shared, pre-existing, unchanged):**
  `cfgrecord.TestBuild_PayloadCarriesProfileFieldsOnly_NoSecrets` — recursive walk
  over the entire structpb payload (keys AND string values) finds none of the
  secret values.

## Revisit once in use (maintainer)

- **Richer per-setting depth:** the enforced-state projection reports the
  effective hardening verdict (encryption on/off, compliant/not, supervised/not,
  per-profile assignment health). If a deployment needs a finer per-payload
  setting (e.g. the actual passcode minimum length), it requires reading the raw
  profile payload through a NEW allow-list-gated, redaction-audited read — a
  deliberate follow-on, NOT this slice (it crosses the payload boundary D1
  rejected).
- **Pagination:** unchanged from slice 556 — one bounded page per MDM. A large
  fleet needs cursor pagination (the same follow-on as slices 490 / 555 / 556).
- **Assignment-state enum coverage:** `normalizeAssignmentState` recognizes the
  common Graph states; confirm the full enum (e.g. `notApplicable`,
  `remediated`) against a real tenant and extend if a state is observed that maps
  to `""` (silently dropped today).

## Spillover slices filed

- None. The slice was self-contained within the slice-556 wiring.
