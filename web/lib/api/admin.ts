// Slice 370 — admin surfaces, extracted from the former `web/lib/api.ts`
// god-file. Co-locates the credentials (slice 034/060), feature flags
// (slice 059/060), super-admins (slice 142), tenants (slice 143), demo
// seed (slice 278), and SSO config (slice 063) clients — all bind to the
// `/v1/admin/*` surface and the admin section consumes them together
// (decision D1: the admin domain IS one domain).

import { apiBaseURL, APIError } from "./base";
import { apiFetch } from "./_shared";

// ===== Slice 060 — Admin settings (API keys + features) =====
//
// The admin section binds to three already-shipped backend surfaces:
//   * /v1/admin/credentials (slice 034 — issue / list / rotate / revoke)
//   * /v1/admin/features    (slice 059 — list + per-key PATCH)
//   * cred.IsAdmin          (slice 034 — boolean on the calling credential)
//
// SSO config CRUD, the user/roles list, and a unified audit-log read
// model are NOT on main as of slice 060; those page surfaces ship as
// empty-state placeholders that name the missing endpoint and the
// follow-up slice. See `Plans/canvas/10-roadmap.md` and the slice 060 PR
// description for the gap inventory.

export type AdminCredential = {
  id: string;
  tenant_id: string;
  scope_predicate: string;
  allowed_kinds: string[];
  last4: string;
  issued_at: string;
  last_used_at?: string | null;
  is_admin: boolean;
  is_approver: boolean;
  owner_roles: string[];
  rotated_from?: string | null;
};

export type AdminCredentialListResponse = { items: AdminCredential[] };

export type AdminCredentialIssueRequest = {
  scope_predicate: string;
  allowed_kinds: string[];
  ttl_seconds: number;
  is_admin: boolean;
  is_approver: boolean;
  owner_roles: string[];
};

export type AdminCredentialIssueResponse = {
  id: string;
  tenant_id: string;
  bearer_token: string;
  last4: string;
  issued_at: string;
  expires_at?: string;
};

export type AdminCredentialRotateResponse = {
  id: string;
  bearer_token: string;
  last4: string;
  predecessor_expires_at: string;
};

export async function listAdminCredentials(
  bearer: string,
): Promise<AdminCredential[]> {
  const res = await apiFetch(`/v1/admin/credentials`, bearer);
  const body = (await res.json()) as AdminCredentialListResponse;
  return body.items;
}

