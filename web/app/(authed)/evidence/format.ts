// Slice 099 — pure formatter helpers for the /evidence list view.
//
// Cell-rendering logic that we want to unit-test without React. The
// page imports these as plain functions; the test file exercises them
// against the slice-013 `evidenceWire` shape so any wire-shape drift
// shows up as a test failure first.

/**
 * The hash-prefix length used in the table cell (per slice 099 P0-A2
 * "8-char prefix only — full hash on copy-click"). Centralised here so
 * the page + tests stay in lockstep if the prefix length changes.
 */
export const HASH_PREFIX_LEN = 8;

/**
 * Render the 8-char hash prefix used in the table cell. Returns an
 * empty string when the input is empty so the cell renders as a
 * single dash via the `dashOr` helper.
 */
export function hashPrefix(content_hash: string): string {
  if (!content_hash) return "";
  return content_hash.slice(0, HASH_PREFIX_LEN);
}

/**
 * Render the scope cell value. Today the wire shape only carries the
 * UUID of the scope cell (or null when the record has no scope). We
 * render a shortened UUID so the cell stays narrow; the full UUID
 * lives in the row drawer.
 */
export function scopeLabel(scope_cell: string | null): string {
  if (!scope_cell) return "—";
  // First 8 chars of the UUID are enough to recognise + click into the
  // row drawer where the full value is shown.
  return `${scope_cell.slice(0, 8)}…`;
}

/**
 * Summarise the provenance JSONB for the source-attribution cell. The
 * platform stores arbitrary connector-supplied JSON here, but the
 * known shape always includes `actor_type` + `actor_id`. We render
 * `<actor_type> · <actor_id>` when both are present; fall back to a
 * dash when neither is.
 */
export function sourceSummary(source: Record<string, unknown> | null): string {
  if (!source) return "—";
  const actorType =
    typeof source.actor_type === "string" ? source.actor_type : "";
  const actorID = typeof source.actor_id === "string" ? source.actor_id : "";
  if (actorType && actorID) return `${actorType} · ${actorID}`;
  if (actorType) return actorType;
  if (actorID) return actorID;
  return "—";
}

/**
 * Pretty-print a record's full payload for the row drawer.
 */
export function prettyJSON(value: unknown): string {
  return JSON.stringify(value, null, 2);
}

/**
 * Render an RFC3339 timestamp for the cell. Today we render the
 * timestamp verbatim — a future slice may localise to the user's
 * timezone per the slice-097 preference. The function is here so the
 * timezone work has a single seam to extend.
 */
export function observedAtLabel(observed_at: string): string {
  return observed_at;
}

/**
 * Slice 236 — render the filter-row meta-count string.
 *
 * The /evidence page surfaces this in the filter-pill row so the
 * operator can distinguish three states the previous "Showing N
 * records" string conflated:
 *
 *   1. ledger is empty tenant-wide  → "No records in ledger yet"
 *   2. filters narrowed to zero     → "Showing 0 of M records"
 *   3. filters narrow a non-empty
 *      ledger to a smaller window   → "Showing N of M records"
 *
 * Per the slice 236 spec (AC-5), the empty-ledger branch matches a
 * `total === 0` upstream count regardless of how many rows the filter
 * predicates returned (they trivially also return zero). The "of M"
 * suffix only renders when `total > 0`.
 *
 * The function takes both inputs as plain `number` for testability —
 * the page binds `records.length` and `EvidenceListResponse.total`
 * directly. Negative inputs are not expected from the wire shape but
 * are clamped to zero defensively.
 */
export function recordCountMeta(shown: number, total: number): string {
  const safeShown = Math.max(0, shown);
  const safeTotal = Math.max(0, total);
  if (safeTotal === 0) return "No records in ledger yet";
  return `Showing ${safeShown} of ${safeTotal} records`;
}

/**
 * Slice 236 — render the page-title subtitle's ledger-context suffix.
 *
 * The mockup (`Plans/_archive/mockups/evidence.html` line 111) shows
 * `append-only · 14,712 records · 7 connectors`. The connectors count
 * is deferred to a future slice (P0-236-1 — separate connector
 * inventory endpoint), so this slice surfaces only the records count.
 *
 * When the ledger is empty the suffix collapses to empty string so the
 * page doesn't render a noisy "0 records" line — the meta-row's
 * "No records in ledger yet" carries the operator signal in that case.
 */
export function ledgerSubtitleSuffix(total: number): string {
  const safeTotal = Math.max(0, total);
  if (safeTotal === 0) return "";
  return `append-only · ${safeTotal} records`;
}
