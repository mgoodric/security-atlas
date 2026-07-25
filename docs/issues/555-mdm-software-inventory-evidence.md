# 555 — MDM connectors: software-inventory evidence (Jamf + Intune)

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + over-collection boundary)
**Status:** `blocked` (depends on #490 — base MDM connectors — merged first)

## Narrative

Slice 490 shipped the Jamf + Intune MDM connectors with a single pull-only
posture-summary surface (`endpoint.device_posture.v1`). It **deliberately
excluded the installed-application inventory** (Jamf `APPLICATIONS` section /
Intune `detectedApps`) as the load-bearing over-collection guard (P0-490-3): a
full app inventory is high-volume, high-PII-adjacency data that the
posture-summary control question does not need.

Software-inventory evidence is nonetheless a real, separate SOC 2 / ISO demand —
unauthorized-software detection (CM / asset-management controls), license
compliance, and known-vulnerable-version detection. It answers a **different
control question** than device posture, so it warrants its own evidence kind, not
an extension of `endpoint.device_posture.v1`.

This slice adds a software-inventory **summary** surface. The hard JUDGMENT it
owns: the field set (app name + version + publisher only? counts only? a bounded
top-N?), whether to emit per-app records or one per-device roll-up, and the
over-collection boundary (no file paths, no per-user app-usage telemetry, no
license keys).

## Dependencies

- **#490** (base MDM connectors) — must merge first; reuses
  `connectors/mdm/{devposture,devrecord,idem}` patterns and the per-vendor
  read-only auth.

## Anti-criteria (P0)

- Does NOT collect file paths, per-user usage telemetry, or license keys.
- Does NOT widen the platform-side wire — push only (invariant #3).
- Does NOT require a write/management MDM scope (read-only only).

Parent: #490.
