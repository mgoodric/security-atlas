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
