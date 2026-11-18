import type { NextRequest } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

// #2414 — GET /v1/activity/actors: the machine and system actors in the
// caller's activity for a window, for the Activity page's actor picker.
// Only `from`, `to` and `include_reads` reach upstream; the platform
// validates the window and applies the same visibility as the feed.

const FORWARDED = ["from", "to", "include_reads"] as const;

export async function GET(req: NextRequest) {
  const out = new URLSearchParams();
  for (const key of FORWARDED) {
    const v = req.nextUrl.searchParams.get(key);
    if (v) out.set(key, v);
  }
  const qs = out.toString();
  return forwardJSON(`/v1/activity/actors${qs ? `?${qs}` : ""}`);
}
