// Slice 060 + Slice 130 — admin / self-introspection BFF.
//
// Slice 060 (original): the admin layout needs to know whether the current
// bearer is an admin credential. The cheapest "am I admin?" test was a
// status-code probe of /v1/admin/credentials — non-admin gets 403, admin
// gets 200 — and the BFF returned `{ is_admin: boolean }`.
//
// Slice 130 (this slice): the slice-125 `/audit-log` route guard needs the
// caller's role list (not just `is_admin`) so auditors + grc_engineers can
// reach the page (slice-124 OPA gate permits all three). The BFF now
// proxies through GET /v1/me (which any signed-in caller can reach — no
// admin gate beyond authn) and forwards BOTH `is_admin` and `roles[]`.
//
// Wire shape (additive — slice 060 consumers that read only `is_admin`
// continue to work unchanged):
//
//   {
//     is_admin: boolean,
//     roles:    string[]   // always present; empty array when none
//   }
//
// Auth posture (unchanged from slice 060):
//   - No session cookie       -> 401, { is_admin: false, roles: [] }
//   - Upstream 200             -> 200, { is_admin, roles } from upstream
//   - Upstream 401             -> 401, { is_admin: false, roles: [] }
//   - Upstream 403             -> 200, { is_admin: false, roles: [] }
//   - Upstream other (5xx)     -> 502, { is_admin: false, roles: [], error }
//
// Tenant id and credential fingerprint stay on the platform; the BFF
// surfaces only the two role-related signals the frontend needs to render.
//
// P0-A1 (slice 130): we never trust client-supplied role claims. The BFF
// forwards ONLY the bearer; the platform reads roles from the user_roles
// table under tenant RLS.
//
// P0-A3 (slice 130): fail-closed. If the upstream returns no `roles`
// field (legacy / partial response / non-JSON body), the BFF defaults
// `roles` to []. Non-admin callers with empty `roles` get redirected by
// the layout — never silently admitted.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

type AdminMeBody = {
  is_admin: boolean;
  roles: string[];
  error?: string;
};

function emptyBody(): AdminMeBody {
  return { is_admin: false, roles: [] };
}

export async function GET() {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json(emptyBody(), { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/me`, {
    headers: { Authorization: `Bearer ${bearer}` },
  });
  if (upstream.status === 200) {
    let parsed: unknown;
    try {
      parsed = await upstream.json();
    } catch {
      // Non-JSON 200 body — degrade closed. Should never happen against
      // the real platform; defensive guard against a misconfigured
      // upstream proxy or partial-response.
      return NextResponse.json(emptyBody(), { status: 200 });
    }
    const body = parsed as { is_admin?: unknown; roles?: unknown };
    const isAdmin = body.is_admin === true;
    const roles = Array.isArray(body.roles)
      ? body.roles.filter((r): r is string => typeof r === "string")
      : [];
    return NextResponse.json({ is_admin: isAdmin, roles });
  }
  if (upstream.status === 403) {
    // Shouldn't normally happen on /v1/me (any signed-in caller can read
    // their own profile), but if the platform ever tightens the gate we
    // degrade to the same shape as a non-admin reader.
    return NextResponse.json(emptyBody(), { status: 200 });
  }
  if (upstream.status === 401) {
    return NextResponse.json(emptyBody(), { status: 401 });
  }
  return NextResponse.json(
    { ...emptyBody(), error: `upstream ${upstream.status}` },
    { status: 502 },
  );
}
