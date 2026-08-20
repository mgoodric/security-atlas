// Slice 660 — non-admin enabled-modules BFF.
//
// Proxies GET /v1/features/enabled (authed, NOT admin-only) so the web
// shell can gate nav entries for EVERY signed-in user (not just admins).
// The upstream returns only the slice 660 GATING flag booleans for the
// caller's own tenant:
//
//   { "modules": { "oscal.export": false, "board.reporting": false } }
//
// The bearer cookie never reaches the browser — read server-side here and
// forwarded as the Authorization header (same pattern as /api/admin/me).
// Tenant scope stays on the platform (RLS — invariant #6).
//
// Fail-closed posture (P0): any non-200 / network error / non-JSON body
// collapses to an empty modules map. The shell's nav gate treats a
// missing key as "off" (hide the nav entry) — rendering a pre-GA nav link
// the route would 404 on is worse than a brief absence. This mirrors the
// slice 186 admin-probe fail-closed convention.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { normalizeEnabledModules } from "@/lib/api/self.server";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

type EnabledBody = {
  modules: Record<string, boolean>;
};

// OE-549: the 200-body coercion moved to `normalizeEnabledModules` in
// `lib/api/self.server.ts` so this route and `lib/feature-nav.server.ts`
// (which now reads `/v1/features/enabled` directly rather than
// self-fetching this route) share ONE definition of the fail-closed
// shape. The status-code posture below is unchanged.
function emptyBody(): EnabledBody {
  return { modules: {} };
}

export async function GET() {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json(emptyBody(), { status: 401 });
  }
  try {
    const upstream = await fetch(`${apiBaseURL()}/v1/features/enabled`, {
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    });
    if (!upstream.ok) {
      // Non-200 -> fail-closed empty map. The caller hides the gated nav.
      return NextResponse.json(emptyBody(), {
        status: upstream.status === 401 ? 401 : 200,
      });
    }
    const modules = normalizeEnabledModules(await upstream.json());
    return NextResponse.json({ modules });
  } catch {
    return NextResponse.json(emptyBody(), { status: 200 });
  }
}
