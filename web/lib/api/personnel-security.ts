// OE-664 — personnel-security checklist client. Mirrors the slice-384
// action-plans client shape (lib/api/action-plans.ts): browser-side
// fetchers that hit the BFF at `/api/personnel-security/...`, which
// forwards the bearer cookie to upstream `/v1/personnel-security/...`.
// The bearer never reaches the browser.
//
// Wire source: `checklistWire` / `itemWire` in
// `internal/api/personnelsecurity/handlers.go` (OE-663). Tenant
// isolation is enforced by RLS at the DB layer (invariant 6); the UI
// never passes tenant_id, and a cross-tenant id resolves to a clean
// upstream 404 (no cross-tenant roster leakage).

import { APIError } from "./base";

/** Workflow kind. Mirrors internal/personnelsecurity WorkflowKind. */
export type ChecklistKind = "onboarding" | "offboarding";

export const CHECKLIST_KINDS: ChecklistKind[] = ["onboarding", "offboarding"];

/** Checklist lifecycle status (open until every item completes). */
export type ChecklistStatus = "open" | "completed";

export const CHECKLIST_STATUSES: ChecklistStatus[] = ["open", "completed"];

/** One checklist item. Mirrors `itemWire`. */
export type ChecklistItem = {
  id: string;
  checklist_id: string;
  slug: string;
  title: string;
  category: string;
  sort_order: number;
  completed_at?: string;
  completed_by: string;
  evidence_record_id?: string;
  evidence_uri: string;
  notes: string;
};

/** One checklist. Mirrors `checklistWire`; `items` is `[]` on the list path. */
export type Checklist = {
  id: string;
  kind: ChecklistKind;
  source: string;
  source_event_id: string;
  person_external_id: string;
  person_work_email: string;
  person_display_name: string;
  control_id?: string;
  due_at: string;
  status: ChecklistStatus;
  items: ChecklistItem[];
};

export type ChecklistsListResponse = {
  checklists: Checklist[];
  count: number;
};

export type ChecklistDetailResponse = {
  checklist: Checklist;
};

export type ChecklistsListFilters = {
  kind?: ChecklistKind | "";
  status?: ChecklistStatus | "";
  overdue?: boolean;
};

async function parse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON — keep the status line */
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as T;
}

/** List the tenant's personnel-security checklists (RLS-scoped). */
export async function fetchPersonnelChecklists(
  filters: ChecklistsListFilters = {},
): Promise<ChecklistsListResponse> {
  const qs = new URLSearchParams();
  if (filters.kind) qs.set("kind", filters.kind);
  if (filters.status) qs.set("status", filters.status);
  if (filters.overdue) qs.set("overdue", "true");
  const url = qs.toString()
    ? `/api/personnel-security/checklists?${qs.toString()}`
    : `/api/personnel-security/checklists`;
  return parse<ChecklistsListResponse>(await fetch(url));
}

/** Fetch one checklist plus its items. */
export async function fetchPersonnelChecklist(
  id: string,
): Promise<ChecklistDetailResponse> {
  return parse<ChecklistDetailResponse>(
    await fetch(`/api/personnel-security/checklists/${encodeURIComponent(id)}`),
  );
}

/** The shape POSTed to create a manual checklist (source=manual). */
export type CreateChecklistInput = {
  kind: ChecklistKind;
  person_external_id: string;
  person_work_email?: string;
  person_display_name?: string;
  control_id?: string;
  /** RFC 3339 timestamp; omitted → the store's kind-specific default. */
  due_at?: string;
};

/** Create a manual checklist; returns the created checklist + items. */
export async function createPersonnelChecklist(
  input: CreateChecklistInput,
): Promise<ChecklistDetailResponse> {
  return parse<ChecklistDetailResponse>(
    await fetch(`/api/personnel-security/checklists`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        kind: input.kind,
        person_external_id: input.person_external_id,
        person_work_email: input.person_work_email ?? "",
        person_display_name: input.person_display_name ?? "",
        control_id: input.control_id ?? "",
        ...(input.due_at ? { due_at: input.due_at } : {}),
      }),
    }),
  );
}

/** The shape POSTed to complete an item (evidence + tracking only). */
export type CompleteItemInput = {
  /** Defaults server-side to the caller's credential UserID when empty. */
  completed_by?: string;
  evidence_uri?: string;
  notes?: string;
};

/**
 * Complete a checklist item. The platform writes the
 * personnel_security.workflow.v1 evidence record and the completion
 * columns in one transaction (invariant 9: manual evidence is
 * first-class). An already-completed or cross-tenant item is a 404.
 */
export async function completePersonnelChecklistItem(
  itemId: string,
  input: CompleteItemInput = {},
): Promise<{ item: ChecklistItem }> {
  return parse<{ item: ChecklistItem }>(
    await fetch(
      `/api/personnel-security/checklist-items/${encodeURIComponent(
        itemId,
      )}/complete`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          completed_by: input.completed_by ?? "",
          evidence_uri: input.evidence_uri ?? "",
          notes: input.notes ?? "",
        }),
      },
    ),
  );
}
