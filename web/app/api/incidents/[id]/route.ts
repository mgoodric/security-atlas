import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { APIError } from "@/lib/api/base";
import { getIncident } from "@/lib/api/incidents";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

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
    return NextResponse.json(await getIncident(bearer, id));
  } catch (err) {
    if (err instanceof APIError) {
      if (err.status === 404) {
        return NextResponse.json(
          { error: "incident not found" },
          { status: 404 },
        );
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
