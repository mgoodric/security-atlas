import { getExpiringExceptions } from "@/lib/api";
import { dashboardProxy } from "../proxy";

// Slice 040 — server-side proxy for GET /v1/exceptions/expiring?within=30d
// (slice 028 exception lifecycle).
//
// The dashboard mockup's "Upcoming · next 30 days" panel also wants
// board-report-due, access-review, and questionnaire-due rows. There is
// no unified upcoming-rollup endpoint on main — exceptions-expiring is
// the one real source and is bound here. The other categories are noted
// as a labelled gap in the panel rather than fabricated (see the slice
// 040 decisions log). The window is fixed at 30d to match the panel.

export function GET() {
  return dashboardProxy((bearer) => getExpiringExceptions(bearer, "30d"));
}
