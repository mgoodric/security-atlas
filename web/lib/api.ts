// API base URL helpers for the platform's HTTP endpoints. The bearer token
// lives in a cookie that the platform reads server-side; client-side
// fetches send the cookie via credentials: "include".
//
// Server-side (BFF route handlers, RSC fetches) and client-side (browser)
// run on different network paths and need different base URLs:
//
//   - SERVER: a Next.js API route handler in the `web` container reaches
//     atlas over the internal Docker network. Default `http://atlas:8080`
//     (the compose service name); override with `ATLAS_HTTP_URL`.
//
//   - CLIENT: the browser reaches atlas through whatever public URL fronts
//     the deployment. Default empty string = same-origin relative URLs,
//     which works for any reverse proxy that routes /v1, /health, and
//     /api under the same hostname as the web frontend (e.g. NPM with
//     custom locations). Override at build time with
//     `NEXT_PUBLIC_API_BASE_URL` when the API lives on a different origin.
//
// The published `web` image is therefore deployment-agnostic — the
// compose sets `ATLAS_HTTP_URL` per environment, and the browser uses
// same-origin URLs through the reverse proxy.

const SERVER_DEFAULT = "http://atlas:8080";
const CLIENT_DEFAULT = "";

export function apiBaseURL(): string {
  if (typeof window === "undefined") {
    return (
      process.env.ATLAS_HTTP_URL ||
      process.env.NEXT_PUBLIC_API_BASE_URL ||
      SERVER_DEFAULT
    );
  }
  return process.env.NEXT_PUBLIC_API_BASE_URL || CLIENT_DEFAULT;
}

export type Anchor = {
  id: string;
  scf_id: string;
  family: string;
  name: string;
  description: string;
};

export type FrameworkVersion = {
  id: string;
  framework: string;
  version: string;
};

export type Requirement = {
  id: string;
  framework_version_id: string;
  code: string;
  text: string;
};

export type RequirementWithMapping = {
  requirement: Requirement;
  framework_version: FrameworkVersion;
  strm_type: "equal" | "subset_of" | "intersects";
  strength: number;
};

export type AnchorDetail = {
  anchor: Anchor;
  requirements: RequirementWithMapping[];
};

export class APIError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function apiFetch(path: string, bearer: string): Promise<Response> {
  const res = await fetch(`${apiBaseURL()}${path}`, {
    headers: {
      Authorization: `Bearer ${bearer}`,
    },
  });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return res;
}

export async function listAnchors(bearer: string): Promise<Anchor[]> {
  const res = await apiFetch("/v1/anchors", bearer);
  const body = (await res.json()) as { anchors: Anchor[] };
  return body.anchors;
}

// ----- Slice 104: anchors with optional joined state -----
//
// `AnchorState` mirrors `anchorStateCellWire` in
// internal/api/anchors/handlers.go — the slice-104 backend extension
// returns one rollup cell per anchor when `?include=state` is set.
// The shape is INTENTIONALLY a subset of the slice-012 `stateWire` —
// only the columns slice 098's design doc pins to the /controls table
// (result, freshness_status, last_observed_at) plus `evaluated_at` for
// staleness display.
export type AnchorState = {
  result: string;
  freshness_status: string;
  last_observed_at: string | null;
  evaluated_at: string;
};

// Slice 226 — `?include=state` now also carries `frameworks: string[]`
// (display abbreviations like `SOC2 · ISO · CSF`). The wire ships
// display values, not slugs; the abbreviation authority lives in the
// backend (`internal/catalog/framework_codes.go`), and the frontend is
// a pure renderer (P0-226-2). Empty array means the anchor has no
// satisfaction edges yet; the page renders `—` in that case (AC-6).
export type AnchorWithState = Anchor & {
  state: AnchorState | null;
  frameworks: string[];
};

// Slice 224 — accepts an optional `scopeCellID` that, when set, is
// forwarded to the upstream as `?scope=<cell_id>` so the worst_per_anchor
// rollup narrows to evaluations recorded against that scope cell.
// Server-side filtering only (P0-224-2): the applicability_expr never
// reaches the browser.
export async function listAnchorsWithState(
  bearer: string,
  scopeCellID?: string,
): Promise<AnchorWithState[]> {
  const qs = new URLSearchParams({ include: "state" });
  if (scopeCellID) qs.set("scope", scopeCellID);
  const res = await apiFetch(`/v1/anchors?${qs.toString()}`, bearer);
  const body = (await res.json()) as { anchors: AnchorWithState[] };
  return body.anchors;
}

export async function getAnchorRequirements(
  bearer: string,
  id: string,
): Promise<AnchorDetail> {
  const res = await apiFetch(
    `/v1/anchors/${encodeURIComponent(id)}/requirements`,
    bearer,
  );
  return (await res.json()) as AnchorDetail;
}

// ----- Slice 098 + 104: /controls list view (browser-side BFF call) -----
//
// The page at `web/app/(authed)/controls/page.tsx` calls this from the
// browser; the BFF handler at `web/app/api/controls/route.ts` is the
// server-side counterpart that injects the bearer cookie.
//
// Slice 098 shipped this view bound to the catalog (anchorWire only,
// state columns rendered as `—`). Slice 104 lifts the BFF to the
// joined `?include=state` shape — every row now carries either a real
// state cell or `state: null` (anchor has no tenant control). The
// frontend table renders the populated rows and the `—` placeholder
// stays for the null branch (slice 098 P0-A1 — no fabrication).

export type ControlsListResponse = {
  anchors: AnchorWithState[];
};

// Slice 224 — accepts an optional `scopeCellID` that the page forwards
// to the BFF as `?scope=<cell_id>`. Empty / undefined value omits the
// query param entirely so the BFF takes its no-filter branch.
export async function fetchControlsList(
  scopeCellID?: string,
): Promise<ControlsListResponse> {
  const qs = new URLSearchParams();
  if (scopeCellID) qs.set("scope", scopeCellID);
  const url = qs.toString()
    ? `/api/controls?${qs.toString()}`
    : `/api/controls`;
  const res = await fetch(url);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON — keep the status line
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as ControlsListResponse;
}

// ----- Slice 224: tenant scope cells (for the /controls Scope filter pill) -----
//
// `ScopeCell` mirrors `cellWire` in internal/api/scopes/handlers.go.
// The /controls page consumes this via the BFF route at
// `/api/scope-cells` to populate the Scope filter pill's dropdown
// options. Filtering by scope cell is a server-side concern
// (P0-224-2 — applicability_expr never reaches the browser); these
// types only carry the cell id + display label.

export type ScopeCell = {
  id: string;
  label: string;
  dimensions: Record<string, string>;
};

export type ScopeCellsListResponse = {
  cells: ScopeCell[];
};

// Server-side fn: hit the platform directly with the bearer. Used by the
// /api/scope-cells BFF route handler that runs server-side. Mirrors the
// listAnchorsWithState shape (no client-direct call from the browser —
// we never expose the platform's bearer to the page).
export async function listScopeCells(bearer: string): Promise<ScopeCell[]> {
  const res = await apiFetch("/v1/scopes/cells", bearer);
  const body = (await res.json()) as { cells: ScopeCell[] };
  return body.cells ?? [];
}

// Browser-side fn: the page calls this via TanStack Query. Hits the
// BFF, which injects the bearer cookie.
export async function fetchScopeCells(): Promise<ScopeCellsListResponse> {
  const res = await fetch("/api/scope-cells");
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON — keep the status line
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as ScopeCellsListResponse;
}

// ----- Slice 151: tenant control list (for risk-create form picker) -----
//
// `TenantControl` mirrors `activeControlWire` in
// internal/api/controls/list.go. The slice-151 risk-create form's
// control-link multi-select consumes this shape.
//
// IMPORTANT: distinct from the slice 098 `Anchor` type. `Anchor` is the
// SCF catalog row (global, no tenant); `TenantControl` is a row from
// the tenant `controls` table. The risk-control link FK requires a
// tenant control id, not an anchor id — see
// `migrations/sql/20260511000005_risk_register.sql`.
export type TenantControl = {
  id: string;
  title: string;
  control_family: string;
  scf_id: string;
  lifecycle_state: string;
  bundle_id: string;
};

export type TenantControlsListResponse = {
  controls: TenantControl[];
  count: number;
};

// Server-side fn: hit the platform directly with the bearer. Used by the
// `/api/controls-list` BFF route handler that runs server-side.
export async function fetchTenantControls(
  bearer: string,
): Promise<TenantControl[]> {
  const res = await apiFetch("/v1/controls", bearer);
  const body = (await res.json()) as TenantControlsListResponse;
  return body.controls ?? [];
}

// Browser-side fn: hits the `/api/controls-list` BFF route the form
// uses. Returns the controls array on success or throws APIError on a
// non-2xx upstream.
export async function fetchTenantControlsList(): Promise<TenantControl[]> {
  const res = await fetch(`/api/controls-list`);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON — keep the status line
    }
    throw new APIError(res.status, msg);
  }
  const body = (await res.json()) as TenantControlsListResponse;
  return body.controls ?? [];
}

