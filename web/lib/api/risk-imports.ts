// #2847 — wire types and pure view helpers for the risk import review
// page. The types mirror internal/risk/riskimport (Batch, BatchRow,
// RejectedRow, RowDecision, ApproveResult); the helpers are node-testable
// so the page stays a renderer.

export type RowStatus = "pending" | "approved" | "rejected" | "duplicate";

export const ROW_STATUSES: readonly RowStatus[] = [
  "pending",
  "approved",
  "rejected",
  "duplicate",
];

export const ROW_STATUS_LABELS: Record<RowStatus, string> = {
  pending: "Pending",
  approved: "Approved",
  rejected: "Rejected",
  duplicate: "Duplicate",
};

// The review flags a staged row can carry (risk_import_rows_flags_chk).
export type RowFlag =
  | "owner_unlinked"
  | "treatment_downgraded"
  | "parent_link_failed";

export const FLAG_LABELS: Record<RowFlag, string> = {
  owner_unlinked: "Owner not linked to a user",
  treatment_downgraded: "Treatment downgraded to untreated",
  parent_link_failed: "Parent link failed",
};

/** flagLabel names a flag; one this client does not know shows as sent. */
export function flagLabel(flag: string): string {
  return FLAG_LABELS[flag as RowFlag] ?? flag;
}

// The reasons the upload refuses a row (riskimport.Reject*).
export const REJECT_REASON_LABELS: Record<string, string> = {
  missing_required_field: "Missing required field",
  duplicate_external_ref: "Risk ID repeated in the file",
  invalid_external_ref: "Invalid risk ID",
  unknown_category: "Unknown category",
  invalid_review_due: "Invalid review date",
  invalid_treatment: "Unknown treatment",
  invalid_acceptance: "Acceptance missing its justification or until-date",
  invalid_level: "Unknown level",
  unknown_org_unit: "Unknown org unit",
  ambiguous_org_unit: "Org unit name shared by more than one unit",
  level_mismatch: "Level differs from the org unit's level",
};

export function rejectReasonLabel(reason: string): string {
  return REJECT_REASON_LABELS[reason] ?? reason;
}

export type BatchRow = {
  id: string;
  row_number: number;
  external_ref?: string;
  title: string;
  description: string;
  // #2844: the row's category cell and theme values as written, and the
  // category slug and theme names the batch maps them to. category is
  // empty while its value is unmapped; themes lists only mapped values.
  category_value?: string;
  category: string;
  theme_values?: string[] | null;
  themes?: string[] | null;
  review_due?: string;
  // #2842: the treatment the risk is created with, and the register's when
  // it was downgraded to untreated. An accepted row is created proposed;
  // historical_accepter is the register's, never the risk's accepter.
  treatment?: string;
  requested_treatment?: string;
  justification?: string;
  proposed_until?: string;
  historical_accepter?: string;
  instrument_reference?: string;
  linked_control_ids?: string[] | null;
  unresolved_controls?: string[] | null;
  // #2843: the active user the owner cell matched by email, or the cell as
  // the unlinked owner when none matched. The page names the user through
  // <Person> (/v1/people); the row carries only the id.
  owner_user_id?: string;
  owner_unlinked?: string;
  // #2845: the level the row names (absent when none), and the org unit
  // cell with the tenant org unit it matched by name. With a unit and no
  // level the risk takes the unit's level; with neither it is a team risk.
  level?: string;
  org_unit_value?: string;
  org_unit_id?: string;
  // #2846: the row's parent-risk-ID cell and, once approved, how linking
  // its risk under that parent went: none, waiting on a pending parent row
  // of any import of the organisation (#2957), linked (parent_risk_id), or
  // failed (parent_link_error, with the parent_link_failed flag). The risk
  // imports either way.
  parent_ref?: string;
  parent_link_status?: string;
  parent_link_error?: string;
  parent_risk_id?: string;
  // #2957: the organisation's risks the export's parent_risk_ids named,
  // staged only when parent_ref is blank; the risk is linked under each.
  parent_risk_ids?: string[];
  unmapped: Record<string, string> | null;
  flags: string[] | null;
  status: RowStatus | string;
  decided_by?: string;
  risk_id?: string;
  rejection_reason?: string;
};

export type ValueMapping = {
  kind: "category" | "theme";
  value: string;
  row_count: number;
  target?: string;
  mapped_by?: string;
};

export type RejectedRow = {
  row_number: number;
  external_ref?: string;
  reason: string;
  detail: string;
};

export type RiskImportBatch = {
  id: string;
  source_filename: string;
  source_format: string;
  uploaded_by: string;
  // #2881: the users(id) of the uploading person, absent for a machine
  // upload. That person never decides the batch's rows.
  uploader_user_id?: string;
  // #2881: whether the caller may approve or reject the batch's rows: an
  // admin or approver (never a GRC engineer) who did not upload it.
  can_decide?: boolean;
  status: string;
  total_rows: number;
  staged_count: number;
  rejected_count: number;
  unmapped_columns: string[] | null;
  // #2844: the batch's distinct category and theme values, each with the
  // category slug or theme name it maps to (absent while unmapped).
  category_values?: ValueMapping[] | null;
  theme_values?: ValueMapping[] | null;
  created_at: string;
  staged: BatchRow[] | null;
  rejected: RejectedRow[] | null;
};

