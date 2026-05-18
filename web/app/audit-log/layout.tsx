// Slice 125 + Slice 130 — server-component layout for /audit-log.
//
// AC-5 + P0-A2 (slice 125). The route lives OUTSIDE the slice-060 `/admin/*`
// tree on purpose: the slice doc calls the path `/audit-log` literally, and
// the page is reachable to admins + auditors + grc_engineers (slice-124 OPA
// gate) — wider than the strict admin-only `/admin/*` group.
//
// Three layers of defense:
//   1. proxy.ts (Next 16 request interceptor) gates ALL non-exempt paths
//      on the session cookie; unauthenticated traffic gets bounced to
//      /login before this layout renders.
//   2. THIS LAYOUT issues a /api/admin/me preflight on the server (so a
//      stale client-side cache cannot bypass it). Callers without one of
//      { admin, auditor, grc_engineer } get a server-side
//      `redirect("/dashboard?error=admin-only")`, preserving the
//      slice-125-spec's redirect-with-error UX.
//   3. The backend (slice-124) is the third leg of the defense — it
//      rejects non-{admin,auditor,grc_engineer} bearers with 403
//      regardless of which UI reaches it.
//
// Slice 130: the gate now consults `roles[]` in addition to `is_admin`.
// Three callers are admitted: an admin (`is_admin === true`), an auditor
// (`roles.includes("auditor")`), or a grc_engineer
// (`roles.includes("grc_engineer")`). These three role checks match
// slice-124's `HasUnifiedAuditLogRole` SQL exactly.
//
// Fail-closed posture (P0-A3): when the BFF returns no `roles` array
// (legacy upstream / BFF error / network blip), the layout treats it as
// `[]` and admits only on `is_admin === true`. Non-admins with a missing
// `roles` array see the redirect — identical to the pre-slice-130 behavior.
// Never silently admit on a missing field.

import { cookies, headers } from "next/headers";
import { redirect } from "next/navigation";

import { SESSION_COOKIE } from "@/lib/auth";

// AUDIT_LOG_ROLES — the canonical set of roles permitted to reach
// /audit-log. MUST match `internal/db/queries/unified_audit_log_role.sql`
// (slice 124) — a divergence between this constant and that SQL is a
// silent UI/backend gap. Treat as one literal in two languages.
const AUDIT_LOG_ROLES = ["admin", "auditor", "grc_engineer"] as const;
type AuditLogRole = (typeof AUDIT_LOG_ROLES)[number];

/**
 * canReachAuditLog encodes the slice-130 widened route-guard predicate.
 * Exported so the slice-130 vitest test can exercise it in isolation
 * without spinning up the layout's `fetch` round-trip.
 *
 * P0-A3 fail-closed: a missing or non-array `roles` field collapses to
 * `[]`; the predicate then admits ONLY on `is_admin === true`.
 *
 * P0-A4 server-side: this function is exported as pure logic; the layout
 * default-export below is the only consumer in the production tree, and
 * it runs in a server component.
 */
export function canReachAuditLog(body: {
  is_admin?: boolean;
  roles?: unknown;
}): boolean {
  if (body.is_admin === true) {
    return true;
  }
  if (!Array.isArray(body.roles)) {
    return false;
  }
  return body.roles.some(
    (r): r is AuditLogRole =>
      typeof r === "string" &&
      (AUDIT_LOG_ROLES as readonly string[]).includes(r),
  );
}

async function fetchAdminMe(bearer: string): Promise<{
  is_admin?: boolean;
  roles?: unknown;
}> {
  const h = await headers();
  const host = h.get("host") ?? "localhost:3000";
  const proto = h.get("x-forwarded-proto") ?? "http";
  const res = await fetch(`${proto}://${host}/api/admin/me`, {
    headers: { Cookie: `${SESSION_COOKIE}=${bearer}` },
    cache: "no-store",
  });
  if (!res.ok) return {};
  return (await res.json()) as { is_admin?: boolean; roles?: unknown };
}

export default async function AuditLogLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    redirect("/login?from=/audit-log");
  }
  const body = await fetchAdminMe(bearer);
  if (!canReachAuditLog(body)) {
    redirect("/dashboard?error=admin-only");
  }
  return <>{children}</>;
}
