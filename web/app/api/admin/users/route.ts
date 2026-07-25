// Slice 479 — admin user-management BFF (list + assign).
//
// GET  /api/admin/users          -> upstream GET  /v1/admin/users
// POST /api/admin/users          -> upstream POST /v1/admin/users/assign
//
// The revoke route lives in ./revoke/route.ts and proxies to
// POST /v1/admin/users/revoke.
//
// AUTHZ-HONEST (P0-479-1): authority is enforced upstream (slice 478).
// This BFF forwards the session cookie's bearer and passes the upstream
// status + error message through verbatim. A 403 from the upstream
// (e.g. a tenant-admin reaching the cross-tenant surface) reaches the
// client as a 403 with the upstream message — the page renders it as a
// clear inline error, never a silent failure (AC-5).
//
// The GET handler also surfaces the derived `cross_tenant` flag so the
// page can gate cross-tenant controls without a second probe — true only
// when the upstream returned the super_admin cross-tenant shape
// (P0-479-2: a tenant-admin's response is the within-tenant shape, so
// cross_tenant=false and no cross-tenant controls render).

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import {
  assignAdminUser,
  listAdminUsers,
  type AssignUserRequest,
} from "@/lib/api/admin";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(req: NextRequest) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const cursor = req.nextUrl.searchParams.get("cursor") ?? undefined;
  const limitRaw = req.nextUrl.searchParams.get("limit");
  const limit =
    limitRaw && Number.isFinite(Number(limitRaw))
      ? Number(limitRaw)
      : undefined;
  try {
    const result = await listAdminUsers(bearer, { cursor, limit });
    return NextResponse.json(result);
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
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  let body: Partial<AssignUserRequest>;
  try {
    body = (await req.json()) as Partial<AssignUserRequest>;
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }

  // Fast client-feedback validation that mirrors the upstream's checks.
  // The upstream (slice 478 validateAssign) is the load-bearing gate.
  const validationError = validateAssignBody(body);
  if (validationError) {
    return NextResponse.json({ error: validationError }, { status: 400 });
  }

  try {
    const out = await assignAdminUser(bearer, {
      tenant_id: (body.tenant_id ?? "").trim(),
      roles: (body.roles ?? []).map((r) => r.trim()),
      user_id: body.self_assign ? undefined : (body.user_id ?? "").trim(),
      self_assign: body.self_assign === true,
    });
    return NextResponse.json(out);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}

// validateAssignBody returns a human-readable error string, or null when
// the body is well-formed. Pure logic — unit-tested.
export function validateAssignBody(
  body: Partial<AssignUserRequest>,
): string | null {
  const tenantID = (body.tenant_id ?? "").trim();
  if (tenantID === "") return "tenant_id is required";
  if (!isUuidShape(tenantID)) return "tenant_id must be a UUID";
  const roles = body.roles ?? [];
  if (roles.length === 0) return "at least one role is required";
  if (body.self_assign !== true) {
    const userID = (body.user_id ?? "").trim();
    if (userID === "") return "user_id is required unless self_assign=true";
    if (!isUuidShape(userID)) return "user_id must be a UUID";
  }
  return null;
}

function isUuidShape(s: string): boolean {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
    s,
  );
}
