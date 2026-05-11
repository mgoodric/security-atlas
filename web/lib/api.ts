// Client-side API helpers for the platform's HTTP endpoints. The bearer
// token lives in a cookie that the platform reads server-side; client-side
// fetches send the cookie via credentials: "include".
//
// The base URL points at the platform's HTTP listener (default :8080).
// `NEXT_PUBLIC_API_BASE_URL` overrides it in dev / staging / prod.

const DEFAULT_BASE = "http://localhost:8080";

export function apiBaseURL(): string {
  return process.env.NEXT_PUBLIC_API_BASE_URL || DEFAULT_BASE;
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
