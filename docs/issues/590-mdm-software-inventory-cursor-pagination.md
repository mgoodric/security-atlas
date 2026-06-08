# 590 — MDM connectors: software-inventory cursor pagination (Jamf + Intune)

**Cluster:** Connectors
**Estimate:** S–M (1d)
**Type:** STANDARD
**Status:** `blocked` (depends on #555 — software-inventory evidence — merged first)

## Narrative

Slice 555 added installed-software inventory evidence
(`endpoint.software_inventory.v1`) to the Jamf + Intune MDM connectors, reading
the **first bounded page** per MDM (Jamf `page-size=200`, Intune `$top=200`),
mirroring the slice-490 posture v0 page bound (threat-model D). A fleet larger
than one page — or, for Intune, an app catalog larger than one page of
`detectedApps` — is silently truncated:

- **Jamf:** `GET /api/v1/computers-inventory` is page-cursored
  (`page` / `page-size` / `totalCount`); v0 reads page 0 only.
- **Intune:** `GET /deviceManagement/detectedApps` is `@odata.nextLink`-cursored;
  v0 reads the first page only. Because slice 555 inverts the app-centric graph
  into a device-centric shape, a device's apps that appear only on later pages
  are missed.

This slice adds cursor pagination to the software read on both connectors so a
large-fleet / large-catalog adopter gets a complete inventory, bounded by a
run-timeout + a max-page cap (no unbounded loop — threat-model D). The
per-device bound (`swinventory.MaxSoftwarePerDevice`) stays.

## Scope

- Jamf: loop `page` until `page * page-size >= totalCount` (or the max-page cap).
- Intune: follow `@odata.nextLink` until absent (or the max-page cap), then
  invert the full app set to device-centric.
- Read-only only; same credential as slice 555; push-only wire (invariant #3).
- A test asserts a two-page fixture yields the union of both pages.

## Anti-criteria (P0)

- Does NOT widen the collected field set beyond slice 555's allow-list (name +
  version + identifier + install date).
- Does NOT remove the per-device `MaxSoftwarePerDevice` ceiling.
- Does NOT introduce an unbounded read loop — a max-page cap + run timeout bound
  the work.

## Dependencies

- **#555** (MDM software-inventory evidence) — must merge first; extends its
  `connectors/{jamf,intune}/internal/devices/swclient.go` read path.

Parent: #555.
