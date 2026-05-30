// Slice 091 — root-route redirect.
//
// Replaces the stock create-next-app template that shipped with the
// Next.js scaffold in slice 005. The route never renders UI: signed-in
// users land on `/dashboard`, unauthenticated users land on
// `/login?from=/` so the existing login flow preserves the original
// destination per slice 086's safe-redirect helper.
//
// Design constraints (slice 091 P0-A1..A4):
//   * Server component only — no client-side flash of unstyled content.
//   * Two destinations only — no third "/onboarding" branch.
//   * Reads the existing `ATLAS_JWT_COOKIE` from `@/lib/auth`. Does NOT
//     modify cookie name, expiry, or middleware order.
//   * No rendered content. Future marketing or tenant-picker UI ships
//     as its own slice.

import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export default async function Home() {
  const session = (await cookies()).get(ATLAS_JWT_COOKIE)?.value;
  redirect(session ? "/dashboard" : "/login?from=/");
}
