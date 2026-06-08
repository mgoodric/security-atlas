// Slice 589 — typed client for the OSCAL vendor-claim read API + the operator
// accept/reject/needs-info disposition. A vendor claim is an ASSERTION, not
// platform-verified evidence: dispositioning a claim records the operator's
// decision and NEVER auto-satisfies a control (mirrors the platform's
// canvas invariant #2 / P0-512-1).

import { apiBaseURL, APIError } from "./base";
import { apiFetch } from "./_shared";

export type ClaimStatus = "asserted" | "accepted" | "rejected" | "needs_info";
export type Disposition = "accept" | "reject" | "needs-info";

export type ComponentDefinitionSummary = {
  id: string;
  source_label: string;
  catalog_title: string;
  oscal_version: string;
  source_sha256: string;
  claim_count: number;
  imported_by: string;
  imported_at: string;
};

export type ComponentClaim = {
  id: string;
  imported_component_id: string;
  component_uuid: string;
  component_title: string;
  component_type: string;
  control_id: string;
  statement: string;
  requirement_uuid: string;
  scf_anchor_id?: string | null;
  unmapped: boolean;
  // Always true — a vendor claim is an assertion, never platform-verified
  // evidence. Surfaced explicitly so the UI can label it honestly.
  is_vendor_claim: boolean;
  claim_status: ClaimStatus;
  dispositioned_by?: string | null;
  dispositioned_at?: string | null;
  disposition_note: string;
};

export type ComponentDefinitionDetail = {
  id: string;
  source_label: string;
  catalog_title: string;
  oscal_version: string;
  source_sha256: string;
  imported_by: string;
  imported_at: string;
  claims: ComponentClaim[];
};

export type ComponentDefinitionList = {
  component_definitions: ComponentDefinitionSummary[];
  count: number;
};

export type DispositionResult = {
  id: string;
  control_id: string;
  is_vendor_claim: boolean;
  claim_status: ClaimStatus;
  dispositioned_by?: string | null;
  dispositioned_at?: string | null;
  disposition_note: string;
};

// Slice 620 — the result of mapping an unmapped claim to an SCF anchor. The
// claim stays a claim (is_vendor_claim=true, claim_status untouched); mapping
// only sets the crosswalk (scf_anchor_id) + clears the unmapped flag.
export type MapScfAnchorResult = {
  id: string;
  control_id: string;
  is_vendor_claim: boolean;
  claim_status: ClaimStatus;
  scf_anchor_id: string;
  unmapped: boolean;
};

// ----- server-side (BFF) callers — bearer from session cookie -----

export async function listComponentDefinitions(
  bearer: string,
): Promise<ComponentDefinitionList> {
  const res = await apiFetch("/v1/oscal/component-definitions", bearer);
  return (await res.json()) as ComponentDefinitionList;
}

export async function getComponentDefinition(
  bearer: string,
  id: string,
): Promise<ComponentDefinitionDetail> {
  const res = await apiFetch(
    `/v1/oscal/component-definitions/${encodeURIComponent(id)}`,
    bearer,
  );
  return (await res.json()) as ComponentDefinitionDetail;
}

export async function dispositionClaim(
  bearer: string,
  claimID: string,
  disposition: Disposition,
  note: string,
): Promise<DispositionResult> {
  const res = await fetch(
    `${apiBaseURL()}/v1/oscal/component-claims/${encodeURIComponent(
      claimID,
    )}:${disposition}`,
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ note }),
    },
  );
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as DispositionResult;
}

// Slice 620 — map an unmapped vendor claim to a canonical SCF anchor. The
// scf_anchor_id is the SCF code (e.g. "IAC-06"); the platform validates it
// against the bundled catalog. Requirement -> SCF anchor only (invariant #7);
// this only sets the crosswalk and NEVER fabricates control coverage.
export async function mapClaimScfAnchor(
  bearer: string,
  claimID: string,
  scfAnchorID: string,
): Promise<MapScfAnchorResult> {
  const res = await fetch(
    `${apiBaseURL()}/v1/oscal/component-claims/${encodeURIComponent(
      claimID,
    )}/scf-anchor`,
    {
      method: "PATCH",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ scf_anchor_id: scfAnchorID }),
    },
  );
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // keep status line
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as MapScfAnchorResult;
}