export type RowDecision = {
  batch_id: string;
  row_id: string;
  row_number: number;
  external_ref?: string;
  status: RowStatus | string;
  decided_by: string;
  risk_id?: string;
  rejection_reason?: string;
};

export type ApproveResult = {
  batch_id: string;
  approved_count: number;
  approved: RowDecision[] | null;
  duplicates: RowDecision[] | null;
  // #2844: rows approval rejected for an unmapped category or theme value.
  rejected?: RowDecision[] | null;
  skipped:
    | {
        row_id: string;
        row_number: number;
        external_ref?: string;
        reason: string;
      }[]
    | null;
};

export type BatchProgress = {
  staged: number;
  approved: number;
  rejected: number;
  duplicate: number;
  pending: number;
  flagged: number;
  /** Approved rows whose risk waits for its parent's import row to be
   *  decided, in this import or another (#2957). */
  waiting: number;
  /** Rows the upload itself refused (missing title, unknown category, ...). */
  notStaged: number;
};

export function isFlagged(row: BatchRow): boolean {
  return (row.flags ?? []).length > 0;
}

/** batchProgress tallies a batch's staged rows by decision and flags. */
export function batchProgress(batch: RiskImportBatch): BatchProgress {
  const p: BatchProgress = {
    staged: batch.staged_count,
    approved: 0,
    rejected: 0,
    duplicate: 0,
    pending: 0,
    flagged: 0,
    waiting: 0,
    notStaged: batch.rejected_count,
  };
  for (const row of batch.staged ?? []) {
    if (row.status === "approved") p.approved++;
    else if (row.status === "rejected") p.rejected++;
    else if (row.status === "duplicate") p.duplicate++;
    else p.pending++;
    if (isFlagged(row)) p.flagged++;
    if (row.parent_link_status === "waiting") p.waiting++;
  }
  return p;
}

export type RowFilter = { status: RowStatus | ""; flaggedOnly: boolean };

export const NO_FILTER: RowFilter = { status: "", flaggedOnly: false };

/** filterRows keeps the staged rows matching the filter, in row order. */
export function filterRows(
  rows: readonly BatchRow[] | null,
  f: RowFilter,
): BatchRow[] {
  return (rows ?? []).filter(
    (r) =>
      (!f.status || r.status === f.status) && (!f.flaggedOnly || isFlagged(r)),
  );
}

/** unmappedEntries lists a row's unmapped values by header, blanks left
 *  out: an empty cell says nothing the reviewer needs. */
export function unmappedEntries(row: BatchRow): [string, string][] {
  return Object.entries(row.unmapped ?? {})
    .filter(([, v]) => v.trim() !== "")
    .sort(([a], [b]) => a.localeCompare(b));
}

/** A row's mapped fields, labelled, in the order the page shows them.
 *  An unset optional field is left out. */
export function mappedFields(row: BatchRow): [string, string][] {
  const out: [string, string][] = [];
  if (row.external_ref) out.push(["Risk ID", row.external_ref]);
  out.push(["Title", row.title]);
  out.push([
    "Category",
    row.category ||
      (row.category_value ? `${row.category_value} (unmapped)` : ""),
  ]);
  const themeValues = row.theme_values ?? [];
  if (themeValues.length > 0) {
    out.push(["Theme values", themeValues.join(", ")]);
    out.push(["Themes", (row.themes ?? []).join(", ") || "(none mapped)"]);
  }
  if (row.review_due) out.push(["Review due", row.review_due]);
  if (row.description) out.push(["Description", row.description]);
  if (row.owner_user_id) {
    out.push(["Owner", row.owner_user_id]);
  } else if (row.owner_unlinked) {
    out.push(["Owner (unlinked)", row.owner_unlinked]);
  }
  if (row.level) out.push(["Level", row.level]);
  if (row.org_unit_value) {
    out.push(["Org unit", row.org_unit_value]);
  }
  if (row.parent_ref) out.push(["Parent risk ID", parentLinkText(row)]);
  else if (row.parent_risk_ids?.length) {
    out.push(["Parent risks", parentLinkText(row)]);
  }
  if (row.treatment) {
    out.push([
      "Treatment",
      row.requested_treatment
        ? `${row.treatment} (register: ${row.requested_treatment})`
        : row.treatment === "accept"
          ? "accept (proposed)"
          : row.treatment,
    ]);
  }
  if (row.justification) out.push(["Justification", row.justification]);
  if (row.proposed_until) out.push(["Proposed until", row.proposed_until]);
  if (row.historical_accepter) {
    out.push(["Accepter (historical)", row.historical_accepter]);
  }
  if (row.instrument_reference) {
    out.push(["Instrument", row.instrument_reference]);
  }
  if (row.linked_control_ids?.length) {
    out.push(["Linked controls", row.linked_control_ids.join(", ")]);
  }
  if (row.unresolved_controls?.length) {
    out.push(["Unresolved controls", row.unresolved_controls.join(", ")]);
  }
  return out;
}

