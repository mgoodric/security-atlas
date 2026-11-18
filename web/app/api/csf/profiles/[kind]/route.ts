import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { getCsfProfile, type CsfProfileKind } from "@/lib/api/csf";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { rejectUnsafeSegment } from "@/lib/api/bff";

const PROFILE_KINDS: ReadonlySet<string> = new Set(["current", "target"]);

// Slice 515 — read a CSF Current or Target profile + its per-Subcategory
// selections. Read-only BFF GET.
export async function GET(
  req: Request,
  { params }: { params: Promise<{ kind: string }> },
) {
  const { kind } = await params;
  const bad = rejectUnsafeSegment({ kind }, PROFILE_KINDS);
  if (bad) return bad;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const fv = new URL(req.url).searchParams.get("framework_version");
  if (!fv) {
    return NextResponse.json(
      { error: "framework_version is required" },
      { status: 400 },
    );
  }
  try {
    const body = await getCsfProfile(bearer, fv, kind as CsfProfileKind);
    return NextResponse.json(body);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
