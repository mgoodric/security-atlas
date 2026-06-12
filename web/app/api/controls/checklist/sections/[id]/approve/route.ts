// Slice 471 — BFF proxy for the per-section one-click approval
// `POST /v1/controls/checklist/sections/{id}:approve` (AC-10).
//
// The approver is derived server-side from the authenticated credential by the
// platform — it is NEVER read from the request body here (a caller cannot
// approve "as" someone else). This BFF carries no body.

import { forwardJSON } from "@/lib/api/bff";

export async function POST(
  _req: Request,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  return forwardJSON(
    `/v1/controls/checklist/sections/${encodeURIComponent(id)}:approve`,
    { method: "POST" },
  );
}
