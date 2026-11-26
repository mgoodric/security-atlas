// Slice 527 — pure option-mapping + tenant-field-mode logic for the
// admin user-assign dialog dropdowns.
//
// Extracted as a pure `.ts` module (no React) so the project's node-only
// vitest runner can cover it (slice 069 P0-A3 / decisions-log D2). The
// JSX wiring in `web/app/admin/users/page.tsx` consumes these helpers;
// Playwright covers the rendered behavior.

import type { AdminUserRow, TenantRow } from "@/lib/api/admin";

export type AssignUserOption = { value: string; label: string };
export type AssignTenantOption = { value: string; label: string };

// userOptions maps the already-loaded admin user list (AC-1: NO second
// fetch) to <select> options. The label is "display-name (email)", or
// just the email when no display name is set. The list is de-duplicated
// by user id: the super_admin cross-tenant shape carries one row per
// user-in-tenant membership, so the same person can appear N times; the
// dialog targets a user identity (the tenant is chosen separately), so
// one option per person is correct (decisions-log D4).
export function userOptions(rows: AdminUserRow[]): AssignUserOption[] {
  const seen = new Set<string>();
  const opts: AssignUserOption[] = [];
  for (const r of rows) {
    if (seen.has(r.id)) continue;
    seen.add(r.id);
    const name = (r.display_name ?? "").trim();
    const email = (r.email ?? "").trim();
    const label = name !== "" ? `${name} (${email})` : email;
    opts.push({ value: r.id, label });
  }
  return opts;
}

// tenantOptions maps the GET /v1/admin/tenants list (super_admin-only —
// fetched ONLY when crossTenant=true) to <select> options. Label = the
// tenant name, value = the tenant id (AC-2).
export function tenantOptions(rows: TenantRow[]): AssignTenantOption[] {
  return rows.map((t) => ({ value: t.id, label: t.name }));
}

// TenantFieldMode is the discriminated result of tenantFieldMode:
//
//   - "chooser":    render the populated tenant <select> (super_admin).
//   - "pinned":     render a read-only display pinned to `tenantId`
//                   (tenant-admin — P0-527-1 / P0-479-2: NO cross-tenant
//                   chooser).
//   - "sole":       #1764 — the install has one tenant. Render NO tenant
//                   field; the assignment lands in `tenantId`.
//   - "unresolved": the tenant has not resolved yet; the field shows a
//                   placeholder and submit is disabled.
export type TenantFieldMode =
  | { kind: "chooser" }
  | { kind: "pinned"; tenantId: string }
  | { kind: "sole"; tenantId: string }
  | { kind: "unresolved" };

// tenantFieldMode decides whether the assign dialog's tenant field is a
// cross-tenant chooser, pinned to the actor's own tenant, or absent
// because the install has a single tenant. It uses ONLY the existing
// `cross_tenant` signal (derived from the user-list response shape — NO
// second authority probe, P0-527-2), the already-fetched session tenant
// id, and (#1764) the install's tenant-count signal. On a single-tenant
// install the session tenant IS the sole tenant; `tenantIds` (the
// super-admin tenant list, when loaded) is the fallback.
export function tenantFieldMode(args: {
  crossTenant: boolean;
  sessionTenantId: string | undefined;
  singleTenant?: boolean;
  tenantIds?: string[];
}): TenantFieldMode {
  const t = (args.sessionTenantId ?? "").trim();
  if (args.singleTenant) {
    const sole =
      t !== "" ? t : args.tenantIds?.length === 1 ? args.tenantIds[0] : "";
    return sole === ""
      ? { kind: "unresolved" }
      : { kind: "sole", tenantId: sole };
  }
  if (args.crossTenant) {
    return { kind: "chooser" };
  }
  if (t === "") {
    return { kind: "unresolved" };
  }
  return { kind: "pinned", tenantId: t };
}