export async function issueAdminCredential(
  bearer: string,
  body: AdminCredentialIssueRequest,
): Promise<AdminCredentialIssueResponse> {
  const res = await fetch(`${apiBaseURL()}/v1/admin/credentials`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as AdminCredentialIssueResponse;
}

export async function rotateAdminCredential(
  bearer: string,
  id: string,
): Promise<AdminCredentialRotateResponse> {
  const res = await fetch(
    `${apiBaseURL()}/v1/admin/credentials/${encodeURIComponent(id)}/rotate`,
    {
      method: "POST",
      headers: { Authorization: `Bearer ${bearer}` },
    },
  );
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as AdminCredentialRotateResponse;
}

export async function revokeAdminCredential(
  bearer: string,
  id: string,
): Promise<void> {
  const res = await fetch(
    `${apiBaseURL()}/v1/admin/credentials/${encodeURIComponent(id)}/revoke`,
    {
      method: "POST",
      headers: { Authorization: `Bearer ${bearer}` },
    },
  );
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
}

export type FeatureFlag = {
  key: string;
  enabled: boolean;
  description: string;
  category: string;
  last_changed_by?: string;
  last_changed_at?: string | null;
  has_override: boolean;
};

export type FeatureFlagListResponse = { items: FeatureFlag[] };

export type FeatureFlagPatchResponse = {
  key: string;
  enabled: boolean;
  has_override: boolean;
};

export async function listFeatureFlags(bearer: string): Promise<FeatureFlag[]> {
  const res = await apiFetch(`/v1/admin/features`, bearer);
  const body = (await res.json()) as FeatureFlagListResponse;
  return body.items;
}

export async function patchFeatureFlag(
  bearer: string,
  key: string,
  body: { enabled: boolean; reason?: string },
): Promise<FeatureFlagPatchResponse> {
  const res = await fetch(
    `${apiBaseURL()}/v1/admin/features/${encodeURIComponent(key)}`,
    {
      method: "PATCH",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
    },
  );
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as FeatureFlagPatchResponse;
}

// ===== Slice 142 — super_admin management surface =====
//
// Wire shape mirrors `internal/api/adminsuperadmins/handler.go`:
//   GET    /v1/admin/super-admins              -> list of super_admin rows
//   POST   /v1/admin/super-admins              -> grant a super_admin
//   DELETE /v1/admin/super-admins/{user_id}    -> demote a super_admin (204)
//
// The handler is super_admin-gated; non-super_admin callers receive
// 403 from the upstream which the BFF passes through.

export type SuperAdminRow = {
  user_id: string;
  granted_at: string;
  granted_via: "bootstrap_first_install" | "manual_grant";
  display_name?: string | null;
  email?: string | null;
};

export type SuperAdminListResponse = { items: SuperAdminRow[] };

export async function listSuperAdmins(
  bearer: string,
): Promise<SuperAdminRow[]> {
  const res = await apiFetch(`/v1/admin/super-admins`, bearer);
  const body = (await res.json()) as SuperAdminListResponse;
  return body.items ?? [];
}

export async function grantSuperAdmin(
  bearer: string,
  userID: string,
): Promise<SuperAdminRow> {
  const res = await fetch(`${apiBaseURL()}/v1/admin/super-admins`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ user_id: userID }),
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) msg = body.error;
    } catch {
      // fall through with the status-line message
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as SuperAdminRow;
}

export async function demoteSuperAdmin(
  bearer: string,
  userID: string,
): Promise<void> {
  const res = await fetch(
    `${apiBaseURL()}/v1/admin/super-admins/${encodeURIComponent(userID)}`,
    {
      method: "DELETE",
      headers: { Authorization: `Bearer ${bearer}` },
    },
  );
  if (!res.ok && res.status !== 204) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) msg = body.error;
    } catch {
      // fall through with the status-line message
    }
    throw new APIError(res.status, msg);
  }
}

// ===== Slice 143 — Admin tenants (create-tenant flow, super_admin-gated) =====
//
// Wire shape mirrors `internal/api/admintenants/handler.go`:
//   GET   /v1/admin/tenants   -> { items: TenantRow[] }
//   POST  /v1/admin/tenants   -> { tenant: TenantRow, creator_admin_user_id?: string }
//
// Both routes are super_admin-gated server-side. The frontend renders
// the list + a "Create tenant" form (name + slug + "Join as admin"
// checkbox) on /admin/tenants.

export type TenantRow = {
  id: string;
  name: string;
  slug?: string | null;
  is_bootstrap_tenant: boolean;
  created_at: string;
  created_by_user_id?: string | null;
};

export type TenantListResponse = { items: TenantRow[] };

export type CreateTenantRequest = {
  name: string;
  slug: string;
  creator_joins_as?: "admin" | "none";
};

export type CreateTenantResponse = {
  tenant: TenantRow;
  creator_admin_user_id?: string | null;
};

export async function listAdminTenants(bearer: string): Promise<TenantRow[]> {
  const res = await apiFetch(`/v1/admin/tenants`, bearer);
  const body = (await res.json()) as TenantListResponse;
  return body.items ?? [];
}

export async function createAdminTenant(
  bearer: string,
  req: CreateTenantRequest,
): Promise<CreateTenantResponse> {
  const res = await fetch(`${apiBaseURL()}/v1/admin/tenants`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(req),
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) msg = body.error;
    } catch {
      // fall through with the status-line message
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as CreateTenantResponse;
}

