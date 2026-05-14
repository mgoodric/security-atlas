// Slice 042 — server-side audit period resolution.
//
// The /audit pages are server components: they resolve the caller's
// assigned AuditPeriod before rendering so the shell has the period in
// hand on first paint (no loading flash for AC-1). This calls the
// platform directly with the bearer cookie — the same pattern the admin
// layout uses, but reading the period instead of the admin flag.
//
// P0-1: the platform scopes /v1/me/audit-period to the caller's UserID.
// This function passes no tenant or period id — the auditor can only
// ever resolve their OWN assigned period.

import { cookies } from "next/headers";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";
import type { AuditPeriod } from "@/lib/api/audit";

export type PeriodResolution =
  | { kind: "ok"; period: AuditPeriod }
  | { kind: "unauthenticated" }
  | { kind: "no-period" }
  | { kind: "error"; status: number };

export async function resolveAuditPeriod(): Promise<PeriodResolution> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) return { kind: "unauthenticated" };

  const res = await fetch(`${apiBaseURL()}/v1/me/audit-period`, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  if (res.status === 404) return { kind: "no-period" };
  if (res.status === 401) return { kind: "unauthenticated" };
  if (!res.ok) return { kind: "error", status: res.status };

  const body = (await res.json()) as { audit_period: AuditPeriod };
  return { kind: "ok", period: body.audit_period };
}
