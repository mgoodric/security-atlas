// Slice 143 — admin tenants BFF (list + create).
//
// GET  /api/admin/tenants -> upstream GET  /v1/admin/tenants
// POST /api/admin/tenants -> upstream POST /v1/admin/tenants
//
// The handler is super_admin-gated upstream; this BFF just forwards
// the session cookie's bearer token. On any non-2xx upstream the
// status code + error message pass through to the client.

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import {
  createAdminTenant,
  listAdminTenants,
  type CreateTenantRequest,
} from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET() {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const items = await listAdminTenants(bearer);
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
  let body: Partial<CreateTenantRequest>;
  try {
    body = (await req.json()) as Partial<CreateTenantRequest>;
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }

  // Client-side validation mirrors the upstream's regex so the operator
  // gets fast feedback. The upstream is the load-bearing check.
  const name = (body.name ?? "").trim();
  const slug = (body.slug ?? "").trim();
  if (name === "") {
    return NextResponse.json({ error: "name is required" }, { status: 400 });
  }
  if (slug === "") {
    return NextResponse.json({ error: "slug is required" }, { status: 400 });
  }
  if (!/^[a-z0-9][a-z0-9-]{0,62}$/.test(slug)) {
    return NextResponse.json(
      { error: "slug must match ^[a-z0-9][a-z0-9-]{0,62}$" },
      { status: 400 },
    );
  }
  const joinsAs = body.creator_joins_as ?? "none";
  if (joinsAs !== "admin" && joinsAs !== "none") {
    return NextResponse.json(
      { error: "creator_joins_as must be 'admin' or 'none'" },
      { status: 400 },
    );
  }

  try {
    const out = await createAdminTenant(bearer, {
      name,
      slug,
      creator_joins_as: joinsAs,
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
