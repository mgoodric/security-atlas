import type { NextRequest } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

// #1794 — GET /v1/frameworks (id, name, slug, latest_version_id). The SoA
// tab reads it to title an ISO/IEC 27001 version "Statement of
// Applicability" and every other framework "Applicability".
//
// #2086 — `?include=versions` is forwarded so the framework picker can
// list every version by name. No other query parameter reaches upstream.

export async function GET(req?: NextRequest) {
  const include = req?.nextUrl.searchParams.get("include");
  return forwardJSON(
    include === "versions"
      ? "/v1/frameworks?include=versions"
      : "/v1/frameworks",
  );
}
