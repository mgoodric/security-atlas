# 556 — MDM configuration-profile detail evidence (Jamf + Intune): JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls (the
separate-kind-vs-posture-extension choice, the record granularity, the
configuration-profile field set, the SCF anchors, and THE load-bearing call —
the secret-redaction allow-list / structural boundary). It does NOT block merge;
the maintainer iterates post-deployment from the "Revisit once in use" list.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** none (no shipped-behavior bug surfaced during the
  build).
- **detection_tier_target:** none.

The only build-time corrections were the expected consequence of adding a sibling
kind (the per-connector `TestSupportedKinds_DevicePosture` set assertion now
includes `endpoint.config_profile.v1`) and a coverage top-up on the
`cfgprofile.Normalize` nil-clock + settings-bound branches — both authoring
fixes at the unit tier, neither a product-behavior defect.

## Decisions made

### D1 — A SEPARATE sibling kind `endpoint.config_profile.v1`, NOT an extension of posture or software-inventory (the clustering call)

- **Options:** (a) a new sibling kind `endpoint.config_profile.v1` shared across
  both connectors; (b) extend `endpoint.device_posture.v1` with a `profiles[]`
  array; (c) two per-connector kinds.
- **Chosen:** (a), one shared sibling kind, built once in `connectors/mdm/cfgrecord`,
  normalized once in `connectors/mdm/cfgprofile` — mirroring the slice-490
  `{devposture,devrecord}` and slice-555 `{swinventory,swrecord}` splits exactly.
- **Rationale:** configuration-profile detail answers a DIFFERENT control question
  than the posture verdict. Posture (slice 490) reports WHETHER a device is
  compliant (encryption / screen-lock / OS / enrolment); this reports WHICH
  configuration / compliance profiles are deployed and what compliance-relevant
  settings they enforce — evidence for configuration-management controls (SCF
  CFG-02 Secure Baseline Configurations / CFG-04 Configuration Change Control) at
  a finer grain than the verdict. Option (b) was rejected because folding a
  per-device profile list into the posture summary would re-introduce
  over-collection and couple two distinct control questions into one record,
  breaking the separate-anchor-set provenance. Option (c) was rejected for the
  same reason slices 490 D1 / 555 D1 rejected per-connector kinds: the
  profile-detail field set is identical at the detail-summary altitude, so a
  shared shape is not lossy; the `source_mdm` discriminator preserves per-vendor
  provenance.

### D2 — Field set + per-device roll-up granularity (the field-shape call)

- **Decision:** one record per managed device carrying a bounded `profiles[]`
  list. Each profile carries: `name` (required) + optional `identifier` +
  `profile_type` + `scope` (assigned device-group NAMES) + `uuid` +
  `last_modified` + a bounded `settings[]` list (allow-listed compliance-relevant
  key + non-secret summary value). It carries NO raw payload blob, NO credential,
  NO owner contact detail, NO device geolocation.
- **Granularity rationale:** per-device roll-up (not per-profile records) keeps
  the clean per-device idempotency model the slice-490 family established and
  keeps the profile list joinable to the device's posture + software records by
  `device_id`. Keyed `(mdm, device_id, hour)`, same-device same-hour re-runs
  collapse into one ledger row.
- **Bounds:** `MaxProfilesPerDevice = 200` and `MaxSettingsPerProfile = 64`
  (≥ the allow-list size, so no allow-listed key is ever dropped by the bound),
  both applied after a stable sort — defensive over-collection ceilings
  (threat-model D, parallel to the slice-555 software bound).

### D3 — THE LOAD-BEARING SECRET-REDACTION BOUNDARY (P0-556 / threat-model I)

Configuration profiles routinely embed SECRETS — Wi-Fi PSKs, VPN shared secrets,
certificate private keys, API tokens, SCEP challenges, and arbitrary password /
`<data>` / `PayloadContent` payload values. A leak of any of these into the
append-only evidence ledger is the worst-case outcome (an immutable secret
record). Enforcement is structural at THREE layers (the slice-490/555 gold
standard), so a leak is a compile error, a not-requested field, AND a dropped key:

1. **Type layer (compile error).** `cfgprofile.Setting` / `RawSetting` and the
   per-vendor `RawConfigSetting` carry only a `Key` + a non-secret summary
   `Value`. There is NO field anywhere in the type graph for a raw payload blob,
   a password, a private key, a shared secret, or a SCEP challenge. A future
   contributor cannot accidentally plumb one without adding a struct field — a
   visible, reviewable change.

2. **API-request layer (not requested / not decoded).** The vendor clients
   request profile METADATA only: Jamf asks for the `GENERAL +
CONFIGURATION_PROFILES` inventory sections (which return display name /
   identifier / uuid / last-installed, NOT the raw payload-content blob; never
   `USER_AND_LOCATION` / GPS `location` / `APPLICATIONS`); Intune `$select`s the
   device id and `$expand`s `deviceConfigurationStates($select=id,displayName,
state)` (the assignment-state metadata, NOT the configuration's raw setting
   payload; never `deviceName` / `userPrincipalName`). `json.Decode` discards
   JSON keys with no matching struct field, so a secret a source might still
   return never enters memory as connector data. Proven by
   `TestClient_ListConfigProfiles_DecodesMetadataOnly_NoSecrets` on BOTH
   connectors (the fixture deliberately carries `payloadContent` / `wifiPassword`
   / `certificatePrivateKey` / `settingPayload` and asserts the request never
   asked for them and the decoded shape cannot hold them).

