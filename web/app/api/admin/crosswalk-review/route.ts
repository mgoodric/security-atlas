// Slice 536b-1 — BFF for the crosswalk-review queue (the 536b-2 UI contract).
//
// GET /api/admin/crosswalk-review?framework_version_id=<uuid>[&limit&offset]
//   -> GET /v1/admin/crosswalk-review?...
//
// The upstream (internal/api/admincrosswalkreview) owns ALL validation
// (UUID + pagination bounds) and ALL authorization (cred.IsAdmin; non-admin
// 403) and computes the slice-536a conflict findings over the framework
// version's full edge set. The BFF forwards the whitelisted query params
// verbatim and adds no filtering of its own.
//
// noStore: the review queue is mutable admin state — after an edit or a tier
// transition the refetch must not be answered from the browser HTTP cache
// with the pre-write body (the slice 746 saved-views precedent).

import { forwardJSON, noStore } from "@/lib/api/bff";

const FORWARDED_PARAMS = ["framework_version_id", "limit", "offset"] as const;

export async function GET(req: Request): Promise<Response> {
  const incoming = new URL(req.url).searchParams;
  const qs = new URLSearchParams();
  for (const name of FORWARDED_PARAMS) {
    const v = incoming.get(name);
    if (v !== null) {
      qs.set(name, v);
    }
  }
  const suffix = qs.size > 0 ? `?${qs.toString()}` : "";
  return noStore(await forwardJSON(`/v1/admin/crosswalk-review${suffix}`));
}
