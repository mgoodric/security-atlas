// #1802 — the "audited under" report (#1801) and the overlay edge that
// closes one of its rows (#1797).
//
// GET /v1/mapping-overlays/audited-under lists, per active control, each
// criterion the control was tested under that its SCF anchor does not
// reach through catalog plus overlay edges. POST /v1/mapping-overlays adds
// the overlay edge criterion -> anchor; the next read no longer lists the
// row. Types mirror `gapWire` / `candidateWire` in
// internal/api/mappingoverlays/audited_under.go and `createReq` in
// internal/api/mappingoverlays/handlers.go.
//
// Client-safe: the page imports it. The BFF routes live under
// app/api/mapping-overlays.

import { APIError } from "./base";
import { bffControlFetch } from "./_shared";

export interface CandidateAnchor {
  anchor_id: string;
  anchor_code: string;
  anchor_title: string;
  relationship_type: string;
  strength: number;
}

export interface AuditedUnderRow {
  control_id: string;
  control_bundle_id: string;
  control_title: string;
  anchor_id: string | null;
  anchor_code?: string;
  anchor_scf_release?: string;
  anchor_fit: string;
  anchor_fit_note?: string;
  framework: string;
  framework_version: string;
  requirement_id: string;
  requirement_code: string;
  requirement_title: string;
  tested_under_note?: string;
  candidate_anchors: CandidateAnchor[];
}

/** A tested-under reference that names no requirement in its framework's
 *  current version (#2324): a typo, a withdrawn code, or an unknown
 *  framework. Mirrors `unresolvedWire`. */
export interface UnresolvedReference {
  control_id: string;
  control_bundle_id: string;
  control_title: string;
  system: string;
  code: string;
  tested_under_note?: string;
}

export interface AuditedUnderReport {
  rows: AuditedUnderRow[];
  count: number;
  /** Rows before the cap; more than `count` when `truncated`. */
  total: number;
  limit: number;
  /** True when rows or unresolved_references were cut at `limit`. */
  truncated: boolean;
  unresolved_references: UnresolvedReference[];
  /** Unresolved references before the cap. */
  unresolved_count: number;
  candidate_scf_release: string | null;
}

/** The overlay POST body. Every field is required except the two
 *  versions, which the platform defaults to the current ones. */
export interface NewOverlayEdge {
  framework: string;
  framework_version?: string;
  requirement_code: string;
  scf_release?: string;
  anchor_code: string;
  relationship_type: string;
  strength: number;
  rationale: string;
  source_ref: string;
}

/** The fields the BFF forwards to POST /v1/mapping-overlays. */
export const OVERLAY_EDGE_FIELDS = [
  "framework",
  "framework_version",
  "requirement_code",
  "scf_release",
  "anchor_code",
  "relationship_type",
  "strength",
  "rationale",
  "source_ref",
] as const;

/** The STRM relationships an overlay edge may assert, in the order the
 *  form offers them (internal/mappingoverlay/store.go `relationships`). */
export const OVERLAY_RELATIONSHIPS = [
  "intersects_with",
  "subset_of",
  "superset_of",
  "equal",
] as const;

export function fetchAuditedUnder(): Promise<AuditedUnderReport> {
  return bffControlFetch<AuditedUnderReport>(
    "/api/mapping-overlays/audited-under",
  );
}

const FRAMEWORK_SLUG_RE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;

/** isFrameworkSlug reports whether `v` is shaped like a framework slug
 *  ("soc2", "iso27001", "nist-csf"). The BFFs refuse any other value
 *  rather than forward it upstream. */
export function isFrameworkSlug(v: string): boolean {
  return FRAMEWORK_SLUG_RE.test(v);
}

/** The CSV export link; `framework` narrows it like the page filter. */
export function auditedUnderExportURL(framework: string): string {
  const base = "/api/mapping-overlays/audited-under/export";
  return framework
    ? `${base}?framework=${encodeURIComponent(framework)}`
    : base;
}

/** addOverlayEdge posts the edge and resolves once the platform has
 *  stored it. A refusal throws an APIError carrying the platform's
 *  message and status (403 role, 409 duplicate, 400 invalid). */
export async function addOverlayEdge(edge: NewOverlayEdge): Promise<void> {
  const res = await fetch("/api/mapping-overlays", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(edge),
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON — keep the status line
    }
    throw new APIError(res.status, msg);
  }
}

/** rowKey identifies a report row: one control, one criterion. */
export function rowKey(
  r: Pick<AuditedUnderRow, "control_id" | "requirement_id">,
): string {
  return `${r.control_id}:${r.requirement_id}`;
}

export type GroupBy = "control" | "framework";

export interface RowGroup {
  key: string;
  label: string;
  detail: string;
  rows: AuditedUnderRow[];
}

/** groupRows splits rows by control or by framework version, keeping the
 *  report's order within and between groups. */
export function groupRows(rows: AuditedUnderRow[], by: GroupBy): RowGroup[] {
  const groups = new Map<string, RowGroup>();
  for (const r of rows) {
    const key =
      by === "control" ? r.control_id : `${r.framework}@${r.framework_version}`;
    let g = groups.get(key);
    if (!g) {
      g =
        by === "control"
          ? {
              key,
              label: r.control_title,
              detail: r.control_bundle_id,
              rows: [],
            }
          : { key, label: r.framework, detail: r.framework_version, rows: [] };
      groups.set(key, g);
    }
    g.rows.push(r);
  }
  return [...groups.values()];
}

/** frameworksOf lists the distinct framework slugs in report order. */
export function frameworksOf(rows: AuditedUnderRow[]): string[] {
  return [...new Set(rows.map((r) => r.framework))];
}

/** defaultSourceRef pre-fills the edge's source: the tested-under note
 *  (where the audit recorded it), else the criterion itself. */
export function defaultSourceRef(r: AuditedUnderRow): string {
  const note = r.tested_under_note?.trim();
  if (note) return note;
  return `Tested under ${r.framework} ${r.requirement_code}`;
}

/** edgeFor builds the overlay edge criterion -> the control's anchor, or
 *  null for an unanchored control (there is no anchor to map to). */
export function edgeFor(
  r: AuditedUnderRow,
  input: {
    relationship_type: string;
    strength: number;
    rationale: string;
    source_ref: string;
  },
): NewOverlayEdge | null {
  if (!r.anchor_id || !r.anchor_code) return null;
  return {
    framework: r.framework,
    framework_version: r.framework_version,
    requirement_code: r.requirement_code,
    scf_release: r.anchor_scf_release,
    anchor_code: r.anchor_code,
    relationship_type: input.relationship_type,
    strength: input.strength,
    rationale: input.rationale.trim(),
    source_ref: input.source_ref.trim(),
  };
}
