// Slice 471 — BFF proxy for `POST /v1/controls/checklist:generate`.
//
// Generates a cited, role-sectioned, NON-BINDING checklist DRAFT. This forwards
// the operator's bearer to the platform, which enforces the AI-assist boundary:
// the which-control -> which-role split is DETERMINISTIC, every item is cited to
// a real tenant-owned id (validated before the operator sees it), and nothing is
// authoritative until each section is approved.
//
// Constitutional invariants:
//   * Invariant 6: bearer-forward only; RLS enforces tenancy.
//   * P0-471-1: the BFF never marks anything approved — approval is a separate
//     per-section POST.

import { forwardJSON } from "@/lib/api/bff";

export async function POST(): Promise<Response> {
  // The platform path uses the `:generate` verb suffix; encode it literally.
  return forwardJSON("/v1/controls/checklist:generate", { method: "POST" });
}
