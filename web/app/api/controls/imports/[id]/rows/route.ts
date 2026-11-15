// #1785 — BFF for GET /v1/controls/imports/{id}/rows: staged rows with
// their ranked candidates. Only the two filters the platform knows,
// ?status= and ?no_candidate_reaches=, are forwarded; it validates them.

import { forwardJSON, noStore, rejectNonUUID } from "@/lib/api/bff";

const FORWARDED = ["status", "no_candidate_reaches"];

export async function GET(
  req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  const incoming = new URL(req.url).searchParams;
  const qs = new URLSearchParams();
  for (const k of FORWARDED) {
    const v = incoming.get(k);
    if (v !== null) qs.set(k, v);
  }
  const q = qs.toString();
  return noStore(
    await forwardJSON(`/v1/controls/imports/${id}/rows${q ? `?${q}` : ""}`),
  );
}
