// Slice 370 — /v1/me/* profile + preferences + sessions (slice 108 +
// slice 130/154/162), extracted from the former `web/lib/api.ts`
// god-file.

import { APIError } from "./base";

// ===== Slice 108 — /v1/me/* (profile + preferences + sessions) =====

export type MeProfile = {
  user_id: string;
  tenant_id: string;
  display_name: string;
  email: string;
  idp_subject: string;
  tenant_role: string;
  time_zone: string | null;
  is_admin: boolean;
  owner_roles: string[];
  // Slice 130 (extended by slice 154): canonical `user_roles` list.
  // Always present on the wire — empty array, never omitted — so
  // callers can rely on it without a nil-check. The Profile section
  // on /settings renders the additional roles (excluding the primary
  // admin/user already shown via the `is_admin` badge) as a muted
  // tail, mirroring the `Plans/_archive/mockups/settings.html` "admin +
  // grc_engineer" pattern.
  roles: string[];
};

export type MePatchRequest = {
  display_name?: string;
  time_zone?: string;
};

export type MePreferences = Record<string, Record<string, boolean>>;

// Slice 162: extended with `user_agent`, `ip_address`, `geo_country`, `geo_city`.
// All four are optional — the backend wire shape emits them with `omitempty`,
// so a row that was created before the slice-162 migration (or by a flow that
// had no http.Request in scope) arrives with the field absent. The settings
// page's session-line helper treats `undefined` identically to empty — honest
// empty render, no fabricated placeholder text (slice 162 P0-162-1).
export type MeSession = {
  id: string;
  last4: string;
  created_at: string;
  last_used_at: string | null;
  is_current: boolean;
  user_agent?: string;
  ip_address?: string;
  geo_country?: string;
  geo_city?: string;
};

export type MeSessionsResponse = {
  sessions: MeSession[];
  count: number;
};

// Browser-side fetchers — go through the BFF at /api/me/* so the session-cookie
// bearer is attached server-side. The BFF routes proxy to the platform /v1/me/*.

export async function getMe(): Promise<MeProfile> {
  const res = await fetch(`/api/me`, { cache: "no-store" });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as MeProfile;
}

export async function patchMe(body: MePatchRequest): Promise<MeProfile> {
  const res = await fetch(`/api/me`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON */
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as MeProfile;
}

export async function getMyPreferences(): Promise<MePreferences> {
  const res = await fetch(`/api/me/preferences`, { cache: "no-store" });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  const body = (await res.json()) as { preferences: MePreferences };
  return body.preferences;
}

export async function patchMyPreferences(
  partial: MePreferences,
): Promise<MePreferences> {
  const res = await fetch(`/api/me/preferences`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(partial),
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON */
    }
    throw new APIError(res.status, msg);
  }
  const body = (await res.json()) as { preferences: MePreferences };
  return body.preferences;
}

export async function listMySessions(): Promise<MeSession[]> {
  const res = await fetch(`/api/me/sessions`, { cache: "no-store" });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  const body = (await res.json()) as MeSessionsResponse;
  return body.sessions ?? [];
}

export async function revokeMySession(id: string): Promise<void> {
  const res = await fetch(`/api/me/sessions/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
  if (!res.ok && res.status !== 204) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
}
