// Slice 370 — controls list view + scope cells + tenant controls,
// extracted from the former `web/lib/api.ts` god-file.

import { APIError } from "./base";
import { apiFetch } from "./_shared";
import type { AnchorWithState } from "./anchors";

// ----- Slice 098 + 104: /controls list view (browser-side BFF call) -----
//
// The page at `web/app/(authed)/controls/page.tsx` calls this from the
// browser; the BFF handler at `web/app/api/controls/route.ts` is the
// server-side counterpart that injects the bearer cookie.
//
// Slice 098 shipped this view bound to the catalog (anchorWire only,
// state columns rendered as `—`). Slice 104 lifts the BFF to the
// joined `?include=state` shape — every row now carries either a real
// state cell or `state: null` (anchor has no tenant control). The
// frontend table renders the populated rows and the `—` placeholder
// stays for the null branch (slice 098 P0-A1 — no fabrication).

export type ControlsListResponse = {
  anchors: AnchorWithState[];
};

// Slice 224 — accepts an optional `scopeCellID` that the page forwards
// to the BFF as `?scope=<cell_id>`. Empty / undefined value omits the
// query param entirely so the BFF takes its no-filter branch.
export async function fetchControlsList(
  scopeCellID?: string,
): Promise<ControlsListResponse> {
  const qs = new URLSearchParams();
  if (scopeCellID) qs.set("scope", scopeCellID);
  const url = qs.toString()
    ? `/api/controls?${qs.toString()}`
    : `/api/controls`;
  const res = await fetch(url);
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
  return (await res.json()) as ControlsListResponse;
}

// ----- Slice 224: tenant scope cells (for the /controls Scope filter pill) -----
//
// `ScopeCell` mirrors `cellWire` in internal/api/scopes/handlers.go.
// The /controls page consumes this via the BFF route at
// `/api/scope-cells` to populate the Scope filter pill's dropdown
// options. Filtering by scope cell is a server-side concern
// (P0-224-2 — applicability_expr never reaches the browser); these
// types only carry the cell id + display label.

export type ScopeCell = {
  id: string;
  label: string;
  dimensions: Record<string, string>;
};

export type ScopeCellsListResponse = {
  cells: ScopeCell[];
};

// Server-side fn: hit the platform directly with the bearer. Used by the
// /api/scope-cells BFF route handler that runs server-side. Mirrors the
// listAnchorsWithState shape (no client-direct call from the browser —
// we never expose the platform's bearer to the page).
export async function listScopeCells(bearer: string): Promise<ScopeCell[]> {
  const res = await apiFetch("/v1/scopes/cells", bearer);
  const body = (await res.json()) as { cells: ScopeCell[] };
  return body.cells ?? [];
}

// Browser-side fn: the page calls this via TanStack Query. Hits the
// BFF, which injects the bearer cookie.
export async function fetchScopeCells(): Promise<ScopeCellsListResponse> {
  const res = await fetch("/api/scope-cells");
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
  return (await res.json()) as ScopeCellsListResponse;
}

// ----- Slice 151: tenant control list (for risk-create form picker) -----
//
// `TenantControl` mirrors `activeControlWire` in
// internal/api/controls/list.go. The slice-151 risk-create form's
// control-link multi-select consumes this shape.
//
// IMPORTANT: distinct from the slice 098 `Anchor` type. `Anchor` is the
// SCF catalog row (global, no tenant); `TenantControl` is a row from
// the tenant `controls` table. The risk-control link FK requires a
// tenant control id, not an anchor id — see
// `migrations/sql/20260511000005_risk_register.sql`.
export type TenantControl = {
  id: string;
  title: string;
  control_family: string;
  scf_id: string;
  lifecycle_state: string;
  bundle_id: string;
};

export type TenantControlsListResponse = {
  controls: TenantControl[];
  count: number;
};

// Server-side fn: hit the platform directly with the bearer. Used by the
// `/api/controls-list` BFF route handler that runs server-side.
export async function fetchTenantControls(
  bearer: string,
): Promise<TenantControl[]> {
  const res = await apiFetch("/v1/controls", bearer);
  const body = (await res.json()) as TenantControlsListResponse;
  return body.controls ?? [];
}

// Browser-side fn: hits the `/api/controls-list` BFF route the form
// uses. Returns the controls array on success or throws APIError on a
// non-2xx upstream.
export async function fetchTenantControlsList(): Promise<TenantControl[]> {
  const res = await fetch(`/api/controls-list`);
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
  const body = (await res.json()) as TenantControlsListResponse;
  return body.controls ?? [];
}
