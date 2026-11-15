// #2117 — pure helpers for the /audits/[id] detail page, kept out of the
// component so vitest covers them without a DOM.

import type { AuditPeriod as WorkspacePeriod } from "@/lib/api/audit";
import type {
  AuditPeriod,
  ControlStateResponse,
} from "@/lib/api/audit-periods";
import { APIError } from "@/lib/api/base";

/** The freeze confirm's prompt. Freezing is irreversible, and the text
 *  says so before the operator commits. */
export const FREEZE_CONFIRM_PROMPT =
  "Freeze this audit period? This can't be undone.";

export const FREEZE_CONFIRM_DETAIL =
  "The period's evidence becomes immutable: samples draw only from evidence observed up to this moment, and the period can't be unfrozen or edited. A hash of the frozen state is recorded. Live work outside the audit carries on as normal.";

/** The platform's 404 body for a period that is not there (or is
 *  another tenant's). Any other 404 from the export route means the
 *  route itself is not mounted: the OSCAL bridge isn't running. */
const PERIOD_NOT_FOUND = "audit period not found";

export const OSCAL_UNAVAILABLE_MESSAGE =
  "OSCAL export isn't available on this deployment: the OSCAL bridge isn't running.";

export type ControlStateSummary = {
  count: number;
  latestResult: string | null;
  latestObservedAt: string | null;
};

/** Rolls the horizon read for one control into what its row shows: how
 *  many observations fall inside the period, and the latest one. */
export function summarizeControlState(
  resp: ControlStateResponse,
): ControlStateSummary {
  const obs = resp.observations ?? [];
  let latest: (typeof obs)[number] | null = null;
  for (const o of obs) {
    if (!latest || o.observed_at > latest.observed_at) latest = o;
  }
  return {
    count: obs.length,
    latestResult: latest?.result ?? null,
    latestObservedAt: latest?.observed_at ?? null,
  };
}

/** The toast for a failed freeze. The platform owns the role decision;
 *  the page only says what it answered. */
export function freezeErrorMessage(err: unknown): string {
  if (err instanceof APIError) {
    if (err.status === 403) {
      return "You don't have permission to freeze this audit period.";
    }
    if (err.status === 409) {
      return "This audit period is already frozen.";
    }
    return `Could not freeze the audit period: ${err.message}`;
  }
  return "Could not freeze the audit period.";
}

/** True when an export failure means the OSCAL bridge is absent rather
 *  than the export being refused for this period. */
export function isOscalBridgeUnavailable(err: unknown): boolean {
  if (!(err instanceof APIError)) return false;
  if (err.status === 404) return err.message !== PERIOD_NOT_FOUND;
  return err.status === 502 || err.status === 503;
}

export function oscalExportErrorMessage(err: unknown): string {
  if (isOscalBridgeUnavailable(err)) return OSCAL_UNAVAILABLE_MESSAGE;
  if (err instanceof APIError) {
    return `Could not export the OSCAL bundle: ${err.message}`;
  }
  return "Could not export the OSCAL bundle.";
}

/** The period in the shape the audit workspace's ControlWorkspace takes,
 *  so populations, samples and notes reuse that component. The grant
 *  fields describe an auditor assignment, which a GRC engineer viewing
 *  the period does not have; they are left empty. */
export function toWorkspacePeriod(p: AuditPeriod): WorkspacePeriod {
  return {
    audit_period_id: p.id,
    name: p.name,
    framework_version_id: p.framework_version_id,
    period_start: p.period_start,
    period_end: p.period_end,
    status: p.status,
    frozen_at: p.frozen_at ?? null,
    granted_at: "",
    granted_by: "",
  };
}
