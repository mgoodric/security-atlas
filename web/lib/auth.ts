// Auth helpers. Slice 005 used a dev-mode bearer-token cookie; slice 034
// added an OIDC flow; slice 189 set the canonical `atlas_jwt` cookie via the
// OAuth callback; slice 197 retired the legacy bearer middleware on the
// backend; **slice 206** migrates the BFF cookie name so this constant
// resolves to the same cookie the OAuth callback writes (`atlas_jwt`).
//
// The cookie is httpOnly + sameSite=lax; secure when the request transport
// is HTTPS (see `lib/secure-cookie.ts`).
//
// The constant NAME stays `SESSION_COOKIE` so the 30+ import sites that
// already reference it compile unchanged — only the value changes. A
// follow-on cleanup slice may rename the symbol to `ATLAS_JWT_COOKIE` once
// the migration is settled, but for the v1.14.0 hot-fix we minimise
// blast radius.
//
// Operator note: existing browser sessions hold a `sa_session_token` cookie
// that is now ignored. Users will be redirected to `/login` once; the OAuth
// flow will set `atlas_jwt` and the loop resolves.
export const SESSION_COOKIE = "atlas_jwt";

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
