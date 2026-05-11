"use server";

import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { SESSION_COOKIE } from "@/lib/auth";

// signIn stores the supplied bearer token in an httpOnly cookie. Dev-mode
// auth: the human pastes a token from `atlas-cli credentials issue` or
// from the platform's stderr bootstrap output. Slice 034 replaces this with
// an OIDC redirect.
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
  redirect(target || "/dashboard");
}

export async function signOut(): Promise<void> {
  const jar = await cookies();
  jar.delete(SESSION_COOKIE);
  redirect("/login");
}
