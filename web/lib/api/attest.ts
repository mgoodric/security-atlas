// Slice 370 — manual control attestation + artifact upload (slice 011 /
// slice 036), extracted from the former `web/lib/api.ts` god-file.

import { apiBaseURL, APIError } from "./base";
import { apiFetch } from "./_shared";

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
