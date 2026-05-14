// Slice 042 — audit workspace API client.
//
// Typed client functions + TanStack Query hook helpers binding four
// already-merged backend slices into the auditor workspace:
//
//   * Slice 025 — auditor role + scoped access
//       GET /v1/me/audit-period         single active assignment
//       GET /v1/me/audit-periods        all assignments
//   * Slice 026 — sample-pull primitives
//       POST /v1/populations            create a population
//       GET  /v1/populations/{id}       get one
//       POST /v1/samples                draw a sample
//       GET  /v1/samples/{id}           get one (with evidence_record_ids)
//       POST /v1/samples/{id}/annotations  annotate one record
//       GET  /v1/samples/{id}/annotations  list annotations
//   * Slice 027 — walkthrough recording
//       POST /v1/walkthroughs           create (status=draft)
//       GET  /v1/walkthroughs/{id}      get + tamper-check
//       POST /v1/walkthroughs/{id}/attachments  multipart upload
//   * Slice 029 — Audit Hub threaded comments
//       POST /v1/audit-notes            create a note (auditor_only | shared)
//       GET  /v1/audit-notes/thread     visible thread for a scope anchor
//       GET  /v1/me/notifications       caller's notifications
//
// CONSTITUTIONAL INVARIANT 10 (audit-period freezing): every endpoint
// reached from here is period-bounded. This module deliberately does NOT
// expose `/v1/controls/{id}/state` (the slice-012 live path) or any live
// evidence-list / freshness surface. Canvas §8.1: "auditor sees state as
// of audit_period_end, not live."
//
// All browser-side fetches go through the BFF under /api/audit/** so the
// bearer cookie never leaves the server. These functions are written to
// run client-side against the BFF (relative URLs); the BFF route handlers
// forward to the platform with the Authorization header.

// ----- error type -----

export class AuditAPIError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = "AuditAPIError";
  }
}

async function bffFetch(path: string, init?: RequestInit): Promise<unknown> {
  const res = await fetch(path, init);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON — keep the status line
    }
    throw new AuditAPIError(res.status, msg);
  }
  return res.json();
}

// ===== Slice 025 — auditor period =====

export type AuditPeriod = {
  audit_period_id: string;
  name: string;
  framework_version_id: string;
  period_start: string;
  period_end: string;
  status: string;
  frozen_at?: string | null;
  granted_at: string;
  granted_by: string;
};

export async function getAuditPeriod(): Promise<AuditPeriod | null> {
  const res = await fetch(`/api/audit/period`);
  if (res.status === 404) return null;
  if (!res.ok) {
    throw new AuditAPIError(res.status, `${res.status} ${res.statusText}`);
  }
  const body = (await res.json()) as { audit_period: AuditPeriod };
  return body.audit_period;
}

export async function listAuditPeriods(): Promise<AuditPeriod[]> {
  const body = (await bffFetch(`/api/audit/periods`)) as {
    audit_periods: AuditPeriod[];
  };
  return body.audit_periods ?? [];
}

// ===== Slice 026 — populations, samples, annotations =====

export type Population = {
  id: string;
  control_id: string;
  scope_predicate: unknown;
  time_window_start: string;
  time_window_end: string;
  frozen_at?: string | null;
  row_count: number;
  created_by: string;
  created_at: string;
};

export type CreatePopulationRequest = {
  control_id: string;
  scope_predicate?: unknown;
  time_window_start: string;
  time_window_end: string;
};

export async function createPopulation(
  body: CreatePopulationRequest,
): Promise<Population> {
  const res = (await bffFetch(`/api/audit/populations`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })) as { population: Population };
  return res.population;
}

export async function getPopulation(id: string): Promise<Population> {
  const res = (await bffFetch(
    `/api/audit/populations/${encodeURIComponent(id)}`,
  )) as { population: Population };
  return res.population;
}

export type Sample = {
  id: string;
  population_id: string;
  n: number;
  seed: string;
  created_by: string;
  created_at: string;
  evidence_record_ids: string[];
};

export type DrawSampleRequest = {
  population_id: string;
  n: number;
  seed: string;
};

export async function drawSample(body: DrawSampleRequest): Promise<Sample> {
  const res = (await bffFetch(`/api/audit/samples`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })) as { sample: Sample };
  return res.sample;
}

export async function getSample(id: string): Promise<Sample> {
  const res = (await bffFetch(
    `/api/audit/samples/${encodeURIComponent(id)}`,
  )) as { sample: Sample };
  return res.sample;
}

export type AnnotationResult = "passed" | "failed" | "not-applicable";

export type Annotation = {
  id: string;
  sample_id: string;
  evidence_record_id: string;
  result: AnnotationResult;
  annotated_by: string;
  annotated_at: string;
  notes: string;
};

