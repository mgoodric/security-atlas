import { cookies } from "next/headers";
import { type NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { rejectNonUUID, upstreamBody } from "@/lib/api/bff";

export async function POST(
  _req: NextRequest,
  {
    params,
  }: { params: Promise<{ id: string; qid: string; proposalID: string }> },
): Promise<Response> {
  const { id, qid, proposalID } = await params;
  const bad = rejectNonUUID({ id, qid, proposalID });
  if (bad) return bad;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(
    `${apiBaseURL()}/v1/questionnaires/${encodeURIComponent(
      id,
    )}/questions/${encodeURIComponent(qid)}/scf-mapping/${encodeURIComponent(
      proposalID,
    )}/approve`,
    {
      method: "POST",
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    },
  );
  const text = await upstream.text();
  return new NextResponse(upstreamBody(upstream.status, text), {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
