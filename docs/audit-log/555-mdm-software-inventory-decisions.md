# 555 — MDM software-inventory evidence (Jamf + Intune): JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls (the
separate-kind-vs-posture-extension choice, the record granularity, the
software-field allow-list, the SCF anchors, the over-collection-exclusion
boundary, and the idempotency-namespacing). It does NOT block merge — the
maintainer iterates post-deployment from the "Revisit once in use" list.

- detection_tier_actual: none
- detection_tier_target: none

(No product-behavior bug surfaced during the build. Two self-inflicted test
authoring issues were caught at the green step and fixed before push: (1) the
existing `TestSupportedKinds_DevicePosture` cmd test on both connectors
hard-coded a single-element `SupportedKinds`; it was updated to a set assertion
that includes the new kind — an expected consequence of adding a sibling kind,
not a defect; (2) the `MaxSoftwarePerDevice` bound test needed zero-padded
fixture names so the stable sort applied the bound deterministically — a
test-authoring fix in MY test, not in shipped behavior. Both are build-time
authoring corrections, caught at the unit tier.)

## Decisions made

### D1 — A SEPARATE sibling kind `endpoint.software_inventory.v1`, NOT an extension of `endpoint.device_posture.v1` (THE clustering call)

- **Options considered:** (a) a new sibling kind
  `endpoint.software_inventory.v1`, shared across both connectors; (b) extend
  the slice-490 `endpoint.device_posture.v1` payload with a `software[]` array;
  (c) two per-connector kinds (`jamf.software_inventory.v1` +
  `intune.software_inventory.v1`).
- **Chosen:** (a), one shared sibling kind, built once in
  `connectors/mdm/swrecord`, normalized once in `connectors/mdm/swinventory`.
- **Rationale:** software inventory answers a DIFFERENT control question than
  device posture — patch-/vulnerability-management + asset inventory
  ("are managed endpoints running patched, authorized software?") vs.
  endpoint-posture compliance (encryption / screen-lock / OS / enrolment). The
  slice-555 spec is explicit: "It answers a different control question than
  device posture, so it warrants its own evidence kind, not an extension of
  `endpoint.device_posture.v1`." Option (b) was rejected because folding a
  high-volume app list into the posture summary would (i) re-introduce exactly
  the over-collection the slice-490 posture kind was scoped to avoid (P0-490-3),
  and (ii) couple two distinct control questions into one record, breaking the
  separate-anchor-set provenance. Option (c) was rejected for the same reason
  slice 490 D1 rejected per-connector posture kinds — the installed-software
  field set is genuinely identical at the inventory-summary altitude (name +
  version + identifier + install date), so a shared shape is not lossy; the
  `source_mdm` discriminator preserves per-vendor provenance. The shared-package
  layout (`connectors/mdm/{swinventory,swrecord}`) mirrors the slice-490
  `{devposture,devrecord}` split exactly.

### D2 — Software-field allow-list + per-device roll-up (THE load-bearing over-collection guard, threat-model I / P0-555)

- **Decision:** the record is **one per managed device** carrying a bounded
  `software[]` list; each item carries the app **name** (required) + optional
  **version** + optional bundle/package **identifier** + optional **install
  date** ONLY. It carries **no** file path, **no** per-user app-usage telemetry,
  **no** license key, **no** device contents, and **no** owner contact detail.
- **Granularity rationale (per-device roll-up, not per-app records):** per-app
  records would multiply the ledger row count by the per-device app count (a
  fleet of 200 devices × ~150 apps = ~30k rows per run) and break the clean
  per-device idempotency model the slice-490 family established. One record per
  device, keyed `(mdm, device_id, hour)`, is the right granularity: it dedupes
  same-device same-hour re-runs into one row (like posture) and keeps the
  software list joinable to the device's posture record by `device_id`.
- **Field-set rationale:** version is the load-bearing field (known-vulnerable-
  version detection); name + identifier make a version meaningful; install date
  is a useful-when-present descriptive optional. File path / usage / license key
  are the canonical over-collection vectors an auditor's patch-management
  question does not need, so they are excluded.
