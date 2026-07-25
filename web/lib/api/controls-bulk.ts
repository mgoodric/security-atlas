// Slice 468 — browser-side client for the bulk owner-assign action.
//
// Calls the BFF (`/api/controls/bulk-assign-owner`), which proxies the
// upstream `/v1/controls:bulk-assign-owner`. The upstream re-checks role +
// tenant PER ITEM (the authz amplifier, AC-11) and caps the set server-side;
// the client SELECTION_CAP is ergonomics, not the security boundary.

import { APIError } from "./base";

export type BulkAssignOwnerResponse = {
  owner_user_id: string;
  assigned_by: string;
  assigned: number;
  control_ids: string[];
};

/**
 * Assign `ownerUserId` to every control in `controlIds`. All-or-nothing on
 * the server (no silent partial apply) — a 4xx means nothing was applied.
 * Throws APIError with the upstream message on failure.
 */
export async function bulkAssignOwner(
  controlIds: string[],
  ownerUserId: string,
): Promise<BulkAssignOwnerResponse> {
  const res = await fetch("/api/controls/bulk-assign-owner", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      owner_user_id: ownerUserId,
      control_ids: controlIds,
    }),
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
  return (await res.json()) as BulkAssignOwnerResponse;
}
