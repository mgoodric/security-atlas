import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { listComponentDefinitions } from "@/lib/api/oscal-components";

// Server-side proxy: list the tenant's imported vendor component-definitions.
// Bearer cookie -> upstream Authorization header.
export async function GET() {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const body = await listComponentDefinitions(bearer);
    return NextResponse.json(body);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
