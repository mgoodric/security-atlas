// #1818 — BFF proxy for one organisation risk category.
//
//   PATCH  /api/risks/categories/{ref} -> PATCH  /v1/risks/categories/{ref}
//   DELETE /api/risks/categories/{ref} -> DELETE /v1/risks/categories/{ref}
//
// `ref` is the category's slug or id. Upstream status and body pass
// through verbatim: a rename that tries to change the slug is upstream's
// 400, a delete of a category in use is its 409 with the counts, and a
// caller without risk-taxonomy rights gets its 403.

import { NextResponse } from "next/server";

import { forwardJSON, noStore } from "@/lib/api/bff";

type Ctx = { params: Promise<{ ref: string }> };

export async function PATCH(req: Request, ctx: Ctx): Promise<Response> {
  const { ref } = await ctx.params;
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  return noStore(
    await forwardJSON(`/v1/risks/categories/${encodeURIComponent(ref)}`, {
      method: "PATCH",
      jsonBody: body,
    }),
  );
}

export async function DELETE(_req: Request, ctx: Ctx): Promise<Response> {
  const { ref } = await ctx.params;
  return noStore(
    await forwardJSON(`/v1/risks/categories/${encodeURIComponent(ref)}`, {
      method: "DELETE",
    }),
  );
}
