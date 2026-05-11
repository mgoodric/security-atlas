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