// ----- Slice 024: vendor lite -----

export type VendorCriticality = "low" | "medium" | "high";
export type VendorReviewCadence =
  | "monthly"
  | "quarterly"
  | "biannual"
  | "annual";

export type Vendor = {
  id: string;
  name: string;
  domain?: string | null;
  criticality: VendorCriticality;
  contract_start?: string | null;
  contract_end?: string | null;
  dpa_signed: boolean;
  dpa_signed_at?: string | null;
  review_cadence: VendorReviewCadence;
  last_review_date?: string | null;
  overdue: boolean;
  owner_user: string;
  linked_sow_uri?: string | null;
  notes: string;
  scope_cell_ids: string[];
  created_at: string;
  updated_at: string;
};

export type VendorWrite = {
  name: string;
  domain?: string | null;
  criticality: VendorCriticality;
  contract_start?: string | null;
  contract_end?: string | null;
  dpa_signed: boolean;
  dpa_signed_at?: string | null;
  review_cadence: VendorReviewCadence;
  last_review_date?: string | null;
  owner_user: string;
  linked_sow_uri?: string | null;
  notes: string;
  scope_cell_ids: string[];
};

export type VendorBurndownBand = {
  criticality: string;
  total: number;
  overdue: number;
  on_time_fraction: number;
};

export type VendorBurndown = {
  as_of: string;
  bands: VendorBurndownBand[];
  total: VendorBurndownBand;
};

export type VendorListFilter = {
  criticality?: VendorCriticality;
  overdue?: boolean;
  as_of?: string;
};

function vendorQuery(filter?: VendorListFilter): string {
  if (!filter) return "";
  const qs = new URLSearchParams();
  if (filter.criticality) qs.set("criticality", filter.criticality);
  if (filter.overdue) qs.set("overdue", "true");
  if (filter.as_of) qs.set("as_of", filter.as_of);
  const s = qs.toString();
  return s ? `?${s}` : "";
}

export async function listVendors(
  bearer: string,
  filter?: VendorListFilter,
): Promise<Vendor[]> {
  const res = await apiFetch(`/v1/vendors${vendorQuery(filter)}`, bearer);
  const body = (await res.json()) as { vendors: Vendor[] };
  return body.vendors;
}

export async function getVendor(bearer: string, id: string): Promise<Vendor> {
  const res = await apiFetch(`/v1/vendors/${encodeURIComponent(id)}`, bearer);
  const body = (await res.json()) as { vendor: Vendor };
  return body.vendor;
}

export async function createVendor(
  bearer: string,
  body: VendorWrite,
): Promise<Vendor> {
  const res = await fetch(`${apiBaseURL()}/v1/vendors`, {
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
  const decoded = (await res.json()) as { vendor: Vendor };
  return decoded.vendor;
}

export async function updateVendor(
  bearer: string,
  id: string,
  body: VendorWrite,
): Promise<Vendor> {
  const res = await fetch(
    `${apiBaseURL()}/v1/vendors/${encodeURIComponent(id)}`,
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
  const decoded = (await res.json()) as { vendor: Vendor };
  return decoded.vendor;
}

export async function getVendorBurndown(
  bearer: string,
  filter?: VendorListFilter,
): Promise<VendorBurndown> {
  const res = await apiFetch(
    `/v1/vendors/burndown${vendorQuery(filter)}`,
    bearer,
  );
  return (await res.json()) as VendorBurndown;
}

// ===== Slice 018 — FrameworkScope =====

export type FrameworkScopeState =
  | "draft"
  | "review"
  | "approved"
  | "activated"
  | "superseded";

export type FrameworkScope = {
  id: string;
  framework_version_id: string;
  name: string;
  state: FrameworkScopeState;
  predicate: unknown;
  predicate_hash: string;
  approver_user_id?: string;
  approved_at?: string;
  predicate_hash_at_approval?: string;
  approval_evidence_file_url?: string;
  approval_evidence_file_hash?: string;
  effective_from?: string;
  superseded_by?: string;
  superseded_at?: string;
  created_at: string;
  updated_at: string;
};

export type FrameworkScopeCreate = {
  framework_version_id: string;
  name: string;
  predicate: unknown;
};

export type FrameworkScopePatchResponse = {
  framework_scope: FrameworkScope;
  approval_invalidated: boolean;
};

export async function listFrameworkScopes(
  bearer: string,
  filter: {
    framework_version?: string;
    state?: FrameworkScopeState;
    as_of?: string;
  },
): Promise<FrameworkScope[]> {
  const qs = new URLSearchParams();
  if (filter.framework_version)
    qs.set("framework_version", filter.framework_version);
  if (filter.state) qs.set("state", filter.state);
  if (filter.as_of) qs.set("as_of", filter.as_of);
  const suffix = qs.toString() ? `?${qs.toString()}` : "";
  const res = await apiFetch(`/v1/framework-scopes${suffix}`, bearer);
  const body = (await res.json()) as { framework_scopes: FrameworkScope[] };
  return body.framework_scopes;
}

export async function createFrameworkScope(
  bearer: string,
  body: FrameworkScopeCreate,
): Promise<FrameworkScope> {
  const res = await fetch(`${apiBaseURL()}/v1/framework-scopes`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok)
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  const decoded = (await res.json()) as { framework_scope: FrameworkScope };
  return decoded.framework_scope;
}

export async function patchFrameworkScopePredicate(
  bearer: string,
  id: string,
  predicate: unknown,
): Promise<FrameworkScopePatchResponse> {
  const res = await fetch(
    `${apiBaseURL()}/v1/framework-scopes/${encodeURIComponent(id)}`,
    {
      method: "PATCH",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ predicate }),
    },
  );
  if (!res.ok)
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  return (await res.json()) as FrameworkScopePatchResponse;
}

export async function transitionFrameworkScope(
  bearer: string,
  id: string,
  transition: "submit" | "approve" | "activate",
  body?: Record<string, unknown>,
): Promise<FrameworkScope> {
  const res = await fetch(
    `${apiBaseURL()}/v1/framework-scopes/${encodeURIComponent(
      id,
    )}/${transition}`,
    {
      method: "PATCH",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": "application/json",
      },
      body: body ? JSON.stringify(body) : undefined,
    },
  );
  if (!res.ok)
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  const decoded = (await res.json()) as { framework_scope: FrameworkScope };
  return decoded.framework_scope;
}

// ===== Slice 011 — Manual control attestation =====

export type AttestForm = {
  control_id: string;
  bundle_id: string;
  title: string;
  implementation_type: "manual_attested" | "manual_periodic";
  owner_role: string;
  freshness_class?: string | null;
  manual_evidence_schema: Record<string, unknown> | null;
  caller_can_attest: boolean;
  platform_schema_kind: string;
  platform_schema_version: string;
  platform_schema_requires: string[];
};

export type AttestSubmitRequest = {
  statement: string;
  attestation_data?: Record<string, unknown>;
  supporting_uri?: string;
  artifact_id?: string;
  idempotency_key?: string;
  observed_at?: string;
};

export type AttestSubmitResponse = {
  record_id: string;
  hash: string;
  ingested_at: string;
  credential_id: string;
  deduplicated: boolean;
  payload_uri?: string;
};