// ===== Slice 479 — Admin user-management surface (binds to slice 478) =====
//
// Wire shape mirrors `internal/api/adminusers/{handler,assign}.go`:
//
//   GET  /v1/admin/users          -> super_admin: CrossTenantListResponse
//                                    (items carry tenant_id); tenant-admin:
//                                    the slice-062 ListResponse (no tenant_id).
//   POST /v1/admin/users/assign   -> {user_id?, tenant_id, roles[], self_assign?}
//                                    -> AssignResponse
//   POST /v1/admin/users/revoke   -> {user_id, tenant_id, remove_membership?}
//                                    -> 204 No Content
//
// Authority is enforced SERVER-SIDE (slice 478 D3): cross-tenant writes /
// the cross-tenant list require the super_admin JWT claim; within-tenant
// is allowed for a tenant-admin. The BFF forwards the bearer and passes
// the upstream status + error through verbatim (P0-479-1: the UI does NOT
// enforce authz; it surfaces the server's decisions honestly).

// AdminUserRow is the UNION of the within-tenant and cross-tenant list
// rows. `tenant_id` is present ONLY on the super_admin cross-tenant shape;
// its presence is how the UI detects super_admin scope (P0-479-2: a
// tenant-admin never receives cross-tenant rows, so never sees the
// cross-tenant controls).
export type AdminUserRow = {
  id: string;
  email: string;
  display_name: string;
  status: string;
  roles: string[];
  // Cross-tenant (super_admin) only:
  tenant_id?: string;
  idp_issuer?: string;
  idp_subject?: string;
};

// AdminUserListResult carries the rows plus the derived super_admin scope
// flag and the next-page cursor.
export type AdminUserListResult = {
  items: AdminUserRow[];
  next_cursor?: string;
  // crossTenant is true when the upstream returned the super_admin
  // cross-tenant shape (every item carried a tenant_id). Derived, not a
  // wire field — see deriveCrossTenant.
  cross_tenant: boolean;
};

export type AssignUserRequest = {
  user_id?: string;
  tenant_id: string;
  roles: string[];
  self_assign?: boolean;
};

export type AssignUserResponse = {
  user_id: string;
  tenant_id: string;
  roles: string[];
  idp_issuer: string;
  idp_subject: string;
  membership_created: boolean;
};

export type RevokeUserRequest = {
  user_id: string;
  tenant_id: string;
  remove_membership?: boolean;
};

// deriveCrossTenant reports whether the upstream list response is the
// super_admin cross-tenant shape. The cross-tenant shape tags every row
// with a non-empty tenant_id; the within-tenant shape omits it. An empty
// list is treated as within-tenant (the safe default — no cross-tenant
// controls), which is correct: an empty cross-tenant list means there is
// nothing to act on cross-tenant anyway. Exported for unit testing.
export function deriveCrossTenant(items: AdminUserRow[]): boolean {
  return (
    items.length > 0 &&
    items.every((it) => typeof it.tenant_id === "string" && it.tenant_id !== "")
  );
}

export async function listAdminUsers(
  bearer: string,
  opts: { cursor?: string; limit?: number } = {},
): Promise<AdminUserListResult> {
  const qs = new URLSearchParams();
  if (opts.cursor) qs.set("cursor", opts.cursor);
  if (opts.limit) qs.set("limit", String(opts.limit));
  const suffix = qs.toString() ? `?${qs.toString()}` : "";
  const res = await apiFetch(`/v1/admin/users${suffix}`, bearer);
  const body = (await res.json()) as {
    items?: AdminUserRow[];
    next_cursor?: string;
  };
  const items = body.items ?? [];
  return {
    items,
    next_cursor: body.next_cursor,
    cross_tenant: deriveCrossTenant(items),
  };
}

export async function assignAdminUser(
  bearer: string,
  req: AssignUserRequest,
): Promise<AssignUserResponse> {
  const res = await fetch(`${apiBaseURL()}/v1/admin/users/assign`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(req),
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) msg = body.error;
    } catch {
      // fall through with the status-line message
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as AssignUserResponse;
}

