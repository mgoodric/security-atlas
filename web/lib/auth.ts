// Auth helpers. Slice 005 uses a dev-mode bearer-token cookie; slice 034
// replaces this with a real OIDC flow. The cookie is httpOnly + sameSite=lax;
// secure=true in production.

export const SESSION_COOKIE = "sa_session_token"

export type Session = {
  bearer: string
}
