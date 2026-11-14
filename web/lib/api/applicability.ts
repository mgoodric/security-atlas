// #1794 — client shapes and pure helpers for the Statement of Applicability
// tab on the framework-scope page. The types mirror `requirementWire`,
// `countsWire` and `importRejectionWire` in
// internal/api/frameworkscopes/applicability*.go. The browser reaches the
// platform only through the /api/framework-scopes BFF routes.

import type { FrameworkScope } from "./framework-scopes";

export type ApplicabilityDecision = "applicable" | "excluded";

export type RequirementApplicability = {
  requirement_id: string;
  code: string;
  title: string;
  decision: ApplicabilityDecision | null;
  undecided: boolean;
  justification: string;
  implementation_ref: string;
  updated_by?: string;
  updated_at?: string;
  contradiction: boolean;
};

export type ApplicabilityCounts = {
  total: number;
  applicable: number;
  excluded: number;
  undecided: number;
  contradictions: number;
};

export type ApplicabilityList = {
  framework_version_id: string;
  framework_scope: FrameworkScope | null;
  requirements: RequirementApplicability[];
  contradictions_evaluated_against: string;
  counts: ApplicabilityCounts;
};

export type ImportRejection = {
  line: number;
  requirement_code: string;
  reason: string;
};

export type ImportResult = {
  applied: number;
  rejected: ImportRejection[];
  approval_invalidated: boolean;
};

// The tab's filters. `all` sends no filter; the counts always describe the
// whole revision whatever the filter.
export const SOA_FILTERS = [
  "all",
  "applicable",
  "excluded",
  "undecided",
  "contradiction",
] as const;
export type SoAFilter = (typeof SOA_FILTERS)[number];

// applicabilityQuery maps a tab filter onto the platform's
// ?decision= / ?contradiction= query string (leading "?" included, or "").
export function applicabilityQuery(filter: SoAFilter): string {
  switch (filter) {
    case "all":
      return "";
    case "contradiction":
      return "?contradiction=true";
    default:
      return `?decision=${filter}`;
  }
}

export const SOA_EXPORT_FORMATS = ["csv", "xlsx"] as const;
export type SoAExportFormat = (typeof SOA_EXPORT_FORMATS)[number];

// buildSoAExportURL is the BFF URL a download link points at. Pinning
// `revision` exports the revision on screen rather than the activated one.
export function buildSoAExportURL(
  frameworkVersionID: string,
  format: SoAExportFormat,
  revisionID?: string,
): string {
  const q = new URLSearchParams();
  q.set("framework_version", frameworkVersionID);
  q.set("format", format);
  if (revisionID) q.set("revision", revisionID);
  return `/api/framework-scopes/applicability/export?${q.toString()}`;
}

// The catalog slug of ISO/IEC 27001 (isoSlug in
// internal/frameworkscope/soa_export.go). Its SoA carries the title
// auditors expect; every other framework's is "Applicability".
const ISO_27001_SLUG = "iso27001";

export function soaTitle(frameworkSlug: string | undefined): string {
  return frameworkSlug === ISO_27001_SLUG
    ? "Statement of Applicability"
    : "Applicability";
}

// isFrozen: decisions on an activated or superseded revision cannot change
// (framework_scope_applicability_guard rejects the write), so the tab
// renders them read-only.
export function isFrozen(scope: Pick<FrameworkScope, "state">): boolean {
  return scope.state === "activated" || scope.state === "superseded";
}

// defaultRevision picks the revision the tab opens on: the newest one still
// being edited (draft, review or approved), else the activated one, else
// the first listed.
export function defaultRevision(
  scopes: FrameworkScope[],
): FrameworkScope | undefined {
  const open = scopes
    .filter((s) => !isFrozen(s))
    .sort((a, b) => b.created_at.localeCompare(a.created_at));
  return open[0] ?? scopes.find((s) => s.state === "activated") ?? scopes[0];
}

// #1795 — the applicability diff a revision in review is approved against.
// Mirrors `decisionChangeWire` in internal/api/frameworkscopes/applicability.go.
export type ApplicabilityChangeKind =
  | "added"
  | "removed"
  | "decision_changed"
  | "justification_changed";

export type DecisionSide = {
  decision: ApplicabilityDecision;
  justification: string;
  implementation_ref: string;
};

export type ApplicabilityChange = {
  requirement_id: string;
  code: string;
  title: string;
  change: ApplicabilityChangeKind;
  before: DecisionSide | null;
  after: DecisionSide | null;
};

export type ApplicabilityDiff = {
  framework_scope: FrameworkScope;
  // The activated revision compared against; null when none is.
  baseline: FrameworkScope | null;
  predicate: { changed: boolean; before: unknown; after: unknown };
  changes: ApplicabilityChange[];
};

export const CHANGE_LABELS: Record<ApplicabilityChangeKind, string> = {
  added: "Added",
  removed: "Removed",
  decision_changed: "Decision changed",
  justification_changed: "Justification changed",
};

// summarizeChanges tallies a diff by kind, for the review panel's header.
export function summarizeChanges(
  changes: ApplicabilityChange[],
): Record<ApplicabilityChangeKind, number> {
  const out: Record<ApplicabilityChangeKind, number> = {
    added: 0,
    removed: 0,
    decision_changed: 0,
    justification_changed: 0,
  };
  for (const c of changes) out[c.change]++;
  return out;
}

// describeSide renders one side of a change: "excluded — <justification>",
// or "undecided" when the requirement had no decision on that side.
export function describeSide(side: DecisionSide | null): string {
  if (!side) return "undecided";
  return side.justification
    ? `${side.decision} — ${side.justification}`
    : side.decision;
}