export async function revokeAdminUser(
  bearer: string,
  req: RevokeUserRequest,
): Promise<void> {
  const res = await fetch(`${apiBaseURL()}/v1/admin/users/revoke`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(req),
  });
  if (!res.ok && res.status !== 204) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) msg = body.error;
    } catch {
      // fall through with the status-line message
    }
    throw new APIError(res.status, msg);
  }
}

// ===== Slice 278 — Admin demo-seed UI (binds to internal/api/admindemo) =====
//
// Wire shape mirrors `internal/api/admindemo/handler.go`:
//
//   GET    /v1/admin/demo/status     -> {enabled: boolean}
//   POST   /v1/admin/demo/seed       -> seed summary
//   POST   /v1/admin/demo/teardown   -> {tenant_slug, status: "deleted"}
//
// 503 from /seed or /teardown means the env-var gate is unset. The
// frontend distinguishes the "feature disabled" 503 (banner) from
// the "transient" 5xx (error toast) via the /status probe.

export type DemoStatusResponse = {
  enabled: boolean;
};

export type DemoSeedResponse = {
  tenant_id: string;
  tenant_slug: string;
  controls: number;
  risks: number;
  evidence: number;
  audit_periods: number;
  samples: number;
  idempotent: boolean;
};

export type DemoTeardownResponse = {
  tenant_slug: string;
  status: string;
};

export async function getAdminDemoStatus(
  bearer: string,
): Promise<DemoStatusResponse> {
  const res = await fetch(`${apiBaseURL()}/v1/admin/demo/status`, {
    headers: { Authorization: `Bearer ${bearer}` },
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) msg = body.error;
    } catch {
      // fall through
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as DemoStatusResponse;
}

export async function postAdminDemoSeed(
  bearer: string,
): Promise<DemoSeedResponse> {
  const res = await fetch(`${apiBaseURL()}/v1/admin/demo/seed`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) msg = body.error;
    } catch {
      // fall through
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as DemoSeedResponse;
}

export async function postAdminDemoTeardown(
  bearer: string,
): Promise<DemoTeardownResponse> {
  const res = await fetch(`${apiBaseURL()}/v1/admin/demo/teardown`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) msg = body.error;
    } catch {
      // fall through
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as DemoTeardownResponse;
}

// ===== Slice 063 — Admin SSO config (binds to slice 062 backend) =====
//
// Wire shape mirrors `internal/api/adminsso/handler.go`:
//   GET   /v1/admin/sso   -> AdminSSOConfig sans client_secret (404 = unset)
//   PATCH /v1/admin/sso   -> upsert; empty client_secret => leave existing
//
// client_secret is NEVER returned from GET. The UI keeps the input
// password-typed and treats an empty submit as "leave existing" per
// slice 062's handler contract (slice 034 AC-9 / write-once secret).

export type AdminSSOConfig = {
  id: string;
  name: string;
  issuer_url: string;
  client_id: string;
  redirect_url: string;
  allowed_email_domains: string[];
  created_at: string;
  updated_at: string;
};

export type AdminSSOPatchRequest = {
  issuer_url: string;
  client_id: string;
  client_secret?: string;
  redirect_url: string;
  allowed_email_domains: string[];
};

// getAdminSSO returns the tenant's primary IdP config, or null if none
// is set yet (upstream 404). Throws on any other non-2xx.
export async function getAdminSSO(
  bearer: string,
): Promise<AdminSSOConfig | null> {
  const res = await fetch(`${apiBaseURL()}/v1/admin/sso`, {
    headers: { Authorization: `Bearer ${bearer}` },
  });
  if (res.status === 404) return null;
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as AdminSSOConfig;
}

export async function patchAdminSSO(
  bearer: string,
  body: AdminSSOPatchRequest,
): Promise<AdminSSOConfig> {
  const res = await fetch(`${apiBaseURL()}/v1/admin/sso`, {
    method: "PATCH",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    // Bubble up the upstream JSON body's `error` field for UI display
    // (slice 062 always returns {error: string} on non-2xx).
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON; fall back to status line
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as AdminSSOConfig;
}
