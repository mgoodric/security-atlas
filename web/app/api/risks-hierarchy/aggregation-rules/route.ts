import { getAggregationRules } from "@/lib/api";
import { hierarchyProxy } from "../proxy";

// Slice 056 — server-side proxy for GET /v1/aggregation-rules (slice
// 054 aggregation rules engine). Supplies real rule-threshold metadata
// (`window_days`, `min_risks`, `min_teams`, `target_theme`) for the
// heatmap cell-hover tooltip so the "nearest rule fires at {threshold}"
// copy cites real numbers, not fabricated thresholds. Pure read-only.

export function GET() {
  return hierarchyProxy((bearer) =>
    getAggregationRules(bearer).then((rules) => ({
      rules,
      count: rules.length,
    })),
  );
}
