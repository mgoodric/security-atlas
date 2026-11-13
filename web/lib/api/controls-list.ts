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

// Issue #1741: the totals ride along so the page can show "N of the
// catalog's M anchors" without a second call. `total` is the row count
// matching the request's `has_control` filter; `with_control_total` is
// how many anchors the caller's tenant has a control on. Optional
// because hand-rolled fixtures predate them.
export type ControlsListResponse = {
  anchors: AnchorWithState[];
  total?: number;
  catalog_total?: number;
  with_control_total?: number;
};

// Slice 224 — accepts an optional `scopeCellID` that the page forwards
// to the BFF as `?scope=<cell_id>`. Empty / undefined value omits the
// query param entirely so the BFF takes its no-filter branch.
//
// Issue #1741 — `hasControl` forwards `?has_control=true|false`, which
// the platform applies in SQL ahead of paging.
export async function fetchControlsList(
  scopeCellID?: string,
  hasControl?: boolean,
  limit?: number,
): Promise<ControlsListResponse> {
  const qs = new URLSearchParams();
  if (scopeCellID) qs.set("scope", scopeCellID);
  if (hasControl !== undefined) qs.set("has_control", String(hasControl));
  if (limit !== undefined) qs.set("limit", String(limit));
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

// Issue #1832 — the catalog totals alone, for the "My controls" header.
// The totals ride on every anchor page, so one row is enough to read
// them; the page used to pull up to 100 anchors with state to do it.
export function fetchCatalogCounts(): Promise<ControlsListResponse> {
  return fetchControlsList(undefined, true, 1);
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

// ----- Issue #1741: the tenant's own controls, with state -----
//
// `ControlWithState` mirrors `controlWithStateWire` in
// internal/api/controls/list.go (`GET /v1/controls?include=state`). One
// row per active tenant control, with that control's own evaluation
// rollup, so two controls on one SCF anchor never share a state cell.
// This is the /controls page's default "My controls" view; the SCF
// catalog stays available as a separate view over `/api/controls`.
export type ControlWithState = TenantControl & {
  scf_anchor_id: string | null;
  state: AnchorWithState["state"];
  frameworks: string[];
};

export type ControlsWithStateResponse = {
  controls: ControlWithState[];
  count: number;
  total: number;
};

/** The platform's page cap for `GET /v1/controls?include=state`. */
export const CONTROLS_WITH_STATE_PAGE_LIMIT = 500;

// Server-side fn: one page of `/v1/controls?include=state`, used by the
// `/api/controls-list` BFF when the browser asks for state.
export async function fetchTenantControlsWithState(
  bearer: string,
  opts: { scope?: string; limit?: number; offset?: number } = {},
): Promise<ControlsWithStateResponse> {
  const qs = new URLSearchParams({ include: "state" });
  if (opts.scope) qs.set("scope", opts.scope);
  if (opts.limit !== undefined) qs.set("limit", String(opts.limit));
  if (opts.offset !== undefined) qs.set("offset", String(opts.offset));
  const res = await apiFetch(`/v1/controls?${qs.toString()}`, bearer);
  const body = (await res.json()) as ControlsWithStateResponse;
  return {
    controls: body.controls ?? [],
    count: body.count ?? 0,
    total: body.total ?? 0,
  };
}

// Issue #1832 — the one active-control count. The sidebar badge and the
// /controls page both read it from a `/api/controls-list?include=state`
// page, whose `total` is the platform's CountActiveControls. `count` and
// the row length are fallbacks for payloads without a total.
export function activeControlsTotal(
  page: Partial<ControlsWithStateResponse>,
): number {
  return page.total ?? page.count ?? page.controls?.length ?? 0;
}

// Browser-side fn: one page of the caller's controls with state, through
// the BFF.
async function fetchMyControlsPage(
  qs: URLSearchParams,
): Promise<Partial<ControlsWithStateResponse>> {
  qs.set("include", "state");
  const res = await fetch(`/api/controls-list?${qs.toString()}`);
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
  return (await res.json()) as Partial<ControlsWithStateResponse>;
}

// Browser-side fn: the caller's active-control count for the sidebar
// badge. One row is fetched; only the total is read.
export async function fetchMyControlsTotal(): Promise<number> {
  const page = await fetchMyControlsPage(new URLSearchParams({ limit: "1" }));
  return activeControlsTotal(page);
}

// Browser-side fn: every active control of the caller's tenant, walked
// page by page through the BFF until `total` is reached. The page's
// family / state / freshness filters run over the full set, so it must
// not stop at the first page.
export async function fetchMyControls(
  scopeCellID?: string,
): Promise<ControlsWithStateResponse> {
  const controls: ControlWithState[] = [];
  let total = 0;
  for (;;) {
    const qs = new URLSearchParams({
      limit: String(CONTROLS_WITH_STATE_PAGE_LIMIT),
      offset: String(controls.length),
    });
    if (scopeCellID) qs.set("scope", scopeCellID);
    const page = await fetchMyControlsPage(qs);
    const rows = page.controls ?? [];
    total = activeControlsTotal(page);
    controls.push(...rows);
    if (rows.length === 0 || controls.length >= total) break;
  }
  return { controls, count: controls.length, total };
}
