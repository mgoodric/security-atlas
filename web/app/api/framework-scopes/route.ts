import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import {
  FrameworkScopeCreate,
  FrameworkScopeState,
  createFrameworkScope,
  listFrameworkScopes,
} from "@/lib/api/framework-scopes";

// Slice 018 — list + create framework scopes. Same cookie-bearer
// pattern as /api/vendors/route.ts.

export async function GET(req: NextRequest) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const fv = req.nextUrl.searchParams.get("framework_version") ?? undefined;
  const state = (req.nextUrl.searchParams.get("state") ?? undefined) as
    | FrameworkScopeState
    | undefined;
  const asOf = req.nextUrl.searchParams.get("as_of") ?? undefined;
  try {
    const framework_scopes = await listFrameworkScopes(bearer, {
      framework_version: fv,
      state,
      as_of: asOf,
    });
    return NextResponse.json({ framework_scopes });
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
  let body: FrameworkScopeCreate;
  try {
    body = (await req.json()) as FrameworkScopeCreate;
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }
  try {
    const framework_scope = await createFrameworkScope(bearer, body);
    return NextResponse.json({ framework_scope }, { status: 201 });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
