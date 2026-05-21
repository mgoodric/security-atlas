// oauth-client.ts — slice 189 frontend OAuth client.
//
// Drives the RFC 6749 §4.1 Authorization Code grant with PKCE (RFC
// 7636 §4.2 S256). Generates a code_verifier per RFC 7636 §4.1
// guidance (32 random bytes → base64url no padding), derives the
// SHA-256 challenge, persists the verifier in sessionStorage (P0-189-8
// — NOT localStorage), and redirects the browser to /oauth/authorize.
//
// On return, the Next.js route handler at /oauth/callback consumes
// `?code=&state=`, reads the verifier from sessionStorage, exchanges
// for the JWT via POST /oauth/token, and sets the httpOnly cookie.
//
// SECURITY:
//
//   - verifier lives in sessionStorage ONLY (P0-189-8); cleared on
//     successful completion or on logout.
//   - state is generated client-side as a CSRF guard for the
//     callback; verified server-side AND client-side.
//   - The JWT cookie set by the callback route is HttpOnly + Secure +
//     SameSite=Lax (P0-189-9) — written by the route handler, not
//     this module.

const VERIFIER_BYTES = 32;
const STATE_BYTES = 16;

// sessionStorage key for the in-flight code_verifier. Per
// P0-189-8 sessionStorage is used (not localStorage) so the verifier
// is scoped to the browsing tab and does not persist beyond it.
export const VERIFIER_STORAGE_KEY = "atlas:oauth_code_verifier";

// sessionStorage key for the in-flight CSRF state.
export const STATE_STORAGE_KEY = "atlas:oauth_state";

// sessionStorage key for the original return URL the user was on
// when the login flow started (the callback redirects here after
// minting the JWT cookie).
export const RETURN_TO_STORAGE_KEY = "atlas:oauth_return_to";

// Encode bytes as URL-safe base64 with NO padding (RFC 7636 §4.1).
function base64UrlEncode(bytes: Uint8Array): string {
  let binary = "";
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i]);
  }
  // btoa → standard base64 → strip padding → swap +/ for -_
  const standard = btoa(binary);
  return standard.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// Generate `count` cryptographically random bytes.
function randomBytes(count: number): Uint8Array {
  const buf = new Uint8Array(count);
  crypto.getRandomValues(buf);
  return buf;
}

// generateCodeVerifier returns a fresh RFC 7636 §4.1 code_verifier —
// 32 random bytes encoded as URL-safe base64 with no padding.
// Resulting string length is 43 characters.
export function generateCodeVerifier(): string {
  return base64UrlEncode(randomBytes(VERIFIER_BYTES));
}

// generateCodeChallenge computes the RFC 7636 §4.2 S256 challenge:
// base64url-without-padding(sha256(verifier)).
export async function generateCodeChallenge(verifier: string): Promise<string> {
  const data = new TextEncoder().encode(verifier);
  const hash = await crypto.subtle.digest("SHA-256", data);
  return base64UrlEncode(new Uint8Array(hash));
}

// generateState returns a fresh CSRF state value — 16 random bytes
// encoded base64url. Long enough that guessing is computationally
// infeasible.
export function generateState(): string {
  return base64UrlEncode(randomBytes(STATE_BYTES));
}

// InitLoginFlowParams gathers the per-call configuration the
// frontend needs to start an OAuth Authorization Code + PKCE flow.
export interface InitLoginFlowParams {
  // The atlas instance's externally-reachable URL (must match the
  // issuer the authorize endpoint validates against).
  issuer: string;
  // The OAuth client_id assigned to the web frontend at operator
  // setup time.
  clientId: string;
  // The redirect URI registered via `atlas-cli oauth add-redirect-uri`.
  redirectUri: string;
  // The tenant the user is signing in for. v1 single-tenant; slice
  // 192 multi-tenant work will derive this dynamically.
  tenantId: string;
  // The URL to return the user to after successful sign-in. Stored
  // in sessionStorage; the callback route reads it on completion.
  returnTo?: string;
}

