// Slice 370 — SCF anchors + catalog primitive types, extracted from the
// former `web/lib/api.ts` god-file. The anchor/requirement types are the
// catalog vocabulary shared by the controls-list module (which imports
// `AnchorWithState` from here, decision D6).

import { apiFetch } from "./_shared";

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

// Slice 484 — `frameworkVersion` pins the reverse traversal to one framework
// version, forwarded upstream as `?framework_version=slug:version` (e.g.
// `soc2:2017`). When omitted, the upstream defaults to each framework's CURRENT
// version (ADR 0019 §4) — a legacy/superseded version is returned ONLY when
// explicitly pinned, never bled into the default.
export async function getAnchorRequirements(
  bearer: string,
  id: string,
  frameworkVersion?: string,
): Promise<AnchorDetail> {
  let path = `/v1/anchors/${encodeURIComponent(id)}/requirements`;
  if (frameworkVersion) {
    path += `?framework_version=${encodeURIComponent(frameworkVersion)}`;
  }
  const res = await apiFetch(path, bearer);
  return (await res.json()) as AnchorDetail;
}
