// Server-side resolution of the `actor=me` sentinel (slice 270 D5, #2430).
//
// The Activity BFF and the audit-log export BFF both accept `?actor=me`
// and replace it with the caller's own user_id before forwarding. The id
// comes from GET /v1/me under the caller's own bearer, never from the
// client, so a browser cannot ask for "me" and get someone else.

import { apiBaseURL } from "@/lib/api/base";

export const ACTOR_ME_SENTINEL = "me";

// resolveCallerUserID fetches GET /v1/me with the bearer and returns the
// response's `user_id`, or null when /v1/me is unreachable, refuses, or
// carries no user_id.
export async function resolveCallerUserID(
  bearer: string,
): Promise<string | null> {
  try {
    const meRes = await fetch(`${apiBaseURL()}/v1/me`, {
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    });
    if (!meRes.ok) return null;
    const body = (await meRes.json()) as { user_id?: unknown };
    if (typeof body.user_id === "string" && body.user_id.length > 0) {
      return body.user_id;
    }
    return null;
  } catch {
    return null;
  }
}
