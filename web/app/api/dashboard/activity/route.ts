import { getActivity } from "@/lib/api/dashboard";
import { dashboardProxy } from "../proxy";

// Slice 147 — server-side proxy for GET /v1/activity
// (slice 066 dashboard backend reads). The handler returns the paginated
// evidence-ingest activity feed envelope `{activity, count, next_cursor}`
// over the slice-062 `admin_audit_log_v` view (evidence branch).
//
// Replaces the slice 040 `MissingEndpointPanel` placeholder — slice 066
// reads `admin_audit_log_v` filtered to the `evidence_audit_log` source
// table; this BFF forwards the request unchanged. Pagination (cursor +
// limit) is left to a follow-up: this slice ships only the first-page
// fetch since the panel does not yet wire an infinite-scroll affordance.
//
// Bearer never reaches the browser — it is read from the httpOnly
// session cookie server-side and attached as Authorization.

export function GET() {
  return dashboardProxy(getActivity);
}
