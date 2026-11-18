// #1818 — BFF proxy for a category's state transitions.
//
//   POST /api/risks/categories/{ref}/archive     -> upstream .../archive
//   POST /api/risks/categories/{ref}/unarchive   -> upstream .../unarchive
//   POST /api/risks/categories/{ref}/set-default -> upstream .../set-default
//
// Only these three actions are forwarded; any other segment is a 404 here,
// so the BFF never becomes a generic path relay onto /v1/risks/categories.

import { NextResponse } from "next/server";

import {
  SEGMENT,
  forwardJSON,
  noStore,
  rejectUnsafeSegment,
} from "@/lib/api/bff";

const CATEGORY_ACTIONS: readonly string[] = [
  "archive",
  "unarchive",
  "set-default",
];

type Ctx = { params: Promise<{ ref: string; action: string }> };

export async function POST(_req: Request, ctx: Ctx): Promise<Response> {
  const { ref, action } = await ctx.params;
  const bad = rejectUnsafeSegment({ ref }, SEGMENT.slugOrUUID);
  if (bad) return bad;
  if (!CATEGORY_ACTIONS.includes(action)) {
    return NextResponse.json({ error: "unknown action" }, { status: 404 });
  }
  return noStore(
    await forwardJSON(
      `/v1/risks/categories/${encodeURIComponent(ref)}/${action}`,
      { method: "POST" },
    ),
  );
}
