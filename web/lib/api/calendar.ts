// Slice 370 — compliance calendar (slice 094), extracted from the former
// `web/lib/api.ts` god-file.

import { apiBaseURL, APIError } from "./base";
import { apiFetch, bffControlFetch } from "./_shared";

// ===== Slice 094 — Compliance calendar =====
//
// Read-only aggregation across audit_periods + exceptions + policies +
// controls (with cadence math). Plus a per-user ICS URL token mint.
// See docs/audit-log/094-compliance-calendar-decisions.md.

export type CalendarEventType = "audit" | "exception" | "policy" | "control";

export type CalendarEvent = {
  id: string;
  type: CalendarEventType;
  title: string;
  starts_at: string; // RFC 3339
  ends_at?: string;
  related_entity_id: string;
  related_entity_kind: string;
  summary: string;
  status: string;
  cadence?: string;
};

export type CalendarResponse = {
  events: CalendarEvent[];
  count: number;
  from: string;
  to: string;
  truncated: boolean;
  next_from?: string;
};

export type CalendarSubscriptionResponse = {
  url: string;
  expires_at: string;
};

// Server-side fn: hit the platform with the bearer.
export async function getCalendarEvents(
  bearer: string,
  params: { from?: string; to?: string; types?: string } = {},
): Promise<CalendarResponse> {
  const qp = new URLSearchParams();
  if (params.from) qp.set("from", params.from);
  if (params.to) qp.set("to", params.to);
  if (params.types) qp.set("types", params.types);
  const suffix = qp.toString() ? `?${qp.toString()}` : "";
  const res = await apiFetch(`/v1/calendar${suffix}`, bearer);
  return (await res.json()) as CalendarResponse;
}

export async function postCalendarSubscription(
  bearer: string,
): Promise<CalendarSubscriptionResponse> {
  const url = `${apiBaseURL()}/v1/calendar/subscription`;
  const res = await fetch(url, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
  });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as CalendarSubscriptionResponse;
}

// Browser-side fn: hit the BFF.
export function fetchCalendarEvents(params: {
  from?: string;
  to?: string;
  types?: string;
}): Promise<CalendarResponse> {
  const qp = new URLSearchParams();
  if (params.from) qp.set("from", params.from);
  if (params.to) qp.set("to", params.to);
  if (params.types) qp.set("types", params.types);
  const suffix = qp.toString() ? `?${qp.toString()}` : "";
  return bffControlFetch<CalendarResponse>(`/api/calendar${suffix}`);
}

export async function createCalendarSubscription(): Promise<CalendarSubscriptionResponse> {
  const res = await fetch(`/api/calendar/subscription`, { method: "POST" });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON */
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as CalendarSubscriptionResponse;
}
