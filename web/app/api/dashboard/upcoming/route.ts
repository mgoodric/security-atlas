import { getUpcoming } from "@/lib/api/dashboard";
import { dashboardProxy } from "../proxy";

// Slice 157 — server-side proxy for GET /v1/upcoming (slice 066 AC-4).
// The endpoint returns the unified upcoming-rollup envelope
// `{upcoming, count, next_cursor}` — expiring exceptions, policy-ack
// expirations, vendor reviews, and audit-period milestones merged into
// one date-sorted (ascending) feed. Each row carries `{due_date,
// category, title, resource_type, resource_id}`.
//
// Replaces the slice 040 wiring that hit `/v1/exceptions/expiring?
// within=30d` and surfaced the unified-rollup gap as a labelled
// `upcoming-gap` footer. The slice 066 endpoint shipped the rollup; the
// frontend was never re-pointed until this slice (the spillover from
// slice 147, which closed two of slice 066's four follow-on panels).
//
// Bearer never reaches the browser — it is read from the httpOnly
// session cookie server-side and attached as Authorization.

export function GET() {
  return dashboardProxy(getUpcoming);
}
