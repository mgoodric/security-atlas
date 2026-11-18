// #1778 — GET /v1/controls/remap-proposals proxy for the remap review
// page. Forwards the caller's bearer; the platform scopes the read to the
// caller's tenant (RLS). Only the documented filters and the paging
// parameters (#2325) pass through.

import { forwardJSON } from "@/lib/api/bff";

const FILTERS = ["class", "class_not", "status", "limit", "cursor"] as const;

export function GET(req: Request) {
  const src = new URL(req.url).searchParams;
  const qs = new URLSearchParams();
  for (const k of FILTERS) {
    const v = src.get(k);
    if (v) qs.set(k, v);
  }
  const s = qs.toString();
  return forwardJSON(`/v1/controls/remap-proposals${s ? `?${s}` : ""}`);
}
