// Slice 063 — admin SSO BFF (proxy to slice 062's `/v1/admin/sso`).
//
// GET   /api/admin/sso  -> upstream GET   /v1/admin/sso
//                          Returns { config: AdminSSOConfig | null }.
//                          Upstream 404 (no config yet) -> 200 { config: null }
//                          so the form renders empty rather than erroring.
// PATCH /api/admin/sso  -> upstream PATCH /v1/admin/sso
//                          Returns { config: AdminSSOConfig } on success,
//                          or { error } from upstream verbatim on failure.
//
// The bearer cookie translates to an Authorization header on the upstream
// hop. Per slice 051 D1, tenant_id is NEVER passed in the body — the
// upstream derives it from the calling credential. We strip any tenant_id
// field defensively before forwarding (same pattern as
// `web/app/api/admin/credentials/route.ts`).
//
// Anti-criterion P0 (slice 034 AC-9): the upstream GET response NEVER
// includes `client_secret`. This BFF passes the response through verbatim;
// the UI keeps the secret input password-typed and treats an empty submit
// as "leave existing" per slice 062's handler contract.

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { AdminSSOPatchRequest, getAdminSSO, patchAdminSSO } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET() {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const config = await getAdminSSO(bearer);
    return NextResponse.json({ config });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}

export async function PATCH(req: NextRequest) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  type SSOBodyWithLegacyTenantID = AdminSSOPatchRequest & {
    tenant_id?: string;
  };
  let body: SSOBodyWithLegacyTenantID;
  try {
    body = (await req.json()) as SSOBodyWithLegacyTenantID;
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  // Defensive: never forward a tenant_id field even if the client sent one
  // (slice 051 D1 — tenant is derived from the calling credential upstream).
  if ("tenant_id" in body) {
    delete body.tenant_id;
  }

  // Forward an explicitly-empty client_secret as "leave existing" per the
  // slice 062 handler contract. We achieve this by deleting the field
  // entirely when it's empty, so the upstream's "omitempty" branch fires.
  if (body.client_secret === "") {
    delete body.client_secret;
  }

  try {
    const config = await patchAdminSSO(bearer, body);
    return NextResponse.json({ config });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
