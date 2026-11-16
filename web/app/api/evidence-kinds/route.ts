// #2088 — BFF proxy for the tenant's registered evidence kinds.
//
// The API-key scope builder reads this to offer evidence kinds as a
// picker. The bearer cookie is read server-side and forwarded to
// `/v1/schemas`; it never reaches the browser. Only the fields the picker
// needs flow back (no schema bodies, no tenant or creator ids).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { listEvidenceSchemas } from "@/lib/api/evidence-kinds";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const rows = await listEvidenceSchemas(bearer);
    const schemas = rows.map(({ kind, semver, owner }) => ({
      kind,
      semver,
      owner,
    }));
    return NextResponse.json({ schemas });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
