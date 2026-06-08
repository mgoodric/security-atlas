# 595 — MDM connectors: configuration-profile per-setting enrichment (Jamf + Intune)

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (which per-setting keys to surface — extends the slice-556 allow-list)
**Status:** `blocked` (depends on #556 — config-profile detail evidence — merged first)

## Narrative

Slice 556 added configuration-profile detail evidence
(`endpoint.config_profile.v1`) to the Jamf + Intune MDM connectors. The record
shape carries a per-profile `settings[]` list of compliance-relevant key/value
pairs (`passcode_required`, `disk_encryption_enforced`, `firewall_enabled`, …)
gated by a structural secret-redaction allow-list (`cfgprofile.AllowedSettingKeys`

- the `IsBannedSettingKey` deny-list), but **v0 emits per-profile METADATA only**
  — the `settings[]` list is empty for both vendors at v0 because the reads used are
  metadata-only:

* **Jamf:** the `CONFIGURATION_PROFILES` inventory section returns profile
  display name / identifier / uuid / last-installed, NOT the per-setting payload.
* **Intune:** the `deviceConfigurationStates` expansion returns the assigned
  profile's id / displayName / assignment state, NOT the per-setting values.

The `settings[]` field is wired end-to-end through the allow-list guard (and
exercised by tests via constructed fixtures), so this slice only needs to
populate it from a richer read:

- **Jamf:** parse the configuration-profile payload SUMMARY for the
  compliance-relevant keys (e.g. via `GET /api/v1/computers-inventory` extended
  sections or the classic `/JSSResource/osxconfigurationprofiles` general/payload
  summary), projecting ONLY allow-listed setting keys.
- **Intune:** project the per-setting values from `deviceConfigurations` /
  `deviceCompliancePolicies` (e.g. `passcodeRequired`, `storageRequireEncryption`,
  `firewallEnabled`), mapping vendor property names onto the slice-556 allow-list.

## Anti-criteria (P0) — inherits slice 556's load-bearing boundary

- The enrichment goes \*\*through the same `cfgprofile.AllowedSettingKeys` allow-list
  - `IsBannedSettingKey` deny-list\*\* — it NEVER reads or emits a raw payload-content
    blob, a Wi-Fi PSK, a VPN shared secret, a certificate private key, a SCEP
    challenge, or any credential payload field. New compliance-relevant keys added to
    the allow-list in this slice carry non-secret summary values ONLY.
- Read-only credential, unchanged (no new MDM scope; no write/management
  permission — the slice-490 remote-wipe guard stays).
- Push-only platform wire (invariant #3); no schema break (purely populates an
  already-defined optional array).

## Dependencies

- **#556** (config-profile detail evidence) — must merge first; this populates the
  `settings[]` field the slice-556 schema + builder already define.

Parent: #556.
