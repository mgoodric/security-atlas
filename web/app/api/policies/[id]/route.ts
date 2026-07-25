// Slice 672 — BFF proxy for the `/policies/[id]` read-only detail view.
//
// Mirrors the slice 101 `/api/policies` list route + the slice 024
// `/api/vendors/[id]` detail route: read the bearer cookie server-side
// and call `GET /v1/policies/{id}` upstream. The bearer never reaches
// the browser. The upstream enforces RLS tenant-isolation (invariant
// #6) — this route MUST NOT accept or forward a client-supplied
// tenant_id; the only tenant context is the cookie session.
//
// Read-only: only GET is exposed. Policy submit / approve / publish stay
// on their existing `/v1/policies/{id}/...` routes (out of scope for the
// detail page per slice 672 anti-criteria).
//
// Error mapping (slice 367 — never leak an internal error to the user):
//   - missing bearer cookie         -> 401 { error: "unauthenticated" }
//   - upstream 404 (no such policy) -> 404 { error: "policy not found" }
//     (the page maps this to Next `notFound()` -> in-shell not-found)
//   - upstream 401                  -> 401 (the page redirects to /login)
//   - any other upstream error      -> the upstream status + a clean
//     `{error}` line (getPolicy's APIError already carries the status
//     line; the platform's httperr layer scrubbed the body upstream).

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { APIError } from "@/lib/api/base";
import { getPolicy, getPolicyAckRate } from "@/lib/api/policies";
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
    const policy = await getPolicy(bearer, id);
    // Secondary, best-effort detail: the acknowledgment rate is fetched
    // only for published policies (the upstream returns 409 otherwise);
    // getPolicyAckRate swallows every non-200 to `null` so its absence
    // never breaks the page render. This is ONE server-side call per
    // detail page — not a client-side per-row fan-out (list-view P0-A2).
    const ack_rate =
      policy.status === "published" ? await getPolicyAckRate(bearer, id) : null;
    return NextResponse.json({ policy, ack_rate });
  } catch (err) {
    if (err instanceof APIError) {
      if (err.status === 404) {
        return NextResponse.json(
          { error: "policy not found" },
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
