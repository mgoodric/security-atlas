import { getOrgUnits } from "@/lib/api";
import { hierarchyProxy } from "../proxy";

// Slice 056 — server-side proxy for GET /v1/org_units (slice 053 org
// hierarchy). The org tree panel builds the parent/child tree
// client-side from the flat `parent_id` list. Pure read-only.
//
// NOTE: the AC asks for `?include_risk_counts=true`. The upstream
// ListOrgUnits handler ignores all query params; per-node risk counts
// are a labelled backend gap (see the slice 056 decisions log). This
// route forwards the bare list and never fabricates counts.

export function GET() {
  return hierarchyProxy((bearer) =>
    getOrgUnits(bearer).then((org_units) => ({
      org_units,
      count: org_units.length,
    })),
  );
}
