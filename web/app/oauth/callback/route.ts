// /oauth/callback — slice 189 frontend OAuth callback route handler.
//
// The atlas authorize endpoint redirects the browser here after a
// successful code issuance:
//
//   GET /oauth/callback?code=<code>&state=<state>
//
// This handler:
//
//   1. (Client-side) reads the code+state from the query string,
//      reads the verifier from sessionStorage, exchanges with the
//      atlas /oauth/token endpoint via completeLoginFlow.
//   2. On success, sets the atlas_jwt cookie (D1 — see decisions
//      log) with HttpOnly + Secure + SameSite=Lax (P0-189-9).
//   3. Redirects the user to the stored return_to URL or `/` on
//      missing.
//
// LIMITATION — v1 puts the token-exchange + cookie-set on the
// CLIENT side because the verifier lives in sessionStorage (P0-189-8
// — NOT in a server-side store). The Next.js route handler still
// renders here to provide the page entry point, but the actual JWT
// cookie write happens via a `fetch('/oauth/callback/finalize')`
// POST that this client-side script makes after reading
// sessionStorage. The finalize endpoint is the only server-side
// surface that writes the JWT cookie.
//
// Why this shape: Next.js route handlers cannot read browser
// sessionStorage. The verifier MUST stay in the browser per
// P0-189-8. So the flow is:
//
//   browser hits /oauth/callback?code=...
//     ↓ (client component renders, reads sessionStorage)
//   browser POSTs to /oauth/token directly (with code + verifier)
//     ↓ (response = JWT)
//   browser POSTs JWT to /oauth/callback/finalize (server route)
//     ↓ (route handler sets atlas_jwt httpOnly cookie)
//   browser receives 302 to return_to

import { NextResponse, type NextRequest } from "next/server";

// JWT cookie name — D1 picks new `atlas_jwt` cookie alongside the
// existing slice-034 `atlas_session` opaque session-id cookie. Slice
// 190 will retire `atlas_session` cleanly.
export const ATLAS_JWT_COOKIE = "atlas_jwt";

// JWT cookie lifetime — matches the JWT exp (1 hour per slice 188
// AccessTokenLifetime). The cookie expires from the browser's
// perspective at the same time the token becomes invalid; the
// platform's JWT validator (slice 190) is the source of truth.
export const ATLAS_JWT_COOKIE_LIFETIME_SECONDS = 60 * 60;