// initLoginFlow starts the OAuth Authorization Code + PKCE flow:
//
//   1. Generate verifier + challenge + state.
//   2. Persist verifier + state in sessionStorage.
//   3. Build the /oauth/authorize URL and redirect the browser.
//
// The function does NOT return — control transfers to the IdP / atlas
// AS via window.location.assign.
export async function initLoginFlow(params: InitLoginFlowParams): Promise<void> {
  const verifier = generateCodeVerifier();
  const challenge = await generateCodeChallenge(verifier);
  const state = generateState();

  sessionStorage.setItem(VERIFIER_STORAGE_KEY, verifier);
  sessionStorage.setItem(STATE_STORAGE_KEY, state);
  if (params.returnTo) {
    sessionStorage.setItem(RETURN_TO_STORAGE_KEY, params.returnTo);
  }

  const url = new URL("/oauth/authorize", params.issuer);
  url.searchParams.set("response_type", "code");
  url.searchParams.set("client_id", params.clientId);
  url.searchParams.set("redirect_uri", params.redirectUri);
  url.searchParams.set("scope", "openid");
  url.searchParams.set("state", state);
  url.searchParams.set("code_challenge", challenge);
  url.searchParams.set("code_challenge_method", "S256");
  url.searchParams.set("tenant_id", params.tenantId);

  window.location.assign(url.toString());
}

// CompleteLoginFlowParams is what the Next.js callback route hands
// off to completeLoginFlow.
export interface CompleteLoginFlowParams {
  // The atlas issuer URL.
  issuer: string;
  // The OAuth client_id.
  clientId: string;
  // The redirect_uri used at the authorize step (MUST match exactly).
  redirectUri: string;
  // The authorization code from the callback query string.
  code: string;
  // The state from the callback query string. Compared to the
  // sessionStorage value as a CSRF guard.
  state: string;
}

// CompleteLoginFlowResult captures the JWT-bearing response from the
// token endpoint. The Next.js route handler converts this into a
// httpOnly cookie via Set-Cookie.
export interface CompleteLoginFlowResult {
  accessToken: string;
  tokenType: string;
  expiresIn: number;
  returnTo: string | null;
}

// completeLoginFlow exchanges the code+verifier for a JWT via POST
// /oauth/token. Validates state against the sessionStorage value as a
// CSRF guard. On success, clears the sessionStorage keys.
//
// Throws on:
//   - state mismatch (CSRF guard trip)
//   - missing verifier (sessionStorage cleared mid-flow)
//   - non-2xx response from /oauth/token (the error body is included
//     in the thrown Error message)
export async function completeLoginFlow(
  params: CompleteLoginFlowParams,
): Promise<CompleteLoginFlowResult> {
  const storedState = sessionStorage.getItem(STATE_STORAGE_KEY);
  if (!storedState || storedState !== params.state) {
    throw new Error("oauth: state mismatch (CSRF guard tripped)");
  }
  const verifier = sessionStorage.getItem(VERIFIER_STORAGE_KEY);
  if (!verifier) {
    throw new Error("oauth: code_verifier missing from sessionStorage");
  }

  const returnTo = sessionStorage.getItem(RETURN_TO_STORAGE_KEY);

  const tokenUrl = new URL("/oauth/token", params.issuer);
  const form = new URLSearchParams();
  form.set("grant_type", "authorization_code");
  form.set("code", params.code);
  form.set("code_verifier", verifier);
  form.set("redirect_uri", params.redirectUri);
  form.set("client_id", params.clientId);

  const resp = await fetch(tokenUrl.toString(), {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form.toString(),
  });
  if (!resp.ok) {
    const body = await resp.text();
    throw new Error(`oauth: token exchange failed (${resp.status}): ${body}`);
  }
  const data = (await resp.json()) as {
    access_token: string;
    token_type: string;
    expires_in: number;
  };

  // Clear sessionStorage on success.
  sessionStorage.removeItem(VERIFIER_STORAGE_KEY);
  sessionStorage.removeItem(STATE_STORAGE_KEY);
  sessionStorage.removeItem(RETURN_TO_STORAGE_KEY);

  return {
    accessToken: data.access_token,
    tokenType: data.token_type,
    expiresIn: data.expires_in,
    returnTo,
  };
}
