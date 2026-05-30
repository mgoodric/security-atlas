import { getControlDrift } from "@/lib/api/dashboard";
import { dashboardProxy } from "../proxy";

// Slice 040 — server-side proxy for GET /v1/controls/drift?since=7d
// (slice 016 drift read model). The `since` window is fixed at 7d to
// match the dashboard panel ("last 7 days").

export function GET() {
  return dashboardProxy((bearer) => getControlDrift(bearer, "7d"));
}
