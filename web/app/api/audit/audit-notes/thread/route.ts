// Slice 042 — audit workspace BFF: GET /v1/audit-notes/thread proxy (slice 029).
//
// Query params: audit_period_id (required), scope_type (required),
// scope_id (optional). Returns the VISIBLE thread for the anchor.
//
// P0-2 (auditee cannot read auditor's private notes): the platform
// filters `auditor_only` notes to their author at the QUERY LAYER. This
// BFF passes the upstream response through verbatim — the UI NEVER
// client-side-filters note visibility. What the server returns is what
// the caller is allowed to see.

import { NextRequest } from "next/server";
import { NextResponse } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

export async function GET(req: NextRequest) {
  const sp = req.nextUrl.searchParams;
  const auditPeriodID = sp.get("audit_period_id");
  const scopeType = sp.get("scope_type");
  if (!auditPeriodID || !scopeType) {
    return NextResponse.json(
      { error: "audit_period_id and scope_type are required" },
      { status: 400 },
    );
  }
  const qs = new URLSearchParams();
  qs.set("audit_period_id", auditPeriodID);
  qs.set("scope_type", scopeType);
  const scopeID = sp.get("scope_id");
  if (scopeID) qs.set("scope_id", scopeID);
  return forwardJSON(`/v1/audit-notes/thread?${qs.toString()}`);
}
