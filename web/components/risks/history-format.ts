// #2104 — the risk History section's text, from GET /v1/risks/{id}/audit-log.
//
// Pure, so vitest pins how each entry reads. An entry becomes one line per
// changed field: "<field>: <before> -> <after>". For fields_changed the
// before/after objects already hold only the changed fields; placement
// and category entries carry their whole small state, so unchanged keys
// (and the category id behind the slug) are dropped.
//
// #2138: a field whose value is an id (a person, a control, an org unit)
// carries `ref`, and its before/after stay the ids (or "none"); the
// section resolves them to a name or title, so no id is ever shown.

import type { RiskAuditEntry } from "@/lib/api/risks";
import { enumLabel, humanizeEnum } from "@/lib/labels/enum-labels";

export type HistoryRef = "person" | "control" | "org_unit";

export type HistoryChange = {
  field: string;
  before: string;
  after: string;
  ref?: HistoryRef;
};

// The fields whose values are ids, and what they name.
const REF_FIELDS: Record<string, HistoryRef> = {
  owner_user_id: "person",
  proposed_by: "person",
  accepter: "person",
  closed_by: "person",
  control_id: "control",
  org_unit_id: "org_unit",
};

const FIELD_LABELS: Record<string, string> = {
  title: "Title",
  description: "Description",
  methodology: "Methodology",
  inherent_score: "Inherent score",
  residual_score: "Residual score",
  treatment_owner: "Owner (unlinked)",
  owner_user_id: "Owner",
  review_due_at: "Review due",
  instrument_reference: "Instrument reference",
  category: "Category",
  level: "Level",
  org_unit_id: "Org unit",
  // #2105: treatment and acceptance transitions.
  treatment: "Treatment",
  acceptance_status: "Acceptance",
  justification: "Justification",
  proposed_by: "Proposed by",
  proposed_until: "Proposed expiry",
  accepted_until: "Accepted until",
  accepter: "Approved by",
  self_approved: "Self-approved",
  reason: "Reason",
  // #2107: the open/closed lifecycle.
  status: "Status",
  closure_reason: "Closure reason",
  closure_note: "Closure note",
  closed_by: "Closed by",
  closed_at: "Closed at",
  // #2106: control links.
  control_id: "Control",
};

// Keys an entry carries that are not a change a reader needs to see.
const HIDDEN_KEYS = new Set(["category_id"]);

export function historyActionLabel(action: string): string {
  switch (action) {
    case "fields_changed":
      return "Edited";
    case "category_changed":
      return "Category changed";
    case "placement_changed":
      return "Moved";
    case "treatment_changed":
      return "Treatment changed";
    case "acceptance_proposed":
      return "Acceptance proposed";
    case "acceptance_approved":
      return "Acceptance approved";
    case "acceptance_rejected":
      return "Acceptance rejected";
    case "acceptance_revoked":
      return "Acceptance revoked";
    case "closed":
      return "Closed";
    case "reopened":
      return "Reopened";
    case "control_linked":
      return "Control linked";
    case "control_unlinked":
      return "Control unlinked";
    default:
      return humanizeEnum(action);
  }
}

/** One value as History shows it. */
export function formatHistoryValue(field: string, v: unknown): string {
  if (v === null || v === undefined || v === "") return "none";
  if (field === "review_due_at" && typeof v === "string") {
    const d = new Date(v);
    return Number.isNaN(d.getTime()) ? v : d.toISOString().slice(0, 10);
  }
  if (field === "methodology" && typeof v === "string") {
    return enumLabel("RiskMethodology", v);
  }
  if (field === "level" && typeof v === "string") {
    return enumLabel("RiskLevel", v);
  }
  if (field === "treatment" && typeof v === "string") {
    return enumLabel("RiskTreatment", v);
  }
  if (field === "acceptance_status" && typeof v === "string") {
    return enumLabel("RiskAcceptanceStatus", v);
  }
  if (field === "status" && typeof v === "string") {
    return enumLabel("RiskStatus", v);
  }
  if (field === "closure_reason" && typeof v === "string") {
    return enumLabel("RiskClosureReason", v);
  }
  // Only a self-approval is worth a line; false reads as no change.
  if (field === "self_approved") return v === true ? "yes" : "none";
  if (typeof v === "string") return v;
  if (typeof v === "object") {
    const o = v as Record<string, unknown>;
    if (typeof o.likelihood === "number" && typeof o.impact === "number") {
      return `likelihood ${o.likelihood} × impact ${o.impact}`;
    }
    if (Object.keys(o).length === 0) return "pending evaluation";
    return JSON.stringify(o);
  }
  return String(v);
}

/** The changed fields of one audit entry, in a stable order. */
export function historyChanges(entry: RiskAuditEntry): HistoryChange[] {
  const before = entry.before ?? {};
  const after = entry.after ?? {};
  const keys = Array.from(
    new Set([...Object.keys(before), ...Object.keys(after)]),
  ).filter((k) => !HIDDEN_KEYS.has(k));
  const order = Object.keys(FIELD_LABELS);
  keys.sort((a, b) => {
    const ia = order.indexOf(a);
    const ib = order.indexOf(b);
    return (ia < 0 ? order.length : ia) - (ib < 0 ? order.length : ib);
  });
  const out: HistoryChange[] = [];
  for (const k of keys) {
    const b = formatHistoryValue(k, before[k]);
    const a = formatHistoryValue(k, after[k]);
    if (a === b) continue;
    const field = FIELD_LABELS[k] ?? humanizeEnum(k);
    const ref = REF_FIELDS[k];
    out.push(
      ref
        ? { field, before: b, after: a, ref }
        : { field, before: b, after: a },
    );
  }
  return out;
}
