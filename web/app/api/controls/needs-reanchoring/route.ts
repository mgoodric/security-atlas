import { getControlsNeedingReanchoring } from "@/lib/api/reanchoring";

import { dashboardProxy } from "../../dashboard/proxy";

// Issue #2176 — server-side proxy for GET /v1/controls/needs-reanchoring.
// The dashboard warning and /controls/needs-reanchoring read it. It sits
// under /api/controls, not /api/dashboard, because the dashboard's first
// load is prefetched server-side and fires no /api/dashboard/* request
// (slice 380); this optional banner is a client read on its own route.

export function GET() {
  return dashboardProxy((bearer) => getControlsNeedingReanchoring(bearer));
}
