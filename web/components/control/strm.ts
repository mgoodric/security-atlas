// Slice 041 — STRM relationship-type styling.
//
// The DB enum `strm_relationship_type` has five values (equal, subset_of,
// superset_of, intersects_with, no_relationship — `internal/db/dbx/models.go`).
// The UCF traversal SQL filters out `no_relationship`, so the coverage
// endpoint returns at most four. This map covers all five and ships a
// neutral fallback so any future enum value renders without crashing or
// being silently dropped (slice anti-criterion: no fabricated mappings —
// render exactly what the backend returns).

import { enumLabel } from "@/lib/labels/enum-labels";

export type StrmStyle = {
  // Tailwind classes for the small badge in the coverage table.
  badge: string;
  // SVG stroke color for the UCF mini-viz edge.
  stroke: string;
  // Display label (#2083), from the shared label module.
  label: string;
};

const KNOWN: Record<string, StrmStyle> = {
  equal: {
    badge: "bg-emerald-50 text-emerald-700",
    stroke: "rgb(16 185 129)",
    label: enumLabel("StrmRelationshipType", "equal"),
  },
  subset_of: {
    badge: "bg-sky-50 text-sky-700",
    stroke: "rgb(14 165 233)",
    label: enumLabel("StrmRelationshipType", "subset_of"),
  },
  superset_of: {
    badge: "bg-violet-50 text-violet-700",
    stroke: "rgb(139 92 246)",
    label: enumLabel("StrmRelationshipType", "superset_of"),
  },
  intersects_with: {
    badge: "bg-amber-50 text-amber-700",
    stroke: "rgb(245 158 11)",
    label: enumLabel("StrmRelationshipType", "intersects_with"),
  },
  no_relationship: {
    badge: "bg-slate-100 text-slate-500",
    stroke: "rgb(148 163 184)",
    label: enumLabel("StrmRelationshipType", "no_relationship"),
  },
};

const FALLBACK: StrmStyle = {
  badge: "bg-slate-100 text-slate-600",
  stroke: "rgb(100 116 139)",
  label: "Unknown",
};

// strmStyle resolves a raw `relationship_type` string to its styling.
// Unrecognized values get the neutral fallback rather than throwing.
export function strmStyle(relationshipType: string): StrmStyle {
  return KNOWN[relationshipType] ?? FALLBACK;
}
