// Auth helpers. Slice 005 used a dev-mode bearer-token cookie; slice 034
// added an OIDC flow; slice 189 set the canonical `atlas_jwt` cookie via the
// OAuth callback; slice 197 retired the legacy bearer middleware on the
// backend; **slice 206** migrates the BFF cookie name so this constant
// resolves to the same cookie the OAuth callback writes (`atlas_jwt`).
//
// The cookie is httpOnly + sameSite=lax; secure when the request transport
// is HTTPS (see `lib/secure-cookie.ts`).
//
// **slice 397** completes the cleanup deferred at slice 206: the constant
// was originally named `SESSION_COOKIE` (a misleading name flagged by the
// slice 328 audit as finding M-3, since it resolves to the JWT cookie
// `atlas_jwt`, not a generic session). It is now `ATLAS_JWT_COOKIE`. This
// was a pure symbol rename — the cookie value (`atlas_jwt`) and all wire
// behavior are unchanged.
//
// Operator note: existing browser sessions hold a `sa_session_token` cookie
// that is now ignored. Users will be redirected to `/login` once; the OAuth
// flow will set `atlas_jwt` and the loop resolves.
export const ATLAS_JWT_COOKIE = "atlas_jwt";

// OIDC_SESSION_COOKIE is the slice 034 session-id cookie set by the OIDC
// login callback. Distinct from ATLAS_JWT_COOKIE (the api_keys bearer).
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
