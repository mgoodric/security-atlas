// #1818 — BFF proxy for one organisation theme.
//
//   PATCH  /api/themes/{ref} -> PATCH  /v1/themes/{ref}
//   DELETE /api/themes/{ref} -> DELETE /v1/themes/{ref}
//
// `ref` is the theme's name or id. Upstream refuses built-ins and themes
// still in use (409 with counts); status and body pass through verbatim.

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
    await forwardJSON(`/v1/themes/${encodeURIComponent(ref)}`, {
      method: "PATCH",
      jsonBody: body,
    }),
  );
}

export async function DELETE(_req: Request, ctx: Ctx): Promise<Response> {
  const { ref } = await ctx.params;
  return noStore(
    await forwardJSON(`/v1/themes/${encodeURIComponent(ref)}`, {
      method: "DELETE",
    }),
  );
}
