// Slice 681 / ATLAS-039 — BFF proxy for the `/risks/[id]` read-only
// detail view.
//
// Mirrors the slice 672 `/api/policies/[id]` detail route + the slice
// 100 `/api/risks` list route: read the bearer cookie server-side and
// call `GET /v1/risks/{id}` upstream. The bearer never reaches the
// browser. The upstream enforces RLS tenant-isolation (invariant #6) —
// this route MUST NOT accept or forward a client-supplied tenant_id; the
// ONLY tenant context is the cookie session, so a tenant can only ever
// read its own risk (a cross-tenant id resolves to upstream 404).
//
// GET reads the risk. #2104 adds PATCH: the Edit route saves the record
// fields through it, forwarded to `PATCH /v1/risks/{id}` with the upstream
// status and body passed through (a 400 refusal renders in the form).
// #2311: an If-Match header is forwarded too, so the platform can refuse
// (412) an edit made from a version someone else has since changed.
//
// Error mapping (slice 367 — never leak an internal error to the user):
//   - missing bearer cookie       -> 401 { error: "unauthenticated" }
//   - upstream 404 (no such risk) -> 404 { error: "risk not found" }
//     (the page maps this to Next `notFound()` -> in-shell not-found)
//   - upstream 401                -> 401 (the page redirects to /login)
//   - any other upstream error    -> the upstream status + a clean
//     `{error}` line (getRisk's APIError already carries the status
//     line; the platform's httperr layer scrubbed the body upstream).

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { APIError } from "@/lib/api/base";
import { forwardJSON, noStore } from "@/lib/api/bff";
import { getRisk } from "@/lib/api/risks";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function PATCH(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  const ifMatch = req.headers.get("If-Match");
  return noStore(
    await forwardJSON(`/v1/risks/${encodeURIComponent(id)}`, {
      method: "PATCH",
      jsonBody: body,
      headers: ifMatch ? { "If-Match": ifMatch } : undefined,
    }),
  );
}

export async function GET(
  _req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const detail = await getRisk(bearer, id);
    return NextResponse.json(detail);
  } catch (err) {
    if (err instanceof APIError) {
      if (err.status === 404) {
        return NextResponse.json({ error: "risk not found" }, { status: 404 });
      }
      return NextResponse.json({ error: err.message }, { status: err.status });
    }
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