export async function getAttestForm(
  bearer: string,
  controlID: string,
): Promise<AttestForm> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/attest-form`,
    bearer,
  );
  return (await res.json()) as AttestForm;
}

export async function submitAttestation(
  bearer: string,
  controlID: string,
  body: AttestSubmitRequest,
): Promise<AttestSubmitResponse> {
  const res = await fetch(
    `${apiBaseURL()}/v1/controls/${encodeURIComponent(controlID)}/attestations`,
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
    },
  );
  if (!res.ok)
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  return (await res.json()) as AttestSubmitResponse;
}

export type ArtifactUploadResponse = {
  artifact: {
    id: string;
    payload_uri: string;
    size_bytes: number;
    content_type: string;
  };
};

// uploadArtifact pushes a binary blob to slice-036 via the platform's
// multipart endpoint and returns the artifact id, which the caller cites
// in the attestation body via `artifact_id`.
export async function uploadArtifact(
  bearer: string,
  file: File,
): Promise<ArtifactUploadResponse> {
  const form = new FormData();
  form.append("file", file);
  const res = await fetch(`${apiBaseURL()}/v1/artifacts:upload`, {
    method: "POST",
    headers: { Authorization: `Bearer ${bearer}` },
    body: form,
  });
  if (!res.ok)
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  return (await res.json()) as ArtifactUploadResponse;
}

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

// ===== Slice 041 — Control detail view =====
//
// Binds three already-merged backend slices into the /controls/[id] view:
//
//   * Slice 008 — UCF graph traversal
//       GET /v1/controls/{id}/coverage          control + anchor + requirements[]
//   * Slice 012 — control state evaluation
//       GET /v1/controls/{id}/state             per-scope-cell evaluated state
//       GET /v1/controls/{id}/effectiveness     rolling 30-day pass rate
//   * Slice 018 — FrameworkScope intersection
//       GET /v1/controls/{id}/effective-scope?framework_version=<UUID>
//
// The `relationship_type` (STRM) field is typed as an OPEN string, not a
// closed union. The DB enum has five values (equal, subset_of, superset_of,
// intersects_with, no_relationship — `internal/db/dbx/models.go`); the
// slice-005 `RequirementWithMapping.strm_type` 3-value union pre-dates the
// full enum and would silently drop `superset_of`. Rendering the raw string
// with a known-value style map + neutral fallback is drift-proof and honors
// the slice's anti-criterion against fabricated mappings.
//
// NOTE: there is no `GET /v1/evidence?control_id=...` list endpoint on main
// (only `POST /v1/evidence:push`). The evidence-stream section of the view
// renders an empty-state naming that gap; no evidence client fn exists here
// until that endpoint ships.

// controlWire mirrors `controlWire` in internal/api/ucfcoverage/handlers.go.
export type ControlWire = {
  id: string;
  bundle_id: string;
  version: number;
  scf_id?: string;
  scf_anchor_id?: string;
  title: string;
  control_family: string;
  implementation_type: string;
  lifecycle_state: string;
  owner_role: string;
  freshness_class?: string;
};

// anchorWire mirrors `anchorWire` in the same handler (bare anchor form).
export type ControlAnchorWire = {
  id: string;
  scf_id: string;
  family: string;
  name: string;
  description?: string;
};

// requirementForAnchorWire mirrors the same handler's per-requirement row.
// `relationship_type` is the STRM edge label — open string by design.
//
// Slice 256 — `coverage` is the per-row weighted score
// (strength × 30-day effectiveness, intersected with the framework's
// scope predicate). Always present on /v1/controls/{id}/coverage as a
// number-or-null (never undefined): null when the row's
// framework_version is out of scope OR when the control has no
// effectiveness data yet. The frontend MUST NOT compute coverage
// client-side as a fallback (slice 256 anti-criterion P0-256-1).
export type CoverageRequirement = {
  edge_id: string;
  requirement_id: string;
  code: string;
  title: string;
  body?: string;
  framework_slug: string;
  framework_name: string;
  framework_version: string;
  framework_version_id: string;
  framework_version_status: string;
  relationship_type: string;
  strength: number;
  coverage: number | null;
  source_attribution: string;
  rationale?: string;
};

export type ControlCoverage = {
  control: ControlWire;
  anchor: ControlAnchorWire | null;
  requirements: CoverageRequirement[];
};

// stateWire mirrors `stateWire` in internal/api/controlstate/handlers.go.
export type ControlStateEntry = {
  scope_cell_id: string | null;
  result: string;
  freshness_status: string;
  evidence_count_in_window: number;
  last_observed_at: string | null;
  evaluated_at: string;
  freshness_class: string;
  trigger: string;
};

export type ControlStateResponse = {
  control_id: string;
  states: ControlStateEntry[];
  count: number;
};

// effectivenessWire mirrors `effectivenessWire` in the controlstate handler.
export type ControlEffectiveness = {
  control_id: string;
  pass_rate: number;
  pass_count: number;
  total_count: number;
  window_start: string;
  window_end: string;
};

// EffectiveScope response from internal/api/frameworkscopes/handlers.go.
export type EffectiveScopeCell = {
  id: string;
  label: string;
  dimensions: Record<string, unknown>;
};

export type EffectiveScopeResponse = {
  control_id: string;
  framework_version_id: string;
  framework_scope_id: string | null;
  effective_scope: EffectiveScopeCell[];
  effective_scope_count: number;
  in_scope: boolean;
  out_of_scope_reason?: string;
};

// ===== Slice 253 — per-control policies / risks / history wire shapes =====
//
// Row sources are `policyWire`, `riskWire`, and `historyWire` in
// `internal/api/controldetail/handler.go` (slice 064). Endpoints have
// shipped on main since slice 064 + slice 106 (the evidence-list peer),
// but the control-detail view never re-pointed past slice 041's
// endpoint-pending placeholders — slice 253 wires the four reads.

// policyWire — one row of GET /v1/controls/{id}/policies.
export type ControlLinkedPolicy = {
  policy_id: string;
  title: string;
  version: string;
  status: string;
};

export type ControlLinkedPoliciesResponse = {
  control_id: string;
  policies: ControlLinkedPolicy[];
  count: number;
};

// riskWire — one row of GET /v1/controls/{id}/risks. The score fields
// stay opaque JSON blobs by design (canvas §2.2 — the 5x5 case carries
// `{likelihood, impact}` numerics; FAIR-shaped scores are valid too).
// The page extracts a display value via `formatResidualScore` from
// `app/(authed)/risks/filters.ts`.
export type ControlLinkedRisk = {
  risk_id: string;
  title: string;
  inherent_score: unknown;
  residual_score: unknown;
  link_weight: number | null;
};

export type ControlLinkedRisksResponse = {
  control_id: string;
  risks: ControlLinkedRisk[];
  count: number;
};

// historyWire — one row of GET /v1/controls/{id}/history. Newest-first,
// keyset-paginated; we render the most recent ~8 entries on the
// right-rail audit-log card (this view does NOT paginate — the dedicated
// History tab/page is the spillover for deeper trails).
export type ControlHistoryEntry = {
  evaluated_at: string;
  scope_cell: string | null;
  computed_state: string;
  freshness_status: string;
  evidence_count: number;
};

export type ControlHistoryResponse = {
  control_id: string;
  history: ControlHistoryEntry[];
  count: number;
  next_cursor: string;
};

// ----- server-side fns (called by the BFF route handlers) -----

export async function getControlCoverage(
  bearer: string,
  controlID: string,
): Promise<ControlCoverage> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/coverage`,
    bearer,
  );
  return (await res.json()) as ControlCoverage;
}

export async function getControlState(
  bearer: string,
  controlID: string,
): Promise<ControlStateResponse> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/state`,
    bearer,
  );
  return (await res.json()) as ControlStateResponse;
}

export async function getControlEffectiveness(
  bearer: string,
  controlID: string,
): Promise<ControlEffectiveness> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/effectiveness`,
    bearer,
  );
  return (await res.json()) as ControlEffectiveness;
}

export async function getControlEffectiveScope(
  bearer: string,
  controlID: string,
  frameworkVersionID: string,
): Promise<EffectiveScopeResponse> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/effective-scope` +
      `?framework_version=${encodeURIComponent(frameworkVersionID)}`,
    bearer,
  );
  return (await res.json()) as EffectiveScopeResponse;
}

// Slice 253 — per-control policies / risks / history (server-side).
//
// Each thin wrapper rides the same `apiFetch` helper used by the
// coverage / state / effectiveness / effective-scope reads above, which
// throws `APIError` on a non-2xx upstream response. The BFF route
// handlers in `app/api/controls/[id]/{policies,risks,history}/route.ts`
// catch the error and propagate the upstream status + message.

export async function getControlPolicies(
  bearer: string,
  controlID: string,
): Promise<ControlLinkedPoliciesResponse> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/policies`,
    bearer,
  );
  return (await res.json()) as ControlLinkedPoliciesResponse;
}

export async function getControlRisks(
  bearer: string,
  controlID: string,
): Promise<ControlLinkedRisksResponse> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/risks`,
    bearer,
  );
  return (await res.json()) as ControlLinkedRisksResponse;
}

export async function getControlHistory(
  bearer: string,
  controlID: string,
): Promise<ControlHistoryResponse> {
  // Right-rail audit-log card renders the latest few entries; the
  // upstream default page size is 50 and that's plenty for the
  // most-recent view. Deeper paginated history is the dedicated
  // History tab/page (slice 254 follow-on).
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/history`,
    bearer,
  );
  return (await res.json()) as ControlHistoryResponse;
}

// ----- browser-side fns (hit the BFF under /api/controls/**) -----

async function bffControlFetch<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON — keep the status line
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as T;
}

export function fetchControlCoverage(
  controlID: string,
): Promise<ControlCoverage> {
  return bffControlFetch<ControlCoverage>(
    `/api/controls/${encodeURIComponent(controlID)}/coverage`,
  );
}

export function fetchControlState(
  controlID: string,
): Promise<ControlStateResponse> {
  return bffControlFetch<ControlStateResponse>(
    `/api/controls/${encodeURIComponent(controlID)}/state`,
  );
}

export function fetchControlEffectiveness(
  controlID: string,
): Promise<ControlEffectiveness> {
  return bffControlFetch<ControlEffectiveness>(
    `/api/controls/${encodeURIComponent(controlID)}/effectiveness`,
  );
}

export function fetchControlEffectiveScope(
  controlID: string,
  frameworkVersionID: string,
): Promise<EffectiveScopeResponse> {
  return bffControlFetch<EffectiveScopeResponse>(
    `/api/controls/${encodeURIComponent(controlID)}/effective-scope` +
      `?framework_version=${encodeURIComponent(frameworkVersionID)}`,
  );
}

