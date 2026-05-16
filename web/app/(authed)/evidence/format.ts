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
