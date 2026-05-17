// Auth helpers. Slice 005 uses a dev-mode bearer-token cookie; slice 034
// replaces this with a real OIDC flow. The cookie is httpOnly + sameSite=lax;
// secure=true in production.

export const SESSION_COOKIE = "sa_session_token";

// OIDC_SESSION_COOKIE is the slice 034 session-id cookie set by the OIDC
// login callback. Distinct from SESSION_COOKIE (the api_keys bearer).
//
// SCOPE — this cookie value is forwarded by the BFF to the platform ONLY
// on the /api/me/sessions* routes (slice 110). Do NOT forward it on any
// other route: the platform handlers outside /v1/me/sessions* have no
// reason to see the OIDC session id, and broadening the surface area
// would leak the id into request paths it does not belong on.
export const OIDC_SESSION_COOKIE = "atlas_session";

export type Session = {
  bearer: string;
};