// GET /oauth/callback renders the client-side script that completes
// the flow. The actual JWT-cookie write happens via POST
// /oauth/callback (see below).
//
// For v1 simplicity, we serve a minimal HTML page that loads the
// completion script. A future slice can promote this to a React
// Server Component when Next.js's hydration story for query-param-
// driven redirects matures.
export async function GET(request: NextRequest): Promise<NextResponse> {
  const url = new URL(request.url);
  const code = url.searchParams.get("code");
  const state = url.searchParams.get("state");
  const error = url.searchParams.get("error");

  if (error) {
    // The authorize endpoint surfaced an error per RFC 6749 §4.1.2.1.
    // Show a minimal error page; future UX can pretty this up.
    return new NextResponse(
      `<!DOCTYPE html><html><body><h1>Sign-in failed</h1><p>${escapeHtml(
        error,
      )}</p></body></html>`,
      { status: 400, headers: { "Content-Type": "text/html; charset=utf-8" } },
    );
  }

  if (!code || !state) {
    return new NextResponse(
      `<!DOCTYPE html><html><body><h1>Sign-in failed</h1><p>missing code or state</p></body></html>`,
      { status: 400, headers: { "Content-Type": "text/html; charset=utf-8" } },
    );
  }

  // The client-side script:
  //   1. Reads verifier + state from sessionStorage.
  //   2. POSTs to /oauth/token to exchange for the JWT.
  //   3. POSTs the JWT to /oauth/callback (this route, POST handler)
  //      so the server can set the httpOnly cookie.
  //   4. Redirects to return_to (read from sessionStorage).
  //
  // Embedded inline so the page works without a separate bundled
  // entry point; the script is small and the page's only job is
  // completing the flow.
  //
  // SECURITY (CodeQL alert #35 / slice 189 D5):
  //
  // `code` and `state` flow from URL query params (caller-controlled).
  // `JSON.stringify` produces a string-literal that is JS-safe but
  // NOT HTML-safe — a value containing `</script>` would close the
  // inline <script> context and allow attacker-controlled HTML to
  // follow. The jsonForScriptTag helper escapes the three characters
  // that matter inside a <script> tag (`<`, `>`, `&`) using their
  // Unicode escape sequences, which are valid JS string literals AND
  // invisible to the HTML parser.
  //
  // The error-handling branch uses textContent on freshly-constructed
  // DOM nodes rather than innerHTML so the error string is rendered
  // as text — no escape sequence required and no attribute-injection
  // surface.
  const script = `
    (async () => {
      try {
        const oauth = await import('/_next/static/chunks/oauth-client.js').catch(() => null);
        let completeLoginFlow;
        if (oauth && oauth.completeLoginFlow) {
          completeLoginFlow = oauth.completeLoginFlow;
        } else {
          const mod = await import('/lib/auth/oauth-client.ts');
          completeLoginFlow = mod.completeLoginFlow;
        }
        const issuer = window.location.origin;
        const clientId = document.querySelector('meta[name="atlas-oauth-client-id"]')?.content;
        const redirectUri = window.location.origin + '/oauth/callback';
        if (!clientId) throw new Error('atlas-oauth-client-id meta missing');
        const result = await completeLoginFlow({
          issuer, clientId, redirectUri,
          code: ${jsonForScriptTag(code)},
          state: ${jsonForScriptTag(state)},
        });
        // Hand the JWT off to the server-side cookie-setter.
        const finalize = await fetch('/oauth/callback', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ access_token: result.accessToken, expires_in: result.expiresIn }),
        });
        if (!finalize.ok) throw new Error('finalize failed: ' + finalize.status);
        window.location.assign(result.returnTo || '/');
      } catch (e) {
        // textContent renders as text — no HTML/attribute injection
        // surface, no escape sequence required.
        document.body.replaceChildren();
        const h1 = document.createElement('h1');
        h1.textContent = 'Sign-in failed';
        const p = document.createElement('p');
        p.textContent = (e && e.message) ? String(e.message) : 'unknown error';
        document.body.appendChild(h1);
        document.body.appendChild(p);
      }
    })();
  `;
  return new NextResponse(
    `<!DOCTYPE html><html><head><title>Completing sign-in...</title></head><body>` +
      `<p>Completing sign-in...</p><script type="module">${script}</script></body></html>`,
    { status: 200, headers: { "Content-Type": "text/html; charset=utf-8" } },
  );
}

// jsonForScriptTag serialises a string value as a JS-safe AND
// HTML-safe literal for embedding inside an inline <script> tag.
//
// The three escapes (`<`, `>`, `&`) are the standard JSON-in-HTML
// safety net per OWASP / Rails / Django guidance:
//
//   - `</script>` cannot terminate the script context (`<` escaped)
//   - HTML comment markers (`<!--`, `-->`) cannot start (`<`, `>` escaped)
//   - HTML entity sequences cannot smuggle through (`&` escaped)
//
// The resulting string is still valid JSON AND valid JS — Unicode
// escapes (`<` etc.) are parsed back to the original characters
// at runtime, so the value seen by the application code is unchanged.
//
// CodeQL alert #35 (XSS via inline script template).
export function jsonForScriptTag(v: unknown): string {
  return JSON.stringify(v)
    .replace(/</g, "\\u003c")
    .replace(/>/g, "\\u003e")
    .replace(/&/g, "\\u0026");
}

// POST /oauth/callback — the server-side finalize endpoint. Receives
// the JWT from the client-side completion script and sets it as a
// httpOnly+Secure+SameSite=Lax cookie (P0-189-9).
export async function POST(request: NextRequest): Promise<NextResponse> {
  let body: { access_token?: string; expires_in?: number };
  try {
    body = (await request.json()) as {
      access_token?: string;
      expires_in?: number;
    };
  } catch {
    return NextResponse.json({ error: "invalid_json" }, { status: 400 });
  }
  if (!body.access_token || typeof body.access_token !== "string") {
    return NextResponse.json(
      { error: "missing_access_token" },
      { status: 400 },
    );
  }
  const maxAge =
    typeof body.expires_in === "number" && body.expires_in > 0
      ? body.expires_in
      : ATLAS_JWT_COOKIE_LIFETIME_SECONDS;

  const resp = NextResponse.json({ ok: true });
  resp.cookies.set({
    name: ATLAS_JWT_COOKIE,
    value: body.access_token,
    httpOnly: true,
    secure: process.env.NODE_ENV === "production",
    sameSite: "lax",
    path: "/",
    maxAge,
  });
  return resp;
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}
