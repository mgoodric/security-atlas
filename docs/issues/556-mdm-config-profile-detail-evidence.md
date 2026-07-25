# 556 — MDM connectors: configuration-profile detail evidence (Jamf + Intune)

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + secret-redaction boundary)
**Status:** `blocked` (depends on #490 — base MDM connectors — merged first)

## Narrative

Slice 490 shipped the Jamf + Intune MDM connectors with a posture-summary surface
(`endpoint.device_posture.v1`) that reports WHETHER a device is compliant, but not
WHICH configuration profiles / compliance policies are assigned or what they
require. Auditors increasingly ask for the configuration baseline itself ("show
the screen-lock policy that is enforced", "show the disk-encryption configuration
profile"), which maps to SCF `CFG-02` (Secure Baseline Configurations) /
`CFG-04` (Configuration Change Control) at a finer grain than the posture
verdict.

This slice adds a configuration-profile / compliance-policy **detail** surface
(Jamf configuration profiles; Intune device-compliance + configuration policies).
The hard JUDGMENT it owns: the field set (profile name + type + assigned scope +
key settings?), and the **secret-redaction boundary** — configuration profiles
can embed certificates, Wi-Fi PSKs, VPN shared secrets, and credential payloads
that must NEVER enter an evidence record.

## Dependencies

- **#490** (base MDM connectors) — must merge first; reuses the per-vendor
  read-only auth + shared `connectors/mdm/` patterns.

## Anti-criteria (P0)

- Does NOT collect certificate private keys, Wi-Fi PSKs, VPN shared secrets, or
  any credential payload embedded in a profile.
- Does NOT widen the platform-side wire — push only (invariant #3).
- Does NOT require a write/management MDM scope (read-only only).

Parent: #490.
