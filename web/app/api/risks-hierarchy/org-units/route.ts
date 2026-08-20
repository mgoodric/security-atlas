import { getOrgUnits } from "@/lib/api/risk-hierarchy";
import { hierarchyProxy } from "../proxy";

// Slice 056 — server-side proxy for GET /v1/org_units (slice 053 org
// hierarchy). The org tree panel builds the parent/child tree
// client-side from the flat `parent_id` list. Pure read-only.

export function GET() {
  return hierarchyProxy((bearer) =>
    getOrgUnits(bearer).then((org_units) => ({
      org_units,
      count: org_units.length,
    })),
  );
}
