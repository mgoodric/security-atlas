import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { getAnchorRequirements } from "@/lib/api/anchors";

// Slice 484 — the version-aware reverse-traversal proxy. Reads the optional
// `?framework_version=slug:version` query and forwards it upstream so the
// operator's version selection reaches the version-pinned read. The bearer
// cookie is forwarded server-side (never to the browser). Absent the pin, the
// upstream defaults to each framework's CURRENT version (ADR 0019 §4); a
// legacy/superseded version is returned ONLY when explicitly pinned.
//
// Input discipline: only a `slug:version` shaped value is forwarded. Anything
// else is dropped (treated as no pin) rather than passed through verbatim — the
// BFF never forwards arbitrary client text into the upstream query string.
const FRAMEWORK_VERSION_RE = /^[A-Za-z0-9._-]+:[A-Za-z0-9._-]+$/;

export async function GET(
  request: NextRequest,
  { params }: { params: Promise<{ id: string }> },
) {
  const { id } = await params;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const raw = request.nextUrl.searchParams.get("framework_version");
  const frameworkVersion =
    raw && FRAMEWORK_VERSION_RE.test(raw) ? raw : undefined;

  try {
    const detail = await getAnchorRequirements(bearer, id, frameworkVersion);
    return NextResponse.json(detail);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
