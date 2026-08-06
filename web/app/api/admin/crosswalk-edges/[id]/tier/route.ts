// Slice 536b-1 — BFF for the crosswalk-mapping tier transition (the
// approve/reject verb of the review UI).
//
// POST /api/admin/crosswalk-edges/{id}/tier
//   -> POST /v1/admin/crosswalk-edges/{id}/tier   (slice 483)
//
// This is deliberately a THIN forward to slice 483's tier state machine —
// the ONE review lifecycle (536a decisions-log §1.2). Approve = body
// {tier: "verified"} (from under_review), reject = {tier: "rejected"},
// claim = {tier: "under_review"}, demote-for-edit = {tier: "under_review"}
// from verified (D-536b-1). The upstream owns transition legality (422 on an
// illegal move — e.g. the draft -> verified skip), the admin gate, and the
// same-transaction tier-transition audit row. No second approval workflow
// exists in the BFF; nothing here (or anywhere) auto-approves a mapping.

import { NextRequest } from "next/server";

import { forwardJSON, noStore } from "@/lib/api/bff";

export async function POST(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    body = {};
  }
  return noStore(
    await forwardJSON(
      `/v1/admin/crosswalk-edges/${encodeURIComponent(id)}/tier`,
      { method: "POST", jsonBody: body },
    ),
  );
}
