// #2088 — the evidence kinds registered for the tenant, for the API-key
// scope builder's kinds picker. Read from the schema registry
// (`GET /v1/schemas`, internal/api/schemaregistry wireListItem).

import { apiFetch, bffControlFetch } from "./_shared";

export type EvidenceSchemaListItem = {
  kind: string;
  semver: string;
  owner: string;
};

export type EvidenceKindsResponse = { schemas: EvidenceSchemaListItem[] };

// The registry's page cap (schemaregistry maxLimit).
const SCHEMA_LIMIT = 500;

// Server-side fn: the /api/evidence-kinds BFF route calls the platform
// with the caller's bearer.
export async function listEvidenceSchemas(
  bearer: string,
): Promise<EvidenceSchemaListItem[]> {
  const res = await apiFetch(`/v1/schemas?limit=${SCHEMA_LIMIT}`, bearer);
  const body = (await res.json()) as Partial<EvidenceKindsResponse>;
  return body.schemas ?? [];
}

// Browser-side fn: the kinds picker calls this via TanStack Query.
export function fetchEvidenceKinds(): Promise<EvidenceKindsResponse> {
  return bffControlFetch<EvidenceKindsResponse>("/api/evidence-kinds");
}
