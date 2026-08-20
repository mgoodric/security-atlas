// Slice 536b-1 — BFF for crosswalk-mapping CONTENT edits.
//
// PATCH /api/admin/crosswalk-edges/{id} -> PATCH /v1/admin/crosswalk-edges/{id}
//
// Body: { relationship_type?, strength?, rationale?, note? } — forwarded
// verbatim so the upstream (internal/api/admincrosswalkreview +
// internal/crosswalkedit) stays the single source of truth for validation
// (STRM type / strength range / empty vs no-op patch), the admin gate, the
// D-536b-1 tier gate (verified/rejected -> 409: demote via the tier endpoint
// first), and the same-transaction before/after audit row. The editor
// identity comes from the bearer's JWT subject upstream — never from this
// body. Status passthrough: 200 / 400 / 401 / 403 / 404 / 409 / 422.

import { NextRequest } from "next/server";

import { forwardJSON, noStore } from "@/lib/api/bff";

export async function PATCH(
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
    await forwardJSON(`/v1/admin/crosswalk-edges/${encodeURIComponent(id)}`, {
      method: "PATCH",
      jsonBody: body,
    }),
  );
}