// Slice 253 — browser-side fetchers for the per-control policies /
// risks / history reads. Each rides the existing `bffControlFetch`
// helper so APIError surfaces with the upstream status; the
// control-detail page's `classifyControlDetailError` already routes
// 401 / 404 / other in a single discriminator and the new queries
// reuse it for free.

export function fetchControlPolicies(
  controlID: string,
): Promise<ControlLinkedPoliciesResponse> {
  return bffControlFetch<ControlLinkedPoliciesResponse>(
    `/api/controls/${encodeURIComponent(controlID)}/policies`,
  );
}

export function fetchControlRisks(
  controlID: string,
): Promise<ControlLinkedRisksResponse> {
  return bffControlFetch<ControlLinkedRisksResponse>(
    `/api/controls/${encodeURIComponent(controlID)}/risks`,
  );
}

export function fetchControlHistory(
  controlID: string,
): Promise<ControlHistoryResponse> {
  return bffControlFetch<ControlHistoryResponse>(
    `/api/controls/${encodeURIComponent(controlID)}/history`,
  );
}

// ===== Slice 040 — Program dashboard view (+ slice 147 re-point) =====
//
// The dashboard at `/dashboard` is the solo-security-leader persona's
// morning home screen. It binds six panels, each to a real backend
// endpoint via a thin BFF proxy under `/api/dashboard/**`. No panel
// fabricates data (anti-criterion P0-1).
//
// Endpoint inventory verified against main `internal/api/` (updated by
// slice 147 — slice 066 backend endpoints are now bound):
//
//   * Recent drift       GET /v1/controls/drift?since=7d    (slice 016) — bound
//   * Evidence freshness GET /v1/evidence/freshness         (slice 016) — bound
//   * Top risks aging    GET /v1/risks?treatment=mitigate   (slice 019) — bound
//       Server-side `sort=residual,age` (slice 066) is not yet passed —
//       spillover at slice 148.
//   * Upcoming items     GET /v1/exceptions/expiring?within=30d (slice 028) — bound
//       The unified rollup `/v1/upcoming` (slice 066) is not yet consumed —
//       spillover at slice 148.
//   * Framework posture  GET /v1/frameworks/posture          (slice 066) — bound by slice 147
//   * Activity feed      GET /v1/activity                    (slice 066) — bound by slice 147
//
// See `docs/audit-log/040-program-dashboard-view-decisions.md` and
// `docs/audit-log/147-dashboard-placeholders-decisions.md` for the full
// rebinding history.

// driftRowWire mirrors `driftRowWire` in internal/api/freshnessdrift/handlers.go.
export type DriftRow = {
  control_id: string;
  last_passing: string;
  current_result: string;
};

// DriftReport mirrors the Drift handler's JSON envelope.
export type DriftReport = {
  since: string;
  through: string;
  delta: number;
  flipped_out_count: number;
  flipped_out: DriftRow[];
};

// freshnessClassBucket mirrors `freshnessClassBucket` in the same handler.
export type FreshnessBucket = {
  freshness_class: string;
  total: number;
  fresh: number;
  stale: number;
};

// FreshnessReport mirrors the Freshness handler's JSON envelope.
export type FreshnessReport = {
  bucket: string;
  buckets: FreshnessBucket[];
  total: number;
  total_stale: number;
};

// DashboardRisk mirrors `riskWire` in internal/api/risks/handlers.go.
// `inherent_score` / `residual_score` are opaque JSON blobs by design —
// the dashboard renders them as-is and never parses an ordering out.
export type DashboardRisk = {
  id: string;
  title: string;
  description: string;
  category: string;
  methodology: string;
  inherent_score: unknown;
  treatment: string;
  treatment_owner: string;
  residual_score: unknown;
  review_due_at?: string;
  accepted_until?: string | null;
  accepter: string;
  instrument_reference: string;
  linked_control_ids: string[];
  created_at: string;
  updated_at: string;
};

export type RiskListResponse = { risks: DashboardRisk[]; count: number };

// ExpiringException mirrors `exceptionWire` in internal/api/exceptions/handlers.go.
export type ExpiringException = {
  id: string;
  control_id: string;
  justification: string;
  compensating_controls: string[];
  requested_by: string;
  requested_at: string;
  approved_by?: string;
  approved_at?: string;
  effective_from?: string;
  expires_at: string;
  expired_at?: string;
  status: string;
  created_at: string;
  updated_at: string;
};

export type ExpiringExceptionsResponse = {
  exceptions: ExpiringException[];
  count: number;
  within: string;
};

// FrameworkPostureRow mirrors `postureWire` in
// internal/api/dashboard/handler.go. One row per active framework version.
// `coverage_pct` and `freshness_composite` are 0-100; `trend_delta_90d`
// is signed (current coverage minus coverage 90 days ago, in points).
export type FrameworkPostureRow = {
  framework_id: string;
  framework_version: string;
  coverage_pct: number;
  freshness_composite: number;
  trend_delta_90d: number;
};

// FrameworkPostureReport mirrors the FrameworkPosture handler's envelope.
export type FrameworkPostureReport = {
  frameworks: FrameworkPostureRow[];
  count: number;
};

// UpcomingItem mirrors `upcomingWire` in
// internal/api/dashboard/handler.go. One row of the unified upcoming
// rollup — merges expiring exceptions, policy-ack expirations, vendor
// reviews, and audit-period milestones into one date-sorted feed.
// `category` is one of: exception / policy_ack / vendor_review /
// audit_period.
export type UpcomingItem = {
  due_date: string;
  category: string;
  title: string;
  resource_type: string;
  resource_id: string;
};

// UpcomingResponse mirrors the Upcoming handler's envelope.
// `next_cursor` is the opaque base64url keyset token for the next page,
// or "" when no next page exists.
export type UpcomingResponse = {
  upcoming: UpcomingItem[];
  count: number;
  next_cursor: string;
};

// ActivityEvent mirrors `activityWire` in internal/api/dashboard/handler.go.
// `summary` is forwarded as-is — the slice-062 admin_audit_log_v evidence
// branch packs an event-type-specific JSON blob whose shape is not
// uniformly renderable in the feed row; the panel cites the metadata
// triple (event_type / resource_type / resource_id) instead.
export type ActivityEvent = {
  ts: string;
  event_type: string;
  actor: string;
  resource_type: string;
  resource_id: string;
  summary: unknown;
};

// ActivityFeedResponse mirrors the Activity handler's envelope.
// `next_cursor` is the opaque base64url keyset token for the next page,
// or "" when no next page exists.
export type ActivityFeedResponse = {
  activity: ActivityEvent[];
  count: number;
  next_cursor: string;
};

// ----- server-side fns (called by the BFF route handlers) -----

export async function getControlDrift(
  bearer: string,
  since = "7d",
): Promise<DriftReport> {
  const res = await apiFetch(
    `/v1/controls/drift?since=${encodeURIComponent(since)}`,
    bearer,
  );
  return (await res.json()) as DriftReport;
}

export async function getEvidenceFreshness(
  bearer: string,
): Promise<FreshnessReport> {
  const res = await apiFetch(`/v1/evidence/freshness`, bearer);
  return (await res.json()) as FreshnessReport;
}

// getMitigateRisks fetches the dashboard top-risks panel feed —
// risks with treatment=mitigate, ranked by residual-score magnitude
// descending then risk age ascending. The `sort=residual,age` ordering
// is the slice-066 (AC-3) server-side capability that slice 157
// re-points the dashboard onto; before slice 157 the dashboard rendered
// the unsorted list with a labelled "ranking pending" footer.
export async function getMitigateRisks(
  bearer: string,
): Promise<DashboardRisk[]> {
  const res = await apiFetch(
    `/v1/risks?treatment=mitigate&sort=residual,age`,
    bearer,
  );
  const body = (await res.json()) as RiskListResponse;
  return body.risks;
}

export async function getExpiringExceptions(
  bearer: string,
  within = "30d",
): Promise<ExpiringExceptionsResponse> {
  const res = await apiFetch(
    `/v1/exceptions/expiring?within=${encodeURIComponent(within)}`,
    bearer,
  );
  return (await res.json()) as ExpiringExceptionsResponse;
}

export async function getFrameworkPosture(
  bearer: string,
): Promise<FrameworkPostureReport> {
  const res = await apiFetch(`/v1/frameworks/posture`, bearer);
  return (await res.json()) as FrameworkPostureReport;
}

export async function getActivity(
  bearer: string,
): Promise<ActivityFeedResponse> {
  const res = await apiFetch(`/v1/activity`, bearer);
  return (await res.json()) as ActivityFeedResponse;
}

// getUpcoming fetches the dashboard's unified upcoming-rollup feed —
// expiring exceptions, policy-ack expirations, vendor reviews, and
// audit-period milestones merged into one date-sorted (ascending)
// paginated feed. The slice-066 (AC-4) endpoint that slice 157
// re-points the dashboard onto; before slice 157 the dashboard rendered
// only the expiring-exceptions subset with a labelled "rollup pending"
// footer.
export async function getUpcoming(bearer: string): Promise<UpcomingResponse> {
  const res = await apiFetch(`/v1/upcoming`, bearer);
  return (await res.json()) as UpcomingResponse;
}

