// Slice 370 — FrameworkScope (slice 018), extracted from the former
// `web/lib/api.ts` god-file.

import { apiBaseURL, APIError } from "./base";
import { apiFetch } from "./_shared";

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
