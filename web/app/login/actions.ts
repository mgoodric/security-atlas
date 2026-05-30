"use server";

import { cookies, headers } from "next/headers";
import { redirect } from "next/navigation";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { safeRedirectTarget } from "@/lib/safe-redirect";
import { shouldUseSecureCookie } from "@/lib/secure-cookie";

// signIn stores the supplied bearer token in an httpOnly cookie. Dev-mode
// auth: the human pastes a token from `atlas-cli credentials issue` or
// from the platform's stderr bootstrap output. Slice 034 replaces this with
// an OIDC redirect.
//
// Slice 073 — after the cookie is set, server-side POST the bearer to
// /v1/install/mark-first-signin so the platform flips the
// first_signin_at marker AND atomically deletes the bootstrap-token
// file (load-bearing P0-A1 safety property). The call is best-effort:
// any failure is logged server-side but does NOT block the redirect to
// the dashboard. The handler is idempotent platform-side, so subsequent
// sign-ins are no-ops.
//
// Slice 086 — every redirect target sourced from the `from` form field
// flows through `safeRedirectTarget` (web/lib/safe-redirect.ts) before
// reaching `redirect()`. Defends against the HIGH open-redirect finding
// in the 2026-Q2 security audit. Two call sites below:
//
//   1. Empty-token error branch — `target` is re-encoded into the
//      `?from=` query param of the error redirect. The error redirect
//      lands back on `/login`, which re-fires `signIn` with the same
//      `from`; validating here prevents the error path from carrying a
//      poisoned target forward into a second-order attack.
//   2. Happy-path redirect — the post-sign-in destination.
//
// Both call sites fall back to `/dashboard` on any non-safe target.
export async function signIn(formData: FormData): Promise<void> {
  const token = String(formData.get("token") ?? "").trim();
  const target = safeRedirectTarget(
    String(formData.get("from") ?? "/dashboard"),
  );

  if (!token) {
    redirect(
      `/login?error=${encodeURIComponent("token is required")}` +
        `&from=${encodeURIComponent(target)}`,
    );
  }

  const jar = await cookies();
  // Slice 146 — pick `secure` based on the actual transport (per-request,
  // via X-Forwarded-Proto / Forwarded) rather than a blunt build-time
  // NODE_ENV check. The old check broke every self-hosted operator that
  // serves the production-build standalone over plain HTTP: Secure cookies
  // were emitted but the browser refused to round-trip them, the BFF saw
  // no cookie, web/proxy.ts redirected /api/dashboard/** to /login, and
  // panels rendered "Unexpected token '<'". See web/lib/secure-cookie.ts +
  // docs/runbooks/bff-cookie-forwarding.md.
  const reqHeaders = await headers();
  jar.set(ATLAS_JWT_COOKIE, token, {
    httpOnly: true,
    sameSite: "lax",
    secure: shouldUseSecureCookie(reqHeaders),
    path: "/",
    // 8 hours — short-lived, matching the typical CI bearer's TTL.
    maxAge: 60 * 60 * 8,
  });

  // Slice 073 — fire-and-forget mark-first-signin. Direct upstream call
  // (the bearer is in scope here); the dedicated BFF route exists for
  // client-side fallback paths and tests. Failures are swallowed so the
  // sign-in flow never blocks on a metadata write.
  try {
    await fetch(`${apiBaseURL()}/v1/install/mark-first-signin`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  } catch {
    // Intentionally swallowed — P0-A5: existing sign-in flow preserved.
  }

  redirect(target);
}

export async function signOut(): Promise<void> {
  const jar = await cookies();
  jar.delete(ATLAS_JWT_COOKIE);
  redirect("/login");
}

// signInLocal — slice 209. POSTs (tenant_id, email, password) to
// /auth/local/login and stores the response token in ATLAS_JWT_COOKIE —
// the same cookie the OAuth callback finalize endpoint writes (slice 189).
// 401 redirects with "invalid credentials" (no oracle — same message
// regardless of email-exists vs password-mismatched).
export async function signInLocal(formData: FormData): Promise<void> {
  const tenantID = String(formData.get("tenant_id") ?? "").trim();
  const email = String(formData.get("email") ?? "").trim();
  const password = String(formData.get("password") ?? "");
  const target = safeRedirectTarget(
    String(formData.get("from") ?? "/dashboard"),
  );

  if (!tenantID || !email || !password) {
    redirect(
      `/login?error=${encodeURIComponent("email and password are required")}` +
        `&from=${encodeURIComponent(target)}`,
    );
  }

  let response: Response;
  try {
    response = await fetch(`${apiBaseURL()}/auth/local/login`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        tenant_id: tenantID,
        email,
        password,
      }),
    });
  } catch {
    // Network error reaching the backend — surface as a non-credential
    // error so the operator knows it's infrastructure, not credentials.
    redirect(
      `/login?error=${encodeURIComponent("sign-in service unavailable")}` +
        `&from=${encodeURIComponent(target)}`,
    );
  }

  if (response.status === 401) {
    // No oracle — same message regardless of email-exists vs. wrong-password.
    redirect(
      `/login?error=${encodeURIComponent("invalid credentials")}` +
        `&from=${encodeURIComponent(target)}`,
    );
  }
  if (!response.ok) {
    redirect(
      `/login?error=${encodeURIComponent("sign-in failed")}` +
        `&from=${encodeURIComponent(target)}`,
    );
  }

  type LocalLoginResponse = {
    user_id: string;
    tenant_id: string;
    display: string;
    token?: string;
  };
  const body: LocalLoginResponse = await response.json();
  if (!body.token) {
    // Backend didn't include a token — atlas was started without the
    // OAuth signer wired. Log the specific cause server-side; surface a
    // generic message to the operator.
    console.warn(
      "signInLocal: backend response missing token; ATLAS_ISSUER_URL likely unset",
    );
    redirect(
      `/login?error=${encodeURIComponent(
        "sign-in is not configured on this server",
      )}` + `&from=${encodeURIComponent(target)}`,
    );
  }

  const jar = await cookies();
  const reqHeaders = await headers();
  jar.set(ATLAS_JWT_COOKIE, body.token, {
    httpOnly: true,
    sameSite: "lax",
    secure: shouldUseSecureCookie(reqHeaders),
    path: "/",
    // 1 hour — matches AccessTokenLifetime (oauthapi.AccessTokenLifetime)
    // and the JWT exp the backend minted. Cookie expiry tracks token
    // expiry; the platform's JWT validator is the source of truth either way.
    maxAge: 60 * 60,
  });

  redirect(target);
}