// ----- browser-side fns (hit the BFF under /api/dashboard/**) -----

export function fetchDashboardDrift(): Promise<DriftReport> {
  return bffControlFetch<DriftReport>(`/api/dashboard/drift`);
}

export function fetchDashboardFreshness(): Promise<FreshnessReport> {
  return bffControlFetch<FreshnessReport>(`/api/dashboard/freshness`);
}

export function fetchDashboardRisks(): Promise<DashboardRisk[]> {
  return bffControlFetch<RiskListResponse>(`/api/dashboard/risks`).then(
    (b) => b.risks,
  );
}

export function fetchDashboardUpcoming(): Promise<UpcomingResponse> {
  return bffControlFetch<UpcomingResponse>(`/api/dashboard/upcoming`);
}

export function fetchDashboardFrameworkPosture(): Promise<FrameworkPostureReport> {
  return bffControlFetch<FrameworkPostureReport>(
    `/api/dashboard/framework-posture`,
  );
}

export function fetchDashboardActivity(): Promise<ActivityFeedResponse> {
  return bffControlFetch<ActivityFeedResponse>(`/api/dashboard/activity`);
}

// ===== Slice 056 — Hierarchical risk dashboard view =====
//
// The `/risks/hierarchy` view binds three panels — org tree, theme
// heatmap, decision timeline — each to a real backend endpoint via a
// thin BFF proxy under `/api/risks-hierarchy/**`. It extends the slice
// 040 pattern (`dashboardProxy`, `PanelCard`/`MissingEndpointPanel`,
// per-panel TanStack Query). No panel fabricates data (anti-criterion
// P0-1).
//
// Endpoint inventory verified against main `internal/api/` at slice
// time:
//
//   * Org tree structure   GET /v1/org_units            (slice 053) — bound
//       The AC asks for `?include_risk_counts=true`. The ListOrgUnits
//       handler ignores all query params and the slice-019 `riskWire`
//       predates slice 052 — there is no `org_unit_id`, `themes`, or a
//       severity field on a risk-list row, so per-node risk counts
//       cannot be derived client-side either. The tree STRUCTURE binds
//       and renders honestly; per-node count chips show a labelled
//       "pending endpoint" affordance naming `?include_risk_counts=true`
//       rather than fabricating zeros. AC-2 is PARTIAL.
//   * Theme vocabulary     GET /v1/themes               (slice 053) — bound
//       Real heatmap columns (10 default + tenant-private). The
//       `themes × org_units` cell-aggregation endpoint does NOT exist on
//       main — the heatmap renders its real axes and overlays a
//       `MissingEndpointPanel` for cell counts. AC-3/4/5 PARTIAL.
//   * Aggregation rules    GET /v1/aggregation-rules    (slice 054) — bound
//       Real `window_days` / `min_risks` / `min_teams` / `target_theme`
//       metadata so the heatmap cell-hover tooltip ("nearest rule fires
//       at {threshold}; window {window_days}d") cites real numbers, not
//       fabricated thresholds.
//   * Decisions            GET /v1/decisions            (slice 055) — bound
//   * Overdue decisions    GET /v1/decisions/overdue    (slice 055) — bound
//       The decision timeline panel is FULLY satisfiable — slice 055 is
//       merged. AC-6 / AC-7 are PASS.
//
// See `docs/audit-log/056-hierarchical-risk-dashboard-decisions.md` for
// the full missing-endpoint gap inventory so a follow-up backend slice
// can be scoped.

// OrgUnit mirrors `wire` in internal/api/orgunits/handlers.go.
export type OrgUnit = {
  id: string;
  name: string;
  parent_id?: string | null;
  level: string;
  acceptance_authorities: unknown;
};

export type OrgUnitListResponse = { org_units: OrgUnit[]; count: number };

// RiskTheme mirrors `themeWire` in internal/api/themes/handlers.go.
export type RiskTheme = {
  name: string;
  description: string;
  source: "default" | "tenant";
};

export type RiskThemeListResponse = { themes: RiskTheme[]; count: number };

// AggregationRule mirrors `ruleWire` in
// internal/api/aggregationrules/handler.go. Only the fields the heatmap
// tooltip cites are typed strictly; the rest are carried as-is.
export type AggregationRule = {
  id: string;
  rule_id: string;
  target_theme: string;
  min_risks: number;
  min_teams: number;
  window_days: number;
  parent_level: string;
  severity_function: string;
  title_template: string;
  status: string;
  activated_by?: string;
  activated_at?: string;
  created_at: string;
  updated_at: string;
};

export type AggregationRuleListResponse = {
  rules: AggregationRule[];
  count: number;
};

// Decision mirrors `decisionWire` in internal/api/decisions/handlers.go.
export type Decision = {
  id: string;
  decision_id: string;
  title: string;
  narrative: string;
  constraints: string[];
  tradeoffs: string;
  decision_maker: string;
  decided_at: string;
  revisit_by?: string;
  status: string;
  superseded_by?: string;
  audit_narrative_opt_out: boolean;
  created_at: string;
  updated_at: string;
};

export type DecisionListResponse = { decisions: Decision[]; count: number };

// DecisionFilter is the client-side filter state for the timeline panel.
// `status` filters by a single status string upstream; `constraints` and
// `decision_maker` are applied client-side over the returned rows (the
// ListDecisions handler exposes only `?status=` and
// `?revisit_due_within_days=`).
export type DecisionFilter = {
  status?: string;
};

// ----- server-side fns (called by the BFF route handlers) -----

export async function getOrgUnits(bearer: string): Promise<OrgUnit[]> {
  const res = await apiFetch(`/v1/org_units`, bearer);
  const body = (await res.json()) as OrgUnitListResponse;
  return body.org_units;
}

export async function getRiskThemes(bearer: string): Promise<RiskTheme[]> {
  const res = await apiFetch(`/v1/themes`, bearer);
  const body = (await res.json()) as RiskThemeListResponse;
  return body.themes;
}

export async function getAggregationRules(
  bearer: string,
): Promise<AggregationRule[]> {
  const res = await apiFetch(`/v1/aggregation-rules`, bearer);
  const body = (await res.json()) as AggregationRuleListResponse;
  return body.rules ?? [];
}

export async function getDecisions(
  bearer: string,
  status?: string,
): Promise<Decision[]> {
  const suffix = status ? `?status=${encodeURIComponent(status)}` : "";
  const res = await apiFetch(`/v1/decisions${suffix}`, bearer);
  const body = (await res.json()) as DecisionListResponse;
  return body.decisions;
}

export async function getOverdueDecisions(bearer: string): Promise<Decision[]> {
  const res = await apiFetch(`/v1/decisions/overdue`, bearer);
  const body = (await res.json()) as DecisionListResponse;
  return body.decisions;
}

// ----- browser-side fns (hit the BFF under /api/risks-hierarchy/**) -----

export function fetchHierarchyOrgUnits(): Promise<OrgUnit[]> {
  return bffControlFetch<OrgUnitListResponse>(
    `/api/risks-hierarchy/org-units`,
  ).then((b) => b.org_units);
}

export function fetchHierarchyThemes(): Promise<RiskTheme[]> {
  return bffControlFetch<RiskThemeListResponse>(
    `/api/risks-hierarchy/themes`,
  ).then((b) => b.themes);
}

export function fetchHierarchyAggregationRules(): Promise<AggregationRule[]> {
  return bffControlFetch<AggregationRuleListResponse>(
    `/api/risks-hierarchy/aggregation-rules`,
  ).then((b) => b.rules ?? []);
}

export function fetchHierarchyDecisions(status?: string): Promise<Decision[]> {
  const suffix = status ? `?status=${encodeURIComponent(status)}` : "";
  return bffControlFetch<DecisionListResponse>(
    `/api/risks-hierarchy/decisions${suffix}`,
  ).then((b) => b.decisions);
}

export function fetchHierarchyOverdueDecisions(): Promise<Decision[]> {
  return bffControlFetch<DecisionListResponse>(
    `/api/risks-hierarchy/decisions-overdue`,
  ).then((b) => b.decisions);
}

// ----- Slice 032: quarterly board pack -----
//
// The quarterly board pack has a draft -> published lifecycle. The
// `content` is a structured map of fixed sections; each section carries a
// templated narrative, an optional operator override, an approval flag,
// and structured data. Publish is gated on every section being approved
// (decision D6). The wire shapes mirror internal/api/board/pack_handlers.go.

export type BoardPackSectionData = {
  // posture
  frameworks?: {
    slug: string;
    name: string;
    coverage_pct: number;
    freshness_pct: number;
    trend_arrow: string;
    delta: number;
    state: string;
  }[];
  // top_risks
  top_risks?: {
    id: string;
    title: string;
    category: string;
    treatment: string;
    residual_severity: number;
    age_days: number;
  }[];
  // coverage_trend
  coverage_pct?: number;
  baseline_coverage_pct?: number;
  coverage_delta?: number;
  // open_findings
  findings?: {
    evaluation_id: string;
    control_id: string;
    scope_cell_id: string;
    evaluated_at: string;
    freshness_status: string;
  }[];
  findings_count?: number;
  // operational_metrics (operator-entered)
  phishing_pass_rate_pct?: number | null;
  p1_patch_median_days?: number | null;
  incident_count?: number | null;
  vendor_reviews_on_time?: number | null;
  vendor_reviews_total?: number | null;
  // investment (operator-entered)
  spend_usd?: number;
  cost_per_coverage_point?: number;
};