- **Enforcement:** structural, at three layers (the slice-490 gold standard).
  (1) The shared `swinventory.SoftwareItem` + the per-vendor
  `RawSoftwareItem` Go types have no field for a path / usage / license key — a
  leak would be a compile error. (2) The vendor clients request ONLY the
  software-relevant data at the API boundary: Jamf asks for the inventory
  sections `GENERAL, APPLICATIONS` (never `USER_AND_LOCATION`, never the GPS
  `location` section); Intune `$select`s `id,displayName,version` on
  `detectedApps` and `$expand`s `managedDevices($select=id)` (never
  `sizeInByte`, never `deviceName`, never `userPrincipalName`). (3) The record
  builder allow-lists payload keys AND per-item keys. Tests:
  `TestBuild_PayloadAndItemsCarrySoftwareFieldsOnly` (swrecord) asserts only
  allow-listed top-level + per-item keys + no banned substring; the client
  tests (`TestClient_ListSoftware_DecodesSoftwareFieldsOnly` /
  `TestClient_ListDetectedApps_InvertsToDeviceCentricSoftwareOnly`) feed a
  fixture deliberately carrying `path`/`sizeInByte`/`licenseKey`/`deviceName`/
  `userPrincipalName` and assert neither the request asked for them nor the
  decoded shape can hold them.
- **Bound:** `MaxSoftwarePerDevice = 500`, applied after a stable sort, is a
  defensive over-collection ceiling (threat-model D, parallel to the slice-490
  device-page bound).

### D3 — `x-default-scf-anchors = [VPM-04, AST-03]` (maintainer recheck)

- **Decision:** the schema defaults to `VPM-04` (Vulnerability Remediation
  Process) and `AST-03` (Asset Inventory).
- **Rationale:** software inventory is the direct evidence substrate for
  patch-/vulnerability-management (VPM-04 — "is the installed version patched?")
  and asset/software inventory (AST-03 — "what is installed on our managed
  assets?"). Both anchors exist in the sample SCF catalog
  (`migrations/fixtures/scf-sample.json`). This is deliberately a DIFFERENT
  anchor set than the slice-490 posture kind's `[END-04, CFG-02]`, which is the
  schema-level signal that this kind answers a different control question (D1).
  Flagged for maintainer accuracy recheck — if a deployment prefers a dedicated
  unauthorized-software/CM anchor, the default is overridable per the schema's
  `x-default-scf-anchors` and the `--software-control` CLI flag.

### D4 — Read-only, SAME credential as `run`; new `run-software` subcommand; pull-only honest interval (P0-555 anti-criteria)

- **Decision:** the software read uses the **same** read-only credential as the
  posture `run` (Jamf read-only API role; Intune
  `DeviceManagementManagedDevices.Read.All` — the `detectedApps` endpoint is
  read under that same device-management permission). No new MDM scope, no
  write/management permission, no widening of the platform-side wire (push
  only — invariant #3). A separate `run-software` subcommand keeps the two
  evidence kinds independently schedulable; `profiles_supported = [pull]`,
  recommended 24h, named "operator-scheduled — NOT continuous monitoring."
- **Rationale:** honors all three slice-555 P0 anti-criteria (no extra scope,
  read-only only, push-only wire) and the slice-490 remote-wipe guard
  unchanged. v0 reads the first bounded page per MDM; cursor pagination across a
  large fleet is the same documented follow-on as slice 490.

### D5 — Idempotency-key namespacing by evidence-kind prefix

- **Decision:** `SoftwareInventoryKey =
sha256("endpoint.software_inventory|<mdm>/<device_id>|<hour>")`, distinct from
  the posture `DevicePostureKey` prefix.
- **Rationale:** the same device in the same hour produces BOTH a posture record
  and a software record; without a kind prefix the two would collide on the same
  idempotency key and the ledger would drop one. The prefix namespaces them.
  Proven by `TestSoftwareInventoryKey_NamespacedFromPosture`.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** none (no shipped-behavior bug surfaced).
- **detection_tier_target:** none.

## Revisit once in use (maintainer)

- **Anchor accuracy:** confirm `VPM-04` + `AST-03` are the right SCF anchors for
  installed-software inventory, or refine toward a dedicated CM /
  unauthorized-software anchor if one is added to the catalog.
- **Per-device bound:** `MaxSoftwarePerDevice = 500` is a v0 ceiling. A device
  with a genuinely larger inventory truncates after a stable sort; raise the
  bound (schema-additive, no break) if a real adopter needs the full list.
- **Pagination:** v0 reads one bounded page per MDM (Jamf `page-size=200`,
  Intune `$top=200`); a large fleet needs cursor pagination — the same
  follow-on as slice 490.
- **Intune device-centric inversion:** the `detectedApps` graph is app-centric;
  v0 reads the first page of apps and inverts to per-device. A fleet whose app
  catalog exceeds one page would miss apps that only appear on later pages —
  cursor pagination resolves this with the bound above.

## Spillover slices filed

- **590** — MDM software-inventory cursor pagination (Jamf + Intune full-fleet /
  full-app-catalog reads beyond the first bounded page). Parent: 555.