export type ReviewMode = "decide" | "read-only";

/** reviewMode is what the page lets the caller do. Only a caller the
 *  batch read marks `can_decide` decides: an admin or approver who did not
 *  upload the batch. A GRC engineer and the uploader read (#2881), as does
 *  everyone while the batch is unknown. `/v1/me` is_approver is not used: it
 *  admits a GRC engineer, and knows nothing of the uploader. The platform's
 *  gate is the authority either way: the mode only hides buttons it would
 *  refuse. */
export function reviewMode(
  batch: Pick<RiskImportBatch, "can_decide"> | undefined,
): ReviewMode {
  return batch?.can_decide ? "decide" : "read-only";
}

/** emptyRowsMessage explains an empty row list: no staged rows at all, or
 *  none matching the filter. */
export function emptyRowsMessage(batch: RiskImportBatch, f: RowFilter): string {
  if ((batch.staged ?? []).length === 0) {
    return "No rows were staged from this file.";
  }
  if (f.flaggedOnly && !f.status) return "No staged row is flagged for review.";
  return "No staged rows match these filters.";
}

/** approveAllMessage summarises an approve-all for a toast. */
export function approveAllMessage(r: ApproveResult): string {
  const dup = r.duplicates?.length ?? 0;
  const rejected = r.rejected?.length ?? 0;
  const skipped = r.skipped?.length ?? 0;
  const parts = [
    `Approved ${r.approved_count} ${r.approved_count === 1 ? "row" : "rows"}`,
  ];
  if (dup > 0) parts.push(`${dup} marked duplicate`);
  if (rejected > 0) parts.push(`${rejected} rejected (unmapped value)`);
  if (skipped > 0) parts.push(`${skipped} skipped`);
  return parts.join("; ");
}

/** unmappedValues lists the batch's category and theme values that map to
 *  nothing yet (#2844), categories first. */
export function unmappedValues(batch: RiskImportBatch): ValueMapping[] {
  return [
    ...(batch.category_values ?? []),
    ...(batch.theme_values ?? []),
  ].filter((v) => !v.target);
}

/** rowHasUnmappedValue: the row's category value, or any of its theme
 *  values, maps to nothing. Approval rejects such a row (#2844). */
export function rowHasUnmappedValue(row: BatchRow): boolean {
  if (row.category_value !== undefined && !row.category) return true;
  return (row.themes ?? []).length < (row.theme_values ?? []).length;
}

/** unmappedPendingCount is how many pending rows approval would reject for
 *  an unmapped value. */
export function unmappedPendingCount(batch: RiskImportBatch): number {
  return (batch.staged ?? []).filter(
    (r) => r.status === "pending" && rowHasUnmappedValue(r),
  ).length;
}

/** approveAllWarning names what approve-all would reject: the pending rows
 *  carrying an unmapped value, and those values. Empty when there are none.
 *  A rejection is final; the review page cannot map values yet. */
export function approveAllWarning(batch: RiskImportBatch): string {
  const rows = unmappedPendingCount(batch);
  if (rows === 0) return "";
  const values = unmappedValues(batch).map((v) => `${v.kind} "${v.value}"`);
  return (
    `${rows} pending ${
      rows === 1 ? "row carries" : "rows carry"
    } an unmapped ` +
    `category or theme value and will be rejected, not created` +
    (values.length > 0 ? ` (${values.join(", ")})` : "") +
    `. A rejection is final: map the values first to import ${
      rows === 1 ? "it" : "them"
    }.`
  );
}

export function batchPath(id: string): string {
  return `/api/risks/imports/${encodeURIComponent(id)}`;
}

export function rowPath(batchID: string, rowID: string, verb: string): string {
  return `${batchPath(batchID)}/rows/${encodeURIComponent(rowID)}/${verb}`;
}

export { bffPostJSON as postJSON } from "./_shared";

/** parentLinkText shows a row's parent risk ID, or the parent risks the
 *  export's parent_risk_ids named, with how its link went. */
export function parentLinkText(row: BatchRow): string {
  const ref = row.parent_ref || (row.parent_risk_ids ?? []).join(", ");
  switch (row.parent_link_status) {
    case "linked":
      return `${ref} (linked)`;
    case "waiting":
      return `${ref} (waiting for its import row to be decided)`;
    case "failed":
      return row.parent_link_error
        ? `${ref} (link failed: ${row.parent_link_error})`
        : `${ref} (link failed)`;
    default:
      return ref;
  }
}
