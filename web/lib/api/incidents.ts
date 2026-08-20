// OE-633 — incident register web client + BFF helpers.
//
// The platform owns lifecycle validation. These helpers type the wire
// shape and proxy operator actions to `/v1/incidents`; they do not
// attempt to duplicate the state machine beyond presentation helpers
// for the next obvious action button.

import { apiBaseURL, APIError } from "./base";
import { apiFetch, bffControlFetch } from "./_shared";

export type IncidentStatus =
  | "detected"
  | "triaged"
  | "contained"
  | "resolved"
  | "closed";

export type IncidentSeverity = "p3" | "p2" | "p1" | "p0";

export type IncidentAffectedSystem = {
  name?: string;
  system?: string;
  service?: string;
  tier?: string;
  environment?: string;
  [key: string]: unknown;
};

export type Incident = {
  id: string;
  title: string;
  description: string;
  status: IncidentStatus;
  operator_severity: IncidentSeverity;
  severity: IncidentSeverity;
  affected_system_tier?: string | null;
  affected_systems: unknown;
  detected_by: string;
  detected_at: string;
  closed_by?: string | null;
  closed_at?: string | null;
  postmortem_summary?: string | null;
  created_at: string;
  updated_at: string;
};

export type IncidentLinks = {
  ControlIDs?: string[];
  RiskIDs?: string[];
  VendorIDs?: string[];
  EvidenceIDs?: string[];
  control_ids?: string[];
  risk_ids?: string[];
  vendor_ids?: string[];
  evidence_ids?: string[];
};

export type IncidentTimelineEntry = {
  id: string;
  action: string;
  actor: string;
  from_state?: IncidentStatus | null;
  to_state: IncidentStatus;
  summary: string;
  detail: unknown;
  occurred_at: string;
};

export type IncidentDetail = {
  record: Incident;
  links: IncidentLinks;
  timeline: IncidentTimelineEntry[];
};

export type IncidentsListResponse = {
  incidents: Incident[];
  count: number;
};

export type IncidentDetailResponse = {
  incident: IncidentDetail;
};

export type IncidentCreateInput = {
  title: string;
  description?: string;
  severity: IncidentSeverity;
  affected_system_tier?: string | null;
  affected_systems?: IncidentAffectedSystem[];
  detected_at?: string | null;
  control_ids?: string[];
  risk_ids?: string[];
  vendor_ids?: string[];
  evidence_ids?: string[];
};

export type IncidentTransitionInput = {
  to_state: Exclude<IncidentStatus, "detected">;
  summary?: string;
};

export type IncidentCloseInput = {
  postmortem_summary: string;
  observed_at?: string | null;
};

export async function listIncidents(
  bearer: string,
): Promise<IncidentsListResponse> {
  const res = await apiFetch("/v1/incidents", bearer);
  return (await res.json()) as IncidentsListResponse;
}

export async function getIncident(
  bearer: string,
  id: string,
): Promise<IncidentDetailResponse> {
  const res = await apiFetch(`/v1/incidents/${encodeURIComponent(id)}`, bearer);
  return (await res.json()) as IncidentDetailResponse;
}

export async function forwardIncidentWrite(
  bearer: string,
  path: string,
  method: "POST" | "PATCH",
  body: unknown,
): Promise<Response> {
  return fetch(`${apiBaseURL()}${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
    cache: "no-store",
  });
}

export async function fetchIncidentsList(): Promise<IncidentsListResponse> {
  return bffControlFetch<IncidentsListResponse>("/api/incidents");
}

export async function fetchIncidentDetail(
  id: string,
): Promise<IncidentDetailResponse> {
  return bffControlFetch<IncidentDetailResponse>(
    `/api/incidents/${encodeURIComponent(id)}`,
  );
}

export async function createIncident(
  body: IncidentCreateInput,
): Promise<IncidentDetailResponse> {
  return writeThroughBFF<IncidentDetailResponse>(
    "/api/incidents",
    "POST",
    body,
  );
}

export async function transitionIncident(
  id: string,
  body: IncidentTransitionInput,
): Promise<{ incident: Incident }> {
  return writeThroughBFF<{ incident: Incident }>(
    `/api/incidents/${encodeURIComponent(id)}/transition`,
    "PATCH",
    body,
  );
}

export async function closeIncident(
  id: string,
  body: IncidentCloseInput,
): Promise<IncidentDetailResponse> {
  return writeThroughBFF<IncidentDetailResponse>(
    `/api/incidents/${encodeURIComponent(id)}/close`,
    "POST",
    body,
  );
}

async function writeThroughBFF<T>(
  path: string,
  method: "POST" | "PATCH",
  body: unknown,
): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: { "Content-Type": "application/json" },
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
  return (await res.json()) as T;
}

export function linkIDs(
  links: IncidentLinks,
  kind: "controls" | "risks" | "vendors" | "evidence",
): string[] {
  switch (kind) {
    case "controls":
      return links.ControlIDs ?? links.control_ids ?? [];
    case "risks":
      return links.RiskIDs ?? links.risk_ids ?? [];
    case "vendors":
      return links.VendorIDs ?? links.vendor_ids ?? [];
    case "evidence":
      return links.EvidenceIDs ?? links.evidence_ids ?? [];
  }
}
