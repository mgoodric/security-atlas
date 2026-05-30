import { getOverdueDecisions } from "@/lib/api/risk-hierarchy";
import { hierarchyProxy } from "../proxy";

// Slice 056 — server-side proxy for GET /v1/decisions/overdue (slice
// 055 Decision Log). Active decisions whose `revisit_by` has already
// passed. The timeline panel cross-references this set to mark rows
// with the amber "Revisit overdue" pill. Pure read-only.

export function GET() {
  return hierarchyProxy((bearer) =>
    getOverdueDecisions(bearer).then((decisions) => ({
      decisions,
      count: decisions.length,
    })),
  );
}
