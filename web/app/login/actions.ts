"use server";

import { cookies, headers } from "next/headers";
import { redirect } from "next/navigation";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";
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
  jar.set(SESSION_COOKIE, token, {
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
  jar.delete(SESSION_COOKIE);
  redirect("/login");
}
