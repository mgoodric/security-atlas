// Slice 479 — admin user-management BFF (revoke).
//
// POST /api/admin/users/revoke -> upstream POST /v1/admin/users/revoke
//
// Authority is enforced upstream (slice 478). This BFF forwards the
// session cookie's bearer and passes the upstream status + error through
// verbatim. On success the upstream returns 204; this route returns 200
// + {ok:true} so the browser fetch's `res.ok` is true and the page's
// TanStack mutation resolves (a 204 has no JSON body to parse).

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { revokeAdminUser, type RevokeUserRequest } from "@/lib/api/admin";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function POST(req: NextRequest) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  let body: Partial<RevokeUserRequest>;
  try {
    body = (await req.json()) as Partial<RevokeUserRequest>;
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }

  const validationError = validateRevokeBody(body);
  if (validationError) {
    return NextResponse.json({ error: validationError }, { status: 400 });
  }

  try {
    await revokeAdminUser(bearer, {
      user_id: (body.user_id ?? "").trim(),
      tenant_id: (body.tenant_id ?? "").trim(),
      remove_membership: body.remove_membership === true,
    });
    return NextResponse.json({ ok: true });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}

// validateRevokeBody returns a human-readable error string, or null when
// the body is well-formed. Pure logic — unit-tested.
export function validateRevokeBody(
  body: Partial<RevokeUserRequest>,
): string | null {
  const userID = (body.user_id ?? "").trim();
  if (userID === "") return "user_id is required";
  if (!isUuidShape(userID)) return "user_id must be a UUID";
  const tenantID = (body.tenant_id ?? "").trim();
  if (tenantID === "") return "tenant_id is required";
  if (!isUuidShape(tenantID)) return "tenant_id must be a UUID";
  return null;
}

function isUuidShape(s: string): boolean {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
    s,
  );
}