export type BoardPackSection = {
  key: string;
  title: string;
  templated_text: string;
  override_text: string;
  approved: boolean;
  data: BoardPackSectionData;
};

export type BoardPackContent = {
  period_end: string;
  generated_at: string;
  status: string;
  sections: Record<string, BoardPackSection>;
};

export type BoardPack = {
  id: string;
  period_end: string;
  status: string;
  content: BoardPackContent;
  narrative_md: string;
  published_by?: string;
  published_at?: string;
  created_at: string;
  updated_at: string;
};

// The fixed, ordered section keys (decision D6) — the single source of
// truth in the UI for "what sections exist and in what order". Mirrors
// internal/board/pack.go SectionKeys.
//
// Slice 273 added `vendor_burndown` in slot §05 (between `open_findings`
// and `operational_metrics`). The mirror is the *only* FE change in this
// slice — no dedicated <VendorBurndown /> component ships here; the page
// renders the section's chrome (title, approve button, templated
// narrative) via the default fallback in SectionStructured, and the
// publish-gate math stays correct (totalSections === BOARD_PACK_SECTION_KEYS.length).
// A dedicated component lands in a follow-on FE slice. See
// docs/audit-log/273-decisions.md D6.
export const BOARD_PACK_SECTION_KEYS: string[] = [
  "posture",
  "top_risks",
  "coverage_trend",
  "open_findings",
  "vendor_burndown",
  "operational_metrics",
  "investment",
  "asks",
];

// Operator-entered structured inputs for the PUT section endpoint
// (decisions D3 + D5). All fields optional — only populated ones apply.
export type BoardPackSectionInputs = {
  phishing_pass_rate_pct?: number;
  p1_patch_median_days?: number;
  incident_count?: number;
  vendor_reviews_on_time?: number;
  vendor_reviews_total?: number;
  spend_usd?: number;
  baseline_coverage_pct?: number;
};

async function boardPackJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body.error) msg = body.error;
    } catch {
      // keep the status-line message
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as T;
}

export function listBoardPacks(): Promise<BoardPack[]> {
  return boardPackJSON<{ packs: BoardPack[] }>("/api/board-packs").then(
    (b) => b.packs ?? [],
  );
}

export function generateBoardPack(periodEnd: string): Promise<BoardPack> {
  return boardPackJSON<BoardPack>("/api/board-packs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ period_end: periodEnd }),
  });
}

export function getBoardPack(id: string): Promise<BoardPack> {
  return boardPackJSON<BoardPack>(`/api/board-packs/${encodeURIComponent(id)}`);
}

export function updateBoardPackSection(
  id: string,
  key: string,
  payload: { override_text?: string; inputs?: BoardPackSectionInputs },
): Promise<BoardPack> {
  return boardPackJSON<BoardPack>(
    `/api/board-packs/${encodeURIComponent(id)}/sections/${encodeURIComponent(
      key,
    )}`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    },
  );
}

export function approveBoardPackSection(
  id: string,
  key: string,
): Promise<BoardPack> {
  return boardPackJSON<BoardPack>(
    `/api/board-packs/${encodeURIComponent(id)}/sections/${encodeURIComponent(
      key,
    )}/approve`,
    { method: "POST" },
  );
}

export function publishBoardPack(
  id: string,
  publishedBy: string,
): Promise<BoardPack> {
  return boardPackJSON<BoardPack>(
    `/api/board-packs/${encodeURIComponent(id)}/publish`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ published_by: publishedBy }),
    },
  );
}

// ----- Slice 043: export URLs + approver-role probe -----
//
// boardPackMarkdownURL / boardPackPdfURL point at the slice-043 BFF
// passthrough routes, NOT the raw /v1/board-packs/...md and .../pdf
// endpoints (which require an Authorization header the browser cannot
// attach to a plain link). The BFF routes read the bearer cookie
// server-side and stream the binary bytes back.

export function boardPackMarkdownURL(id: string): string {
  return `/api/board-packs/${encodeURIComponent(id)}/markdown`;
}

export function boardPackPdfURL(id: string): string {
  return `/api/board-packs/${encodeURIComponent(id)}/pdf`;
}

// The approver gate (AC-3) — the UI hides approve + publish controls
// unless the current bearer is an admin credential. The platform always
// enforces its own publish gate (every section approved + published_by
// required); the UI gate is defense-in-depth + clearer affordance.
//
// Decision D3 of slice 043: there is no `is_board_approver` role on
// main; we reuse the slice-060 /api/admin/me probe (is_admin boolean).
// A finer role is a documented follow-up.

export type SessionMe = {
  is_admin: boolean;
};

export async function getSessionMe(): Promise<SessionMe> {
  const res = await fetch("/api/admin/me", { cache: "no-store" });
  if (res.status === 401) {
    return { is_admin: false };
  }
  if (!res.ok) {
    // Don't throw — degrade to "not approver" so the UI stays usable.
    return { is_admin: false };
  }
  const body = (await res.json()) as { is_admin?: boolean };
  return { is_admin: body.is_admin === true };
}

// ===== Slice 094 — Compliance calendar =====
//
// Read-only aggregation across audit_periods + exceptions + policies +
// controls (with cadence math). Plus a per-user ICS URL token mint.
// See docs/audit-log/094-compliance-calendar-decisions.md.

export type CalendarEventType = "audit" | "exception" | "policy" | "control";

export type CalendarEvent = {
  id: string;
  type: CalendarEventType;
  title: string;
  starts_at: string; // RFC 3339
  ends_at?: string;
  related_entity_id: string;
  related_entity_kind: string;
  summary: string;
  status: string;
  cadence?: string;
};

export type CalendarResponse = {
  events: CalendarEvent[];
  count: number;
  from: string;
  to: string;
  truncated: boolean;
  next_from?: string;
};

export type CalendarSubscriptionResponse = {
  url: string;
  expires_at: string;
};

// Server-side fn: hit the platform with the bearer.
export async function getCalendarEvents(
  bearer: string,
  params: { from?: string; to?: string; types?: string } = {},
): Promise<CalendarResponse> {
  const qp = new URLSearchParams();
  if (params.from) qp.set("from", params.from);
  if (params.to) qp.set("to", params.to);
  if (params.types) qp.set("types", params.types);
  const suffix = qp.toString() ? `?${qp.toString()}` : "";
  const res = await apiFetch(`/v1/calendar${suffix}`, bearer);
  return (await res.json()) as CalendarResponse;
}

export async function postCalendarSubscription(
  bearer: string,
): Promise<CalendarSubscriptionResponse> {
  const url = `${apiBaseURL()}/v1/calendar/subscription`;
  const res = await fetch(url, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
  });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as CalendarSubscriptionResponse;
}

// Browser-side fn: hit the BFF.
export function fetchCalendarEvents(params: {
  from?: string;
  to?: string;
  types?: string;
}): Promise<CalendarResponse> {
  const qp = new URLSearchParams();
  if (params.from) qp.set("from", params.from);
  if (params.to) qp.set("to", params.to);
  if (params.types) qp.set("types", params.types);
  const suffix = qp.toString() ? `?${qp.toString()}` : "";
  return bffControlFetch<CalendarResponse>(`/api/calendar${suffix}`);
}

export async function createCalendarSubscription(): Promise<CalendarSubscriptionResponse> {
  const res = await fetch(`/api/calendar/subscription`, { method: "POST" });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON */
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as CalendarSubscriptionResponse;
}

// ----- Slice 100: /risks list view -----
//
// The slice-019 `riskWire` shape with the slice-067 hierarchy + severity
// fields. `inherent_score` and `residual_score` stay opaque JSON blobs
// (canvas §2.2: 5x5 grid is the v1 shape, but tenants may switch to FAIR
// later). The page renders them through pure formatters that defensively
// extract `{likelihood, impact}` — a malformed score formats as "—",
// matching the platform's degrade-quietly posture (`store.go`
// `residualMagnitude`).

export type Risk = {
  id: string;
  title: string;
  description: string;
  category: string;
  methodology: string;
  inherent_score: unknown;
  treatment: string;
  treatment_owner: string;
  residual_score: unknown;
  review_due_at?: string;
  accepted_until?: string | null;
  accepter: string;
  instrument_reference: string;
  linked_control_ids: string[];
  created_at: string;
  updated_at: string;
  // Slice 067 additions — surfaced by the same handler.
  org_unit_id?: string;
  themes: string[];
  severity: number;
};

export type RisksListResponse = {
  risks: Risk[];
};