3. **Allow-list layer (dropped key).** The settings allow-list
   (`cfgprofile.AllowedSettingKeys`) is the ONLY set of keys that may enter a
   record — every key names a compliance-relevant control (`passcode_required`,
   `disk_encryption_enforced`, `firewall_enabled`, `screen_lock_enforced`, …)
   whose value is a non-secret summary. A belt-and-braces deny-list
   (`IsBannedSettingKey`) drops any key containing a credential substring
   (`password`, `secret`, `psk`, `privatekey`, `token`, `challenge`,
   `payloadcontent`, `certificate`, `data`, `key_data`, `pin`, …). A setting must
   be allow-listed AND not deny-flagged to survive. BOTH the normalizer
   (`cfgprofile.normalizeSettings`) AND the record builder (`cfgrecord.Build`)
   re-apply the filter, so even a hand-built `Device` cannot smuggle a secret.

**The secret-redaction test (THE required assertion):**
`TestBuild_PayloadCarriesProfileFieldsOnly_NoSecrets` hand-builds a `Device`
carrying `wifi_password` / `vpn_shared_secret` / `certificate_private_key` /
`PayloadContent` settings, then asserts: (a) only allow-listed top-level + profile

- setting keys reach the payload; (b) no banned credential substring appears in
  any setting key; and (c) a recursive walk over the entire payload (keys AND string
  values) finds none of the secret VALUES (`hunter2`, `topsecret`, the synthetic
  fake-private-key fixture marker, the base64 blob).
  `TestNormalize_DropsSecretBearingSettings`
  proves the same at the normalizer.

### D4 — `x-default-scf-anchors = [CFG-02, CFG-04]` (maintainer recheck)

- **Decision:** the schema defaults to `CFG-02` (Secure Baseline Configurations)
  and `CFG-04` (Configuration Change Control).
- **Rationale:** configuration-profile detail is the direct evidence substrate for
  configuration-management — CFG-02 ("is the secure baseline configuration
  deployed?") and CFG-04 ("what configuration is change-controlled / deployed to
  these assets?"). Both anchors exist in the sample SCF catalog
  (`migrations/fixtures/scf-sample.json`). This is the SAME `CFG-02` the posture
  kind also carries (posture proves the verdict, config-profile proves the
  baseline detail behind it) PLUS the change-control anchor `CFG-04` that the
  detail surface uniquely supports — and a DIFFERENT set than the slice-555
  software kind's `[VPM-04, AST-03]`, the schema-level signal that this kind
  answers a distinct control question. Overridable per the `--config-control` CLI
  flag and the schema's `x-default-scf-anchors`.

### D5 — Read-only, SAME credential as `run`; new `run-config-profiles` subcommand; pull-only honest interval (P0-556 anti-criteria)

- **Decision:** the config-profile read uses the **same** read-only credential as
  the posture `run` (Jamf read-only API role; Intune
  `DeviceManagementManagedDevices.Read.All` — the deviceConfigurationStates
  expansion is read under that same device-management permission). No new MDM
  scope, no write/management permission, no widening of the platform-side wire
  (push only — invariant #3). A separate `run-config-profiles` subcommand keeps
  the three evidence kinds independently schedulable; `profiles_supported =
[pull]`, recommended 24h, named "operator-scheduled — NOT continuous
  monitoring."
- **Rationale:** honors all three slice-556 P0 anti-criteria (no certificate
  private keys / Wi-Fi PSKs / VPN shared secrets / credential payloads collected;
  push-only wire; no write/management scope). The slice-490 remote-wipe guard is
  unchanged.

### D6 — Idempotency-key namespacing by evidence-kind prefix

- **Decision:** `ConfigProfileKey = sha256("endpoint.config_profile|<mdm>/<device_id>|<hour>")`,
  distinct from the posture and software-inventory key prefixes.
- **Rationale:** the same device in the same hour now produces THREE records
  (posture + software + config-profile); without a kind prefix they would collide
  on the same idempotency key and the ledger would drop two. The prefix namespaces
  all three. Proven by `TestConfigProfileKey_NamespacedFromSiblings`.

## Revisit once in use (maintainer)

- **Anchor accuracy:** confirm `CFG-02` + `CFG-04` are the right SCF anchors for
  configuration-profile detail, or refine if a deployment prefers a dedicated
  configuration-baseline anchor.
- **Per-setting enrichment:** v0 emits per-profile METADATA only (Jamf's
  CONFIGURATION_PROFILES section + Intune's deviceConfigurationStates are
  metadata-only — they report WHICH profiles are deployed, not per-setting
  key/values). The `settings[]` field is wired end-to-end through the allow-list
  guard and is exercised by tests via constructed fixtures, but is empty for both
  vendors at v0. A richer per-setting read (Jamf profile payload-summary parse /
  Intune `deviceConfigurations` per-setting projection — through the SAME
  allow-list, NEVER the raw payload blob) is spillover slice 595.
- **Per-device bound:** `MaxProfilesPerDevice = 200` is a v0 ceiling; raise it
  (schema-additive, no break) if a real adopter assigns more profiles per device.
- **Pagination:** v0 reads one bounded page per MDM (Jamf `page-size=200`, Intune
  `$top=200`); a large fleet needs cursor pagination — the same follow-on as
  slices 490 + 555.

## Spillover slices filed

- **595** — MDM config-profile per-setting enrichment (Jamf profile
  payload-summary parse + Intune per-setting projection, through the same
  compliance-relevant allow-list, never the raw payload blob). Parent: 556.
