// Slice 110 — shared header builder for the /api/me/sessions* BFF routes.
//
// SCOPE — colocated INSIDE the sessions directory on purpose. The
// atlas_session cookie is forwarded ONLY on these three routes
// (GET list, DELETE all-others, DELETE single). Other BFF routes
// MUST NOT import this helper; doing so would broaden the OIDC
// session id's surface area beyond what the slice 108 backend reads.
// See slice 110 P0-A2 + web/lib/auth.ts OIDC_SESSION_COOKIE comment.

// VALID_OIDC_SESSION_ID enforces the slice 034 session id alphabet
// (URL-safe base64, no padding — 43 chars per
// internal/auth/sessions/sessions.go). Anything that fails to match
// is silently dropped: the platform handler degrades to honest
// "no row flagged current", which is strictly better than allowing
// a header-injection vector via a malformed cookie value.
const VALID_OIDC_SESSION_ID = /^[A-Za-z0-9_-]+$/;

export type ForwardHeaders = {
  Authorization: string;
  Cookie?: string;
};

// buildSessionsForwardHeaders constructs the outbound header bag the
// /api/me/sessions* BFF routes send to the platform. Always carries
// `Authorization: Bearer <bearer>`. Adds `Cookie: atlas_session=<value>`
// IFF the cookie is present AND matches the URL-safe-base64 alphabet.
export function buildSessionsForwardHeaders(
  bearer: string,
  oidcSessionCookie: string | undefined,
): ForwardHeaders {
  const headers: ForwardHeaders = { Authorization: `Bearer ${bearer}` };
  if (oidcSessionCookie && VALID_OIDC_SESSION_ID.test(oidcSessionCookie)) {
    headers.Cookie = `atlas_session=${oidcSessionCookie}`;
  }
  return headers;
}