export type AnnotateRequest = {
  evidence_record_id: string;
  result: AnnotationResult;
  notes: string;
};

export async function annotateSample(
  sampleID: string,
  body: AnnotateRequest,
): Promise<Annotation> {
  const res = (await bffFetch(
    `/api/audit/samples/${encodeURIComponent(sampleID)}/annotations`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  )) as { annotation: Annotation };
  return res.annotation;
}

export async function listAnnotations(sampleID: string): Promise<Annotation[]> {
  const res = (await bffFetch(
    `/api/audit/samples/${encodeURIComponent(sampleID)}/annotations`,
  )) as { annotations: Annotation[] };
  return res.annotations ?? [];
}

// ===== Slice 027 — walkthroughs =====

export type WalkthroughAttachment = {
  id: string;
  storage_key: string;
  content_type: string;
  size_bytes: number;
  sha256: string;
  uploaded_by: string;
  uploaded_at: string;
};

export type Walkthrough = {
  id: string;
  audit_period_id?: string;
  control_id: string;
  narrative: string;
  transcript?: string;
  status: string;
  canonical_hash: string;
  created_by: string;
  created_at: string;
  updated_at: string;
  attachments?: WalkthroughAttachment[];
  tamper_detected: boolean;
};

export type CreateWalkthroughRequest = {
  control_id: string;
  audit_period_id?: string;
  narrative: string;
  transcript?: string;
};

export async function createWalkthrough(
  body: CreateWalkthroughRequest,
): Promise<Walkthrough> {
  const res = (await bffFetch(`/api/audit/walkthroughs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })) as { walkthrough: Walkthrough };
  return res.walkthrough;
}

export async function getWalkthrough(id: string): Promise<Walkthrough> {
  const res = (await bffFetch(
    `/api/audit/walkthroughs/${encodeURIComponent(id)}`,
  )) as { walkthrough: Walkthrough };
  return res.walkthrough;
}

export async function uploadWalkthroughAttachment(
  walkthroughID: string,
  file: File,
  annotations?: string,
): Promise<Walkthrough> {
  const form = new FormData();
  form.append("file", file);
  if (annotations) form.append("annotations", annotations);
  const res = await fetch(
    `/api/audit/walkthroughs/${encodeURIComponent(walkthroughID)}/attachments`,
    { method: "POST", body: form },
  );
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // not JSON
    }
    throw new AuditAPIError(res.status, msg);
  }
  const body = (await res.json()) as { walkthrough: Walkthrough };
  return body.walkthrough;
}

// ===== Slice 029 — Audit Hub threaded comments + notifications =====

export type NoteVisibility = "auditor_only" | "shared";
export type NoteScopeType =
  | "control"
  | "finding"
  | "sample"
  | "period"
  | "walkthrough";

export type AuditNote = {
  id: string;
  audit_period_id: string;
  author_user_id: string;
  scope_type: NoteScopeType;
  scope_id?: string;
  body: string;
  visibility: NoteVisibility;
  parent_note_id?: string;
  depth?: number;
  created_at: string;
  updated_at: string;
};

export type CreateNoteRequest = {
  audit_period_id: string;
  scope_type: NoteScopeType;
  scope_id?: string;
  body: string;
  visibility: NoteVisibility;
  parent_note_id?: string;
};

// getNoteThread returns the visible thread for a (scope_type, scope_id,
// period) anchor. P0-2: the platform filters `auditor_only` notes to
// their author server-side — the UI NEVER client-side-filters visibility.
export async function getNoteThread(params: {
  audit_period_id: string;
  scope_type: NoteScopeType;
  scope_id?: string;
}): Promise<AuditNote[]> {
  const qs = new URLSearchParams();
  qs.set("audit_period_id", params.audit_period_id);
  qs.set("scope_type", params.scope_type);
  if (params.scope_id) qs.set("scope_id", params.scope_id);
  const res = (await bffFetch(
    `/api/audit/audit-notes/thread?${qs.toString()}`,
  )) as { audit_notes: AuditNote[] };
  return res.audit_notes ?? [];
}

export async function createNote(body: CreateNoteRequest): Promise<AuditNote> {
  const res = (await bffFetch(`/api/audit/audit-notes`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })) as { audit_note: AuditNote };
  return res.audit_note;
}

export type Notification = {
  id: string;
  recipient_user_id: string;
  type: string;
  payload: Record<string, unknown>;
  created_at: string;
  read_at?: string | null;
};

export async function listNotifications(): Promise<{
  notifications: Notification[];
  unread_count: number;
}> {
  const res = (await bffFetch(`/api/audit/notifications`)) as {
    notifications: Notification[];
    unread_count: number;
  };
  return {
    notifications: res.notifications ?? [],
    unread_count: res.unread_count ?? 0,
  };
}
