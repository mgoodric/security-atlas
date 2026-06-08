# 590 — MDM software-inventory cursor pagination (Jamf + Intune): JUDGMENT-light decisions log

Slice type: STANDARD (JUDGMENT-light). This file records the two subjective
build-time calls the spec left to the implementer — the **page size** and the
**cursor-state shape / termination + max-page cap** — plus the explicit note
that the slice-555 over-collection allow-list and evidence kind are reused
UNCHANGED. It does not block merge.

- detection_tier_actual: none
- detection_tier_target: none

(No shipped-behavior bug surfaced during the build. The only build-time
correction was to the slice-555 Jamf software test server: it served the same
static fixture for every page, which a page-walk would loop on. It was made
page-aware — fixture for `page=0`, an empty terminator page for later pages —
mirroring the real Jamf cursor. That is a test-fixture authoring fix for a test
the new loop changed the assumptions of, not a defect in shipped behavior; caught
at the unit tier. The Intune static fixtures carry no `@odata.nextLink`, so the
nextLink walk terminates after one page and those tests needed no change.)

## What is REUSED UNCHANGED (no schemaregistry / kind / record / guard change)

- **Evidence kind `endpoint.software_inventory.v1`** — unchanged. No
  schemaregistry edit, no new kind, no migration. The schema-drift bijection
  tests (`internal/api/schemaregistry`, `internal/control`) pass unchanged.
- **Over-collection allow-list** — unchanged. Jamf still requests only the
  `GENERAL + APPLICATIONS` sections; Intune still `$select`s `id,displayName,
version` and `$expand`s `managedDevices($select=id)`. The `apiSoftwarePage` /
  `apiDetectedAppsPage` decode structs gained ONLY the page-control fields
  (`totalCount` on Jamf, `@odata.nextLink` on Intune) — neither is a collected
  evidence field; both are page cursors. No app data field was widened. The
  slice-555 over-collection tests
  (`TestClient_ListSoftware_DecodesSoftwareFieldsOnly`,
  `TestClient_ListDetectedApps_InvertsToDeviceCentricSoftwareOnly`) still pass.
- **`MaxSoftwarePerDevice = 500`** per-device ceiling — unchanged (applied in
  `swinventory.Normalize`, untouched by this slice). P0-590 anti-criterion held.
- **Record builder + normalizer** (`connectors/mdm/{swrecord,swinventory}`) —
  untouched.

## Decisions made

### D1 — Page size: REUSE the existing `pageLimit = 200` (no new page-size constant)

- **Options considered:** (a) reuse the slice-490/555 `pageLimit = 200` constant
  already used for the first-page read; (b) introduce a separate, larger
  software-page size.
- **Chosen:** (a). Jamf `page-size = 200`, Intune `$top = 200`, both via the
  existing `pageLimit` constant — identical to the slice-555 first-page read.
- **Rationale:** the spec narrative names exactly these values ("Jamf
  `page-size=200`, Intune `$top=200`"). 200 is a well-mannered page size for both
  APIs (Jamf computers-inventory and Graph `detectedApps` both tolerate it) and
  keeps a single page's memory footprint bounded. There is no reason to diverge
  the software page size from the posture page size, so no new constant was
  added — the change is purely the _loop around_ the existing single-page read.

### D2 — Cursor-state shape + termination + max-page cap (the no-unbounded-loop guard, P0-590 / threat-model D)

- **Jamf shape — numeric `page` increment with a `totalCount` terminator.**
  `GET /api/v1/computers-inventory` is offset-cursored (`page` / `page-size` /
  `totalCount`). The loop increments `page` from 0 and stops when EITHER
  `gathered >= totalCount` (the reported population is fully covered) OR the page
  returns zero results (a defensive terminator for an older Jamf that omits
  `totalCount`, where `totalCount<=0` falls through to the empty-page stop) OR
  the max-page cap is hit.
- **Intune shape — opaque `@odata.nextLink` follow.** `GET
/deviceManagement/detectedApps` is `@odata.nextLink`-cursored (an opaque
  server-issued skiptoken). The first page is built from the
  `$select`/`$expand`/`$top` query; each subsequent page is the server's
  `@odata.nextLink` **absolute URL requested verbatim** (a new
  `getJSONAbsolute` client helper issues the absolute-URL GET; the existing
  `getJSON` is refactored to share its body via `doGetJSON`). The loop stops when
  `@odata.nextLink` is absent OR the max-page cap is hit. Walking every page is
  REQUIRED because slice 555 inverts the app-centric graph into a device-centric
  shape — a device's apps that fall on a later page would be silently dropped if
  the walk stopped early (the exact bug 555 left open; asserted by the d-3
  page-2-only-device assertion in `TestClient_ListDetectedApps_WalksNextLink`).
- **Max-page cap = `maxSoftwarePages = 50` per connector.** At `pageLimit = 200`
  this bounds a single run to 50 × 200 = 10,000 source records (computers on
  Jamf, detectedApps on Intune). The cap is the explicit P0-590 / threat-model-D
  guard: a hostile or non-converging `totalCount` (Jamf) or non-terminating
  `nextLink` chain (Intune) cannot drive an unbounded read loop. Hitting the cap
  is NOT an error — the connector returns what it gathered, matching the
  connector "best-effort within bounds, register-per-run" posture. 10,000 source
  records comfortably exceeds the target solo-leader fleet (50–150 endpoints) and
  a realistic per-fleet app catalog, so the cap is a safety ceiling, not a
  routine truncation point.
- **Honest interval unchanged:** `profiles_supported = [pull]`, operator-
  scheduled (recommended 24h), NOT continuous monitoring — same as slice 555.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** none (no shipped-behavior bug surfaced).
- **detection_tier_target:** none.

## Tests added (the cursor loop)

- `TestClient_ListSoftware_WalksAllPages` (Jamf): a stateful two-page server
  (`page=0` and `page=1`, `totalCount=2`); asserts the result is the UNION of
  both pages (device 501 + 502) AND that exactly two pages were served (the loop
  terminates on `totalCount`, never requesting page 2).
- `TestClient_ListDetectedApps_WalksNextLink` (Intune): a stateful two-page
  server chained by an absolute `@odata.nextLink`; asserts the inverted
  device-centric result is the union, including device `d-1` whose apps span BOTH
  pages and device `d-3` that appears ONLY on page 2, AND that exactly two pages
  were served (the loop terminates when `nextLink` is absent).

## Revisit once in use (maintainer)

- **Max-page cap:** `maxSoftwarePages = 50` (10,000 source records at 200/page) is
  a v0 safety ceiling, not a tuned limit. If a genuine large-enterprise adopter's
  fleet or app catalog exceeds it, raise the cap (a one-constant, non-breaking
  change) or pair it with a run-timeout context deadline at the cmd layer.

## Spillover slices filed

- none (the slice was self-contained — the cursor loop fit entirely within the
  slice-555/490 `swclient` read path).