// Server-side fetcher used by the slice-100 BFF route. Mirrors the
// slice-098 `listAnchors` shape: no query-string narrowing — the page
// owns filter state client-side and the upstream `GET /v1/risks` ships
// the full unfiltered list.
export async function listRisks(bearer: string): Promise<Risk[]> {
  const res = await apiFetch("/v1/risks", bearer);
  const body = (await res.json()) as { risks: Risk[]; count: number };
  return body.risks;
}

// Browser-side fetcher used by the slice-100 page. Hits the BFF at
// `/api/risks` which forwards the bearer cookie to upstream.
export async function fetchRisksList(): Promise<RisksListResponse> {
  const res = await fetch(`/api/risks`);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON — keep the status line
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as RisksListResponse;
}

// ----- Slice 105: risk-create wire shape -----
//
// `RiskCreateInput` mirrors `createReq` in
// `internal/api/risks/handlers.go` exactly. The form binds directly to
// this shape — no invented fields per P0-A4. `inherent_score` /
// `residual_score` stay opaque JSON blobs by design (canvas §2.2 — 5x5
// today, FAIR/dollar-banded tomorrow). The slice-105 form constructs
// `{likelihood, impact}` for the 5x5 case because that is what
// `severityOf()` reads downstream.
//
// Optional fields (review_due_at, accepted_until, accepter,
// instrument_reference, linked_control_ids, residual_score, description)
// are NOT enumerated in the slice-105 AC list — the form omits them
// rather than invent UI for them. The wire shape carries them for the
// future slice that adds the richer editor.

export type RiskCreateInput = {
  title: string;
  description?: string;
  category: string;
  methodology?: string;
  inherent_score?: unknown;
  treatment?: string;
  treatment_owner?: string;
  residual_score?: unknown;
  review_due_at?: string | null;
  accepted_until?: string | null;
  accepter?: string;
  instrument_reference?: string;
  linked_control_ids?: string[];
};

export type RiskCreatedResponse = {
  risk: Risk;
};

// Server-side fn: hit the platform directly with the bearer. Mirrors
// `createVendor`'s shape (slice 024). The form goes through the BFF at
// `POST /api/risks` instead so the bearer stays httpOnly.
export async function createRisk(
  bearer: string,
  body: RiskCreateInput,
): Promise<Risk> {
  const res = await fetch(`${apiBaseURL()}/v1/risks`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON — keep the status line
    }
    throw new APIError(res.status, msg);
  }
  const decoded = (await res.json()) as RiskCreatedResponse;
  return decoded.risk;
}

// ----- Slice 102: /audits list view (browser-side BFF call) -----
//
// Row source: `periodWire` in `internal/api/auditperiods/handlers.go`
// (the canonical mapping per design doc §7). The page at
// `web/app/(authed)/audits/page.tsx` calls `fetchAuditPeriods` from the
// browser; the BFF at `web/app/api/audits/route.ts` is the server-side
// counterpart that injects the bearer cookie (slice 094 pattern).
//
// `frozen_at`, `frozen_hash`, `frozen_by` are present on the wire ONLY
// when the period is frozen (omitempty on the Go side). The TypeScript
// types reflect that with optional + nullable fields.
//
// `audit_periods.status` is constrained to `('open', 'frozen')` in v1
// per migration `20260511000020_audit_periods.sql`. The slice text
// mentions `planned/in-progress/frozen/closed` as forward-looking
// statuses; the page renders whatever status the backend returns and
// treats anything non-`frozen` as "live" for the in-progress amber-dot
// cue. This is forward-compatible: when the backend lifts the CHECK
// constraint to include more statuses, the renderer keeps working.

export type AuditPeriod = {
  id: string;
  name: string;
  framework_version_id: string;
  period_start: string;
  period_end: string;
  status: string;
  frozen_at?: string | null;
  frozen_hash?: string | null;
  frozen_by?: string | null;
  created_by: string;
  created_at: string;
  updated_at: string;
};

export type AuditPeriodsListResponse = {
  audit_periods: AuditPeriod[];
  count: number;
};

export async function fetchAuditPeriods(): Promise<AuditPeriodsListResponse> {
  const res = await fetch(`/api/audits`);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON — keep the status line */
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as AuditPeriodsListResponse;
}

// ----- Slice 099 + 106: /evidence list view (browser-side BFF call) -----
//
// Row source: `evidenceWire` in `internal/api/controldetail/handler.go`
// (the row shape `GET /v1/evidence` returns — both per-control and
// tenant-wide paths use the same wire shape). The page at
// `web/app/(authed)/evidence/page.tsx` calls `fetchEvidenceList` from
// the browser; the BFF at `web/app/api/evidence/route.ts` is the
// server-side counterpart that injects the bearer cookie (slice 094
// pattern) and forwards the whitelisted query params.
//
// Slice 106 changes:
//   * `result` is now on the GET wire shape (the column has always
//     existed on `evidence_records.result`; slice 064 omitted it).
//     The page renders the real result cell — no more em-dash
//     placeholder.
//   * `fetchEvidenceList` accepts an optional filter object. When
//     `controlID` is omitted the BFF + upstream serve the tenant-wide
//     ledger window (RLS continues to scope tenancy).

export type EvidenceResultEnum = "pass" | "fail" | "na" | "inconclusive";

export type EvidenceRecord = {
  evidence_id: string;
  evidence_kind: string | null;
  observed_at: string;
  // `source` is the slice-013 provenance JSONB verbatim. Shape varies
  // by connector — we render a summary client-side. `null` when the row
  // has no provenance metadata recorded.
  source: Record<string, unknown> | null;
  content_hash: string;
  scope_cell: string | null;
  /**
   * Slice 106: the `evidence_records.result` enum, surfaced on the GET
   * wire shape. One of pass | fail | na | inconclusive. Always set —
   * the column is NOT NULL on the table.
   */
  result: EvidenceResultEnum;
};

export type EvidenceListResponse = {
  // Empty string when the request was tenant-wide (no control_id).
  control_id: string;
  evidence: EvidenceRecord[];
  count: number;
  /**
   * Slice 236: tenant-wide ledger row count (ignores filter predicates).
   * Surfaced so the `/evidence` page can render
   * `Showing N of M records` and disambiguate "ledger is empty" from
   * "my filters narrowed to zero". The query runs through the same
   * RLS-bound pool — tenant isolation is preserved.
   */
  total: number;
  next_cursor: string;
};

/**
 * Filter options for `fetchEvidenceList`. All fields are optional — an
 * empty object yields the tenant-wide ledger window.
 *
 * Slice 234 — `scopeCellID` and `since` join the existing set: the
 * `/evidence` filter row now ships six pills (Control, Kind, Result,
 * Source, Scope, Since).
 */
export type EvidenceListFilters = {
  controlID?: string;
  kind?: string;
  result?: EvidenceResultEnum;
  sourceActorType?: string;
  sourceActorID?: string;
  /**
   * Slice 234 — narrow the ledger to one scope cell. Server-side
   * intersection (the cell UUID's evidence rows). Out-of-tenant cells
   * return zero rows naturally via RLS.
   */
  scopeCellID?: string;
  /**
   * Slice 234 — RFC3339 timestamp; the upstream applies
   * `observed_at >= since`. The Since filter pill maps preset windows
   * ("Last 24 hours", "Last 7 days", "Last 30 days", "Audit period")
   * to a concrete RFC3339 cutoff client-side and passes it here.
   */
  since?: string;
  cursor?: string;
  limit?: number;
};

// Browser-side fetcher used by the slice-099 page. Hits the BFF at
// `/api/evidence[?...]` which forwards the bearer cookie to upstream
// `/v1/evidence[?...]`. Slice 106: signature changed from
// `fetchEvidenceList(controlID: string)` to
// `fetchEvidenceList(filters: EvidenceListFilters)` — when `controlID`
// is omitted the request returns the tenant-wide window.
export async function fetchEvidenceList(
  filters: EvidenceListFilters = {},
): Promise<EvidenceListResponse> {
  const qs = new URLSearchParams();
  if (filters.controlID) qs.set("control_id", filters.controlID);
  if (filters.kind) qs.set("kind", filters.kind);
  if (filters.result) qs.set("result", filters.result);
  if (filters.sourceActorType)
    qs.set("source_actor_type", filters.sourceActorType);
  if (filters.sourceActorID) qs.set("source_actor_id", filters.sourceActorID);
  // Slice 234 — new pills.
  if (filters.scopeCellID) qs.set("scope_cell_id", filters.scopeCellID);
  if (filters.since) qs.set("since", filters.since);
  if (filters.cursor) qs.set("cursor", filters.cursor);
  if (filters.limit) qs.set("limit", String(filters.limit));

  const url = qs.toString()
    ? `/api/evidence?${qs.toString()}`
    : `/api/evidence`;
  const res = await fetch(url);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON — keep the status line */
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as EvidenceListResponse;
}

