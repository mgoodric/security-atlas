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
 * native `<a href download>` GET — the browser raises a `download` event.
 *
 * The id is percent-encoded so a malformed/hostile id cannot break out of
 * the path segment.
 */
export function oscalExportDownloadURL(periodID: string): string {
  return `/api/audits/${encodeURIComponent(periodID)}/oscal-export`;
}

/**
 * Builds the deterministic download filename for a period's OSCAL signed
 * bundle — `oscal-bundle-<period-id>-<frozen-date>.json` (or, when the
 * frozen date is absent/malformed, `oscal-bundle-<period-id>.json`).
 *
 * This MIRRORS the server-side `downloadFilename`/`frozenDate` logic in
 * `internal/api/oscalexport/handler.go`. It exists because the anchor's
 * `download` attribute, for a SAME-ORIGIN download, takes precedence over
 * the server's `Content-Disposition` filename: a BARE `download`
 * attribute makes the browser derive the name from the URL's last path
 * segment (`oscal-export` → sniffed `oscal-export.txt`). Setting the
 * `download` attribute to this VALUE pins the suggested filename to the
 * deterministic name regardless of same-origin disposition handling
 * (AC-2/AC-3). The BFF still sets the matching `Content-Disposition`
 * header (the authority for non-anchor / cross-origin consumers); this
 * keeps the anchor and the header in lock-step.
 *
 * `frozenAt` is the period's RFC-3339 freeze horizon (the `frozen_at`
 * wire field); only its leading `YYYY-MM-DD` is used, and a malformed /
 * empty value omits the date segment rather than guessing one.
 */
export function oscalExportDownloadFilename(
  periodID: string,
  frozenAt: string | null | undefined,
): string {
  const date = frozenDateSegment(frozenAt);
  return date
    ? `oscal-bundle-${periodID}-${date}.json`
    : `oscal-bundle-${periodID}.json`;
}

/**
 * Extracts the leading `YYYY-MM-DD` from an RFC-3339 timestamp. Returns
 * `""` when the input is empty/absent or does not start with a 10-char
 * date with hyphens at positions 4 and 7 — the caller then omits the date
 * segment. Mirrors `frozenDate` in the Go handler.
 */
function frozenDateSegment(frozenAt: string | null | undefined): string {
  if (!frozenAt || frozenAt.length < 10) return "";
  const date = frozenAt.slice(0, 10);
  if (date[4] !== "-" || date[7] !== "-") return "";
  if (/[ :TZ]/.test(date)) return "";
  return date;
}
