// Slice 213 — user-avatar component, rendered in the shared authed-shell
// topbar. Closes the audits-page header chrome parity gap surfaced by
// slice 204's audit fleet (and visible on every authed page, since the
// chrome is shared — AC-2).
//
// Design:
//
//   - Server component (mirrors slice 186 `sidebar.tsx`): reads the
//     bearer cookie server-side, calls the existing BFF `/api/me` (slice
//     108 GET handler), and renders the initials + display name in
//     markup the client receives whole. No client-side state.
//   - Fail closed: any fetch error / missing bearer / unparseable body
//     renders NULL. Better a brief gap than the wrong identity.
//     (Parallels P0-186-4 from the sidebar admin-role-gate.)
//   - Reads display_name + email from `/v1/me` via the BFF — no new
//     endpoint (P0-213-1). The user-context source is the existing
//     slice 108 endpoint (P0-213-4 — no mock).
//
// Constitutional invariants:
//   - Invariant 6 (tenant isolation): the BFF forwards the bearer
//     cookie; the platform's /v1/me handler reads the bearer-bound
//     user record. The avatar never reads or forwards a tenant_id.

import { cookies, headers } from "next/headers";

import { deriveDisplayName, deriveInitials } from "@/lib/display-name";
import { SESSION_COOKIE } from "@/lib/auth";

interface MeBody {
  display_name?: unknown;
  email?: unknown;
}

/**
 * Fetches the operator's profile via the BFF `/api/me` route. Mirrors
 * the slice 186 `fetchAdminMe` shape — self-referential fetch via
 * host + proto so the call resolves whether we're rendering on the
 * server-rendered page or in dev with the proxy. Returns `null` on any
 * failure so the caller can collapse to "render nothing".
 */
async function fetchMe(): Promise<MeBody | null> {
  try {
    const jar = await cookies();
    const bearer = jar.get(SESSION_COOKIE)?.value;
    if (!bearer) return null;
    const h = await headers();
    const host = h.get("host") ?? "localhost:3000";
    const proto = h.get("x-forwarded-proto") ?? "http";
    const res = await fetch(`${proto}://${host}/api/me`, {
      headers: { Cookie: `${SESSION_COOKIE}=${bearer}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as MeBody;
  } catch {
    return null;
  }
}

function asString(v: unknown): string {
  return typeof v === "string" ? v : "";
}

export async function UserAvatar() {
  const me = await fetchMe();
  if (!me) return null;

  const profile = {
    display_name: asString(me.display_name),
    email: asString(me.email),
  };
  const name = deriveDisplayName(profile);
  if (name.length === 0) return null;

  const initials = deriveInitials(name);

  return (
    <div
      data-testid="user-avatar"
      className="flex items-center gap-2 pl-3 border-l border-border"
    >
      <div
        aria-hidden
        data-testid="user-avatar-initials"
        className="w-7 h-7 rounded-full bg-primary/15 text-primary flex items-center justify-center text-xs font-semibold"
      >
        {initials}
      </div>
      <span
        data-testid="user-avatar-name"
        className="text-sm text-foreground"
      >
        {name}
      </span>
    </div>
  );
}