// ----- Slice 177: /exceptions list view -----
//
// Row source: `exceptionWire` in `internal/api/exceptions/handlers.go`
// (slice 021 / slice 138 wire shape — same row that the slice 138
// exceptions data-export materialises). The page at
// `web/app/(authed)/exceptions/page.tsx` calls `fetchExceptionsList`
// from the browser; the BFF at `web/app/api/exceptions/route.ts` is the
// server-side counterpart that injects the bearer cookie (slice 094
// pattern). Mirrors the slice 098 controls / slice 099 evidence shape.
//
// Filter parameters whitelisted on the BFF + this fetcher:
//   - status (requested / approved / denied / active / expired)
//   - control_id (UUID — narrows to one anchor's exceptions)
//
// All other params (`tenant_id`, debug flags, etc.) are dropped at the
// BFF. RLS enforces tenant isolation at the DB layer per invariant 6.

/**
 * Status enum for exception rows. Mirrors the constants in
 * `internal/exception/store.go` (StateRequested / StateApproved /
 * StateDenied / StateActive / StateExpired). Lifecycle:
 *   requested → approved → active → expired
 *                       ↘ denied
 */
export type ExceptionStatus =
  | "requested"
  | "approved"
  | "denied"
  | "active"
  | "expired";

/**
 * Exception is the canonical wire shape for an exception/waiver row.
 * Mirrors `exceptionWire` in `internal/api/exceptions/handlers.go`. The
 * optional timestamps reflect the lifecycle: `approved_at` is set after
 * approve, `effective_from` after activate, `expired_at` after the
 * row's expiry sweep ran. `justification` is sensitive (slice 138
 * P0-A-Ledger-3) — surfaced in the table truncated, full text only in
 * row drawer.
 */
export type Exception = {
  id: string;
  control_id: string;
  scope_cell_predicate: unknown;
  justification: string;
  compensating_controls: string[];
  requested_by: string;
  requested_at: string;
  approved_by?: string;
  approved_at?: string;
  denied_by?: string;
  denied_at?: string;
  activated_by?: string;
  activated_at?: string;
  effective_from?: string;
  expires_at: string;
  expired_at?: string;
  status: ExceptionStatus;
  created_at: string;
  updated_at: string;
};

export type ExceptionsListResponse = {
  exceptions: Exception[];
  count: number;
};

/**
 * Filter options for `fetchExceptionsList`. All fields are optional —
 * an empty object yields the tenant-wide list (RLS-scoped).
 */
export type ExceptionsListFilters = {
  status?: ExceptionStatus | "";
  controlId?: string;
};

/**
 * Browser-side fetcher used by the slice 177 page. Hits the BFF at
 * `/api/exceptions[?...]` which forwards the bearer cookie to upstream
 * `/v1/exceptions[?...]`. The bearer never reaches the browser.
 */
export async function fetchExceptionsList(
  filters: ExceptionsListFilters = {},
): Promise<ExceptionsListResponse> {
  const qs = new URLSearchParams();
  if (filters.status) qs.set("status", filters.status);
  if (filters.controlId) qs.set("control_id", filters.controlId);

  const url = qs.toString()
    ? `/api/exceptions?${qs.toString()}`
    : `/api/exceptions`;
  const res = await fetch(url);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON — keep the status line */
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as ExceptionsListResponse;
}

// ----- Slice 101: /policies list view -----
//
// Row source: `policyWire` in `internal/api/policies/handlers.go` (the
// canonical mapping per design doc `Plans/canvas/12-ui-fill-in-design-
// decisions.md` §7). The page at `web/app/(authed)/policies/page.tsx`
// calls `fetchPoliciesList` from the browser; the BFF at
// `web/app/api/policies/route.ts` is the server-side counterpart that
// injects the bearer cookie (slice 094 pattern).
//
// Ack-rate sourcing: the slice 101 design doc + the slice text both
// note that `GET /v1/policies` should be extended with
// `?include=ack_rate` so the list endpoint returns one ack-rate cell
// per row in one round-trip. That extension does NOT exist on main as
// of slice 101 — the spillover slice 107 files it. Until it lands, the
// `ack_rate` field on `Policy` is `null`; the page renders an em-dash
// honestly (precedent: slice 098 D1 for state cells; same pattern).
// Client-side per-row fan-out is explicitly forbidden by P0-A2.

export type PolicyAckRate = {
  numerator: number;
  denominator: number;
  /** Percent in 0..100, null when denominator is zero or window unsettled. */
  percent: number | null;
};

export type Policy = {
  id: string;
  predecessor_id?: string | null;
  title: string;
  version: string;
  effective_date?: string | null;
  body_md: string;
  owner_role: string;
  approver_role: string;
  linked_control_ids: string[];
  acknowledgment_required_roles: string[];
  status: string;
  source_attribution: string;
  created_by: string;
  submitted_at?: string | null;
  submitted_by?: string | null;
  approved_at?: string | null;
  approved_by?: string | null;
  published_at?: string | null;
  published_by?: string | null;
  superseded_at?: string | null;
  created_at: string;
  updated_at: string;
  warnings?: string[];
  /**
   * Optional joined ack-rate cell. Set ONLY when the backend supports
   * `?include=ack_rate` (spillover slice 107). Until that lands the
   * field is always undefined / null and the page renders em-dash.
   */
  ack_rate?: PolicyAckRate | null;
};

export type PoliciesListResponse = {
  policies: Policy[];
};

// Server-side fetcher used by the slice 101 BFF route. Hits the
// upstream `/v1/policies?include=ack_rate` with the bearer.
//
// Slice 107: the `?include=ack_rate` query parameter is hard-coded on
// the BFF (mirrors slice 104's hard-coded `?include=state` for anchors)
// — every list-view caller wants the joined cell. The upstream returns
// `ack_rate: null` for non-published rows and a populated cell for
// published rows; the page renders accordingly.
export async function listPolicies(bearer: string): Promise<Policy[]> {
  const res = await apiFetch("/v1/policies?include=ack_rate", bearer);
  const body = (await res.json()) as { policies: Policy[]; count: number };
  return body.policies;
}

// Browser-side fetcher used by the slice 101 page. Hits the BFF at
// `/api/policies` which forwards the bearer cookie to upstream and
// hard-codes `?include=ack_rate` (slice 107).
export async function fetchPoliciesList(): Promise<PoliciesListResponse> {
  const res = await fetch(`/api/policies`);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON — keep the status line */
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as PoliciesListResponse;
}

// ===== Slice 108 — /v1/me/* (profile + preferences + sessions) =====

export type MeProfile = {
  user_id: string;
  tenant_id: string;
  display_name: string;
  email: string;
  idp_subject: string;
  tenant_role: string;
  time_zone: string | null;
  is_admin: boolean;
  owner_roles: string[];
  // Slice 130 (extended by slice 154): canonical `user_roles` list.
  // Always present on the wire — empty array, never omitted — so
  // callers can rely on it without a nil-check. The Profile section
  // on /settings renders the additional roles (excluding the primary
  // admin/user already shown via the `is_admin` badge) as a muted
  // tail, mirroring the `Plans/mockups/settings.html` "admin +
  // grc_engineer" pattern.
  roles: string[];
};

export type MePatchRequest = {
  display_name?: string;
  time_zone?: string;
};

export type MePreferences = Record<string, Record<string, boolean>>;

// Slice 162: extended with `user_agent`, `ip_address`, `geo_country`, `geo_city`.
// All four are optional — the backend wire shape emits them with `omitempty`,
// so a row that was created before the slice-162 migration (or by a flow that
// had no http.Request in scope) arrives with the field absent. The settings
// page's session-line helper treats `undefined` identically to empty — honest
// empty render, no fabricated placeholder text (slice 162 P0-162-1).
export type MeSession = {
  id: string;
  last4: string;
  created_at: string;
  last_used_at: string | null;
  is_current: boolean;
  user_agent?: string;
  ip_address?: string;
  geo_country?: string;
  geo_city?: string;
};

export type MeSessionsResponse = {
  sessions: MeSession[];
  count: number;
};

// Browser-side fetchers — go through the BFF at /api/me/* so the session-cookie
// bearer is attached server-side. The BFF routes proxy to the platform /v1/me/*.

export async function getMe(): Promise<MeProfile> {
  const res = await fetch(`/api/me`, { cache: "no-store" });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as MeProfile;
}

export async function patchMe(body: MePatchRequest): Promise<MeProfile> {
  const res = await fetch(`/api/me`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON */
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as MeProfile;
}

export async function getMyPreferences(): Promise<MePreferences> {
  const res = await fetch(`/api/me/preferences`, { cache: "no-store" });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  const body = (await res.json()) as { preferences: MePreferences };
  return body.preferences;
}

export async function patchMyPreferences(
  partial: MePreferences,
): Promise<MePreferences> {
  const res = await fetch(`/api/me/preferences`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(partial),
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON */
    }
    throw new APIError(res.status, msg);
  }
  const body = (await res.json()) as { preferences: MePreferences };
  return body.preferences;
}

export async function listMySessions(): Promise<MeSession[]> {
  const res = await fetch(`/api/me/sessions`, { cache: "no-store" });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  const body = (await res.json()) as MeSessionsResponse;
  return body.sessions ?? [];
}

export async function revokeMySession(id: string): Promise<void> {
  const res = await fetch(`/api/me/sessions/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
  if (!res.ok && res.status !== 204) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
}
