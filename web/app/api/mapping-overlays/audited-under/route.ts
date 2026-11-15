// #1802 — BFF for GET /v1/mapping-overlays/audited-under. Only the
// `framework` filter is forwarded, and only when it is shaped like a
// slug; the page reads the whole report and filters it client-side, so it
// normally sends none.

import { NextResponse } from "next/server";

import { isFrameworkSlug } from "@/lib/api/audited-under";
import { forwardJSON, noStore } from "@/lib/api/bff";

export async function GET(req: Request) {
  const framework = new URL(req.url).searchParams.get("framework");
  if (framework && !isFrameworkSlug(framework)) {
    return NextResponse.json(
      { error: "framework must be a framework slug" },
      { status: 400 },
    );
  }
  const qs = framework ? `?framework=${encodeURIComponent(framework)}` : "";
  return noStore(await forwardJSON(`/v1/mapping-overlays/audited-under${qs}`));
}
