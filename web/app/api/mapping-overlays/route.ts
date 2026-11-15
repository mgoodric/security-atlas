// #1802 — BFF for POST /v1/mapping-overlays: add an overlay edge.
//
// Only the overlay fields are forwarded (OVERLAY_EDGE_FIELDS). The
// platform keeps its role guard (admin or grc_engineer) and its
// validation; a 403, 409 or 400 passes through for the page to show.

import type { NextRequest } from "next/server";

import { OVERLAY_EDGE_FIELDS } from "@/lib/api/audited-under";
import { forwardJSON, noStore } from "@/lib/api/bff";
import { pickBody, readJSONObject } from "@/lib/api/control-imports-bff";

export async function POST(req: NextRequest) {
  const body = await readJSONObject(req);
  if (body instanceof Response) return body;
  return noStore(
    await forwardJSON("/v1/mapping-overlays", {
      method: "POST",
      jsonBody: pickBody(body, OVERLAY_EDGE_FIELDS),
    }),
  );
}
