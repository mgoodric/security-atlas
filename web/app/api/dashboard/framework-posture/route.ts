import { getFrameworkPosture } from "@/lib/api/dashboard";
import { dashboardProxy } from "../proxy";

// Slice 147 — server-side proxy for GET /v1/frameworks/posture
// (slice 066 dashboard backend reads). The handler returns the unified
// per-framework-version posture envelope `{frameworks, count}` with
// `coverage_pct`, `freshness_composite`, and `trend_delta_90d` per row
// (null, with `scope_set: false`, for a version with no activated
// framework scope — #2114).
//
// Replaces the slice 040 `MissingEndpointPanel` placeholder — slice
// 066's `internal/api/dashboard` package ships the endpoint, this
// route is the BFF that the panel queries.

export function GET() {
  return dashboardProxy(getFrameworkPosture);
}
