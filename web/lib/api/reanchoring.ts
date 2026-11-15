// Issue #2176 — controls left on a legacy SCF release's anchor that need
// re-anchoring (#1971 D3). GET /v1/controls/needs-reanchoring lists them
// for the bearer's tenant, each with a note saying where its anchor's
// concept went. Pinned controls (a declared scf_release) are excluded
// upstream. The dashboard warning reads the count; the list page reads
// the rows.

import { apiFetch, bffControlFetch } from "./_shared";

export interface ControlNeedingReanchoring {
  id: string;
  bundle_id: string;
  title: string;
  legacy_scf_id: string;
  legacy_anchor_title: string;
  legacy_release: string;
  current_release: string;
  note: string;
}

export interface NeedsReanchoringResponse {
  controls: ControlNeedingReanchoring[];
  count: number;
}

export async function getControlsNeedingReanchoring(
  bearer: string,
): Promise<NeedsReanchoringResponse> {
  const res = await apiFetch(`/v1/controls/needs-reanchoring`, bearer);
  return (await res.json()) as NeedsReanchoringResponse;
}

export function fetchControlsNeedingReanchoring(): Promise<NeedsReanchoringResponse> {
  return bffControlFetch<NeedsReanchoringResponse>(
    `/api/controls/needs-reanchoring`,
  );
}
