// Slice 060 — admin credentials BFF (proxy to slice 034).
//
// GET  /api/admin/credentials  -> upstream GET  /v1/admin/credentials
// POST /api/admin/credentials  -> upstream POST /v1/admin/credentials
//
// The bearer cookie translates to an Authorization header on the upstream
// hop; nothing else flows. Per slice 051, tenant_id is NEVER passed in the
// body — the upstream derives it from the calling credential. We strip any
// tenant_id field defensively before forwarding so an over-eager client
// can't accidentally trip the 400.
//
// Anti-criterion P0: the issue response includes `bearer_token`. The BFF
// passes it through verbatim ONCE; no caching, no logging. The frontend
// stores it in component state, displays it once, and the user copies it.
// Reload of the API-keys page returns to the list (no bearer present).

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import {
  AdminCredentialIssueRequest,
  issueAdminCredential,
  listAdminCredentials,
} from "@/lib/api/admin";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET() {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const items = await listAdminCredentials(bearer);
    return NextResponse.json({ items });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}

export async function POST(req: NextRequest) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  type AdminIssueBodyWithLegacyTenantID = AdminCredentialIssueRequest & {
    tenant_id?: string;
  };
  let body: AdminIssueBodyWithLegacyTenantID;
  try {
    body = (await req.json()) as AdminIssueBodyWithLegacyTenantID;
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  // Defensive: never forward a tenant_id field even if the client sent one
  // (slice 051 D1 — tenant is derived from the calling credential upstream).
  if ("tenant_id" in body) {
    delete body.tenant_id;
  }
  try {
    const issued = await issueAdminCredential(bearer, body);
    return NextResponse.json(issued, { status: 201 });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
