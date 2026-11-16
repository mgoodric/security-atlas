// #1778 — one reviewer decision on a remap proposal:
// POST /v1/controls/remap-proposals/{id}/{approve|reject|keep-legacy}.
//
// The id must be a UUID and the action one of the three verbs, so nothing
// else reaches the upstream path. Only scf_id and reason are forwarded.

import { NextResponse } from "next/server";

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";

const ACTIONS = new Set(["approve", "reject", "keep-legacy"]);

export async function POST(
  req: Request,
  ctx: { params: Promise<{ id: string; action: string }> },
) {
  const { id, action } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  if (!ACTIONS.has(action)) {
    return NextResponse.json({ error: "unknown action" }, { status: 404 });
  }
  let raw: unknown = {};
  try {
    raw = await req.json();
  } catch {
    // empty body: no scf_id, no reason
  }
  const src = (raw && typeof raw === "object" ? raw : {}) as Record<
    string,
    unknown
  >;
  const body: { scf_id?: string; reason?: string } = {};
  if (action === "approve" && typeof src.scf_id === "string" && src.scf_id) {
    body.scf_id = src.scf_id;
  }
  if (typeof src.reason === "string" && src.reason) body.reason = src.reason;
  return forwardJSON(
    `/v1/controls/remap-proposals/${encodeURIComponent(id)}/${action}`,
    { method: "POST", jsonBody: body },
  );
}
