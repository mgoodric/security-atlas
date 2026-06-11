// Slice 457 — OSCAL signed-export download surface (supersedes the
// slice-217 future-state disclosure).
//
// Slice 217 replaced a permanently-disabled "Export OSCAL bundle" button
// with a non-button `<span>` disclosing that the capability "ships with
// the per-period detail view". Slice 457 ships the working download: a
// per-frozen-period link to the BFF download route
// (`web/app/api/audits/[id]/oscal-export/route.ts`), which forwards the
// bearer to `POST /v1/audit-periods/{id}/oscal-export:download` and
// streams the signed bundle back as a `Content-Disposition: attachment`
// artifact. The disclosure's signpost becomes the working action it
// signposted.
//
// Constants are exported so:
//   * Vitest pins the testid tokens + the URL shape without rendering the
//     page.
//   * Playwright (AC-3) asserts the click drives a `download` event with
//     the platform's deterministic filename.
//   * A future copy / route rewrite changes one place; both tests follow.

/**
 * Test-id token surfaced on the per-frozen-period download link that
 * replaces the slice-217 disclosure `<span>`. Pinned by
 * `oscal-export.test.ts`.
 */
export const OSCAL_EXPORT_DOWNLOAD_TESTID = "audits-oscal-export-download";

/**
 * Test-id token on the toolbar note that points operators at the
 * per-row download affordance (the list-level home of the action).
 */
export const OSCAL_EXPORT_TOOLBAR_TESTID = "audits-oscal-export-toolbar";

/**
 * Visible label on the per-period download link. Sentence-cased action
 * verb ("Export OSCAL bundle") — the affordance the slice-217 disclosure
 * promised, now live.
 */
export const OSCAL_EXPORT_DOWNLOAD_LABEL = "Export OSCAL bundle";

/**
 * Toolbar note copy. Frozen periods carry a per-row download link; the
 * note tells the operator where the action lives. Names the capability
 * ("frozen period"), not a slice number (slice 184 D3 precedent).
 */
export const OSCAL_EXPORT_TOOLBAR_NOTE =
  "Export the signed OSCAL bundle (SSP/AP/AR/POA&M) from any frozen period below.";

/**
 * Builds the BFF download URL for a period's OSCAL signed bundle. A
 * native `<a href download>` GET — the browser raises a `download` event
 * and saves the file with the platform's deterministic filename.
 *
 * The id is percent-encoded so a malformed/hostile id cannot break out of
 * the path segment.
 */
export function oscalExportDownloadURL(periodID: string): string {
  return `/api/audits/${encodeURIComponent(periodID)}/oscal-export`;
}
