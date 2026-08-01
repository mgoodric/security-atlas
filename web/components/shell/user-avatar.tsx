// Slice 213 — user-avatar component, rendered in the shared authed-shell
// topbar. Closes the audits-page header chrome parity gap surfaced by
// slice 204's audit fleet (and visible on every authed page, since the
// chrome is shared — AC-2).
//
// Design:
//
//   - Server component (mirrors slice 186 `sidebar.tsx`): reads the
//     bearer cookie server-side, calls the platform's `/v1/me` (the same
//     upstream the slice 108 BFF GET handler proxies), and renders the
//     initials + display name in markup the client receives whole. No
//     client-side state.
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

import { cookies } from "next/headers";

import { deriveDisplayName, deriveInitials } from "@/lib/display-name";
import { fetchSelfProfile } from "@/lib/api/self.server";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

interface MeBody {
  display_name?: unknown;
  email?: unknown;
}

/**
 * Fetches the operator's profile from the platform's `/v1/me`. Returns
 * `null` on any failure so the caller can collapse to "render nothing".
 *
 * OE-549: this was a self-referential fetch of the app's own `/api/me`
 * BFF route at an origin rebuilt from the request Host header. Inside
 * the web container that address is refused (the Host carries the
 * EXTERNAL published port; next-server binds the container IP, not
 * loopback), so the avatar silently never rendered on ANY authed page —
 * the same defect that 500'd `/admin/*`, just fail-soft. It now reads
 * the platform through the stable internal base.
 */
async function fetchMe(): Promise<MeBody | null> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) return null;
  return fetchSelfProfile(bearer);
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
      <span data-testid="user-avatar-name" className="text-sm text-foreground">
        {name}
      </span>
    </div>
  );
}
