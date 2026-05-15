"use server";

import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

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
export async function signIn(formData: FormData): Promise<void> {
  const token = String(formData.get("token") ?? "").trim();
  const target = String(formData.get("from") ?? "/dashboard");

  if (!token) {
    redirect(
      `/login?error=${encodeURIComponent("token is required")}` +
        (target ? `&from=${encodeURIComponent(target)}` : ""),
    );
  }

  const jar = await cookies();
  jar.set(SESSION_COOKIE, token, {
    httpOnly: true,
    sameSite: "lax",
    secure: process.env.NODE_ENV === "production",
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

  redirect(target || "/dashboard");
}

export async function signOut(): Promise<void> {
  const jar = await cookies();
  jar.delete(SESSION_COOKIE);
  redirect("/login");
}
