# 457 — Browser download surface for the OSCAL signed-export bundle

**Cluster:** Feature
**Estimate:** 2-3d (M)
**Type:** JUDGMENT
**Status:** `ready`
**Priority:** P2

> Surfaced during slice 423 (OSCAL signed-export download-chain e2e).

## Narrative

**WHY.** Slice 423 set out to drive the OSCAL signed-export as a
browser DOWNLOAD chain end-to-end (`/audits` → per-period detail →
click export → `page.waitForEvent("download")` with a
`Content-Disposition` attachment), mirroring the board-pack PDF export
(`web/e2e/board-pack-export-e2e.spec.ts`, slice 388). While building it,
the implementing agent found that the browser download surface the spec
assumed **does not exist in v1**:

1. The OSCAL export handler
   (`internal/api/oscalexport/handler.go`) responds
   `Content-Type: application/json` with a JSON ENVELOPE (manifest +
   slice-413 signature + the four OSCAL members) — NOT a
   `Content-Disposition: attachment` byte stream. It raises no browser
   `download` event.
2. There is no BFF route under `web/app/api/**` forwarding to
   `POST /v1/audit-periods/{id}/oscal-export`. The only audit-period
   export BFF (`web/app/api/admin/audit-periods/export/route.ts`,
   slice 139) is the CSV/JSON/XLSX DATA dump, explicitly distinct from
   the cosigned OSCAL bundle (the `page.tsx` P0-A-AP-1 comment).
3. There is no per-period detail page: `/audits/[id]` does not exist
   (the slice-184 banner on `/audits` says the detail view "is coming
   in a future slice"), and the `/audits` toolbar carries a slice-217
   disclosure `<span>`, not an export trigger.

Slice 423 (scope: a 0.5d e2e test, "does NOT add a new export feature")
correctly declined to build that feature and instead asserted the
reachable boundary — an in-page `fetch` POST to the export endpoint,
asserting the JSON envelope + `Content-Type` + the slice-413 signed
manifest (`web/e2e/oscal-export-e2e.spec.ts`). The literal
`waitForEvent("download")` form of slice 423's AC-2 is deferred here,
against a real download surface, rather than faked (a fabricated
download event the product does not emit is the dishonest-test
anti-pattern this project rejects — `web/e2e/README.md`).

**WHAT.** Ship the operator-facing browser download for the OSCAL
signed bundle, then land the `waitForEvent("download")` e2e against it:

1. A per-period detail page (or a `/audits` toolbar action gated to a
   frozen period) that exposes an "Export OSCAL bundle" affordance —
   superseding the slice-217 disclosure `<span>`, which becomes the
   working action it currently signposts.
2. A BFF download route (e.g. `GET /api/audits/[id]/oscal-export`) that
   forwards the bearer to `POST /v1/audit-periods/{id}/oscal-export`
   and packages the JSON envelope into a downloadable artifact (a
   `.json` bundle, or a zipped bundle of the four members + manifest +
   `.sig` sidecar) with a `Content-Disposition: attachment` header —
   the slice-139 streaming-BFF pattern.
3. An e2e (extend `web/e2e/oscal-export-e2e.spec.ts` or a sibling) that
   drives the click → `page.waitForEvent("download")` → asserts the
   suggested filename + `Content-Type` + that the bundle carries the
   slice-413 signing manifest / `.sig` sidecar.

## Scope discipline

- This is the FEATURE slice 423 deliberately did not build. It does NOT
  change the platform export handler's wire contract (the JSON envelope
  stays; the BFF packages it for download).
- It does NOT cryptographically verify the signature in the browser —
  that remains the Go integration tier (slice 425 / P0-423-1).
- Whether the download is a single `.json` envelope vs. a zipped
  multi-file bundle (members + manifest + detached `.sig`) is the
  JUDGMENT call for the implementing agent; record it in the decisions
  log.

## Acceptance criteria

- [ ] **AC-1.** A frozen audit period exposes a working "Export OSCAL
      bundle" affordance in the browser (superseding the slice-217
      disclosure), gated to operators.
- [ ] **AC-2.** A BFF route forwards the bearer to
      `POST /v1/audit-periods/{id}/oscal-export` and returns a
      downloadable artifact with a `Content-Disposition: attachment`
      header and a deterministic filename.
- [ ] **AC-3.** A Playwright e2e drives the click and asserts
      `page.waitForEvent("download")` fires, the suggested filename, and
      the `Content-Type` the BFF sets.
- [ ] **AC-4.** The downloaded artifact carries the slice-413 signing
      manifest (and `.sig` sidecar if the zipped shape is chosen).
- [ ] **AC-5.** The slice-217 disclosure spec assertion in
      `web/e2e/audits-list.spec.ts` is updated to reflect the now-working
      affordance (not silently deleted — the honesty property migrates).

## Dependencies

- **#423** (OSCAL signed-export e2e at the wire boundary) — the spec
  this completes the download leg of.
- **#413** (cosign-kms + embedded signing) — the signed bundle shape.
- **#217** (OSCAL export disclosure) — the disclosure this supersedes.

## Notes for the implementing agent

- `web/app/api/admin/audit-periods/export/route.ts` (slice 139) is the
  canonical streaming-BFF + `Content-Disposition` passthrough pattern to
  mirror.
- `web/e2e/board-pack-export-e2e.spec.ts` (slice 388) is the canonical
  `waitForEvent("download")` template.
- The handler's JSON envelope shape is `exportResponse` in
  `internal/api/oscalexport/handler.go`; the signature manifest is
  `oscal.Signature` (`internal/oscal/sign.go`).
