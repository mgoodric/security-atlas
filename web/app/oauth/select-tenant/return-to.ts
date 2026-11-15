// return-to.ts — the select-tenant page's `return_to` read, split out of
// page.tsx so a node-env vitest can cover it (vitest runs no JSX).
//
// #2196 — `return_to` is user input that reaches router.replace(), so it
// goes through the open-redirect helper like every other redirect target.
import { safeRedirectTarget } from "@/lib/safe-redirect";

export function selectTenantReturnTo(
  searchParams: Pick<URLSearchParams, "get"> | null | undefined,
): string {
  return safeRedirectTarget(searchParams?.get("return_to") || "/dashboard");
}
