// Slice 101 — BFF proxy for `/policies` list view.
//
// Reads the bearer cookie server-side and calls `/v1/policies` upstream.
// The bearer never reaches the browser. Mirrors the slice 098 controls /
// slice 100 risks route pattern (cookie -> bearer -> upstream call). The
// row source is `internal/api/policies/handlers.go` (`policyWire`) per
// design doc `Plans/canvas/12-ui-fill-in-design-decisions.md` §7.
//
// Why a `/api/policies` route: per the BFF-per-page convention slices
// 098/099/100/101/102 follow, keeping the URL shape predictable. The
// slice 107 `?include=ack_rate` extension is hard-coded inside
// `listPolicies` (web/lib/api.ts) — the BFF forwards verbatim. Mirrors
// slice 104's hard-coded `?include=state` for anchors.
//
// Slice 101 exposed GET only; #2116 adds POST, the create form's draft.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { forwardJSON, noStore } from "@/lib/api/bff";
import { listPolicies } from "@/lib/api/policies";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const policies = await listPolicies(bearer);
    return NextResponse.json({ policies });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}

// #2116 — POST creates a draft (the /policies/new form, and "Create new
// version" with a predecessor_id). The platform validates every field and
// owns the 409 for a predecessor that already has a draft successor.
export async function POST(req: Request): Promise<Response> {
  let body: unknown;
  try {
    body = JSON.parse(await req.text());
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  return noStore(
    await forwardJSON("/v1/policies", { method: "POST", jsonBody: body }),
  );
}
