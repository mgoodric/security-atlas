// #2083 — display labels for the API's enum values.
//
// Every enum value the web app shows goes through `enumLabel`, so a user
// reads "Qualitative 5×5" or "Intersects with", never `qualitative_5x5` or
// `intersects_with`.
//
// ENUM_LABELS is keyed by the Postgres enum's sqlc type name, the same name
// `internal/db/dbx/models.go` declares (`type RiskMethodology string`).
// enum-labels.test.ts reads that generated file and fails when any of its
// enum values has no label here, so a migration that adds a value cannot
// ship it to the UI as raw text.
//
// Labels are sentence case. A value outside the map (a future value the web
// app has not caught up with) falls back to `humanizeEnum`, which still
// reads better than the raw identifier.

export const ENUM_LABELS = {
  ControlImplementationType: {
    automated: "Automated",
    semi_automated: "Semi-automated",
    manual_attested: "Manually attested",
    manual_periodic: "Manual, periodic",
  },
  ControlLifecycleState: {
    proposed: "Proposed",
    draft: "Draft",
    active: "Active",
    deprecated: "Deprecated",
    retired: "Retired",
  },
  CrosswalkMappingTier: {
    draft: "Draft",
    under_review: "Under review",
    verified: "Verified",
    rejected: "Rejected",
  },
  CrosswalkSourceAttribution: {
    scf_official: "SCF official",
    community_draft: "Community draft",
    org_internal: "Organisation internal",
  },
  CsfProfileKind: {
    current: "Current profile",
    target: "Target profile",
  },
  CsfTier: {
    tier1_partial: "Tier 1: Partial",
    tier2_risk_informed: "Tier 2: Risk informed",
    tier3_repeatable: "Tier 3: Repeatable",
    tier4_adaptive: "Tier 4: Adaptive",
  },
  DecisionStatus: {
    active: "Active",
    revisited: "Revisited",
    superseded: "Superseded",
    expired: "Expired",
  },
  EvidenceFreshnessClass: {
    realtime: "Real time",
    daily: "Daily",
    weekly: "Weekly",
    monthly: "Monthly",
    quarterly: "Quarterly",
    annual: "Annual",
  },
  EvidenceResult: {
    pass: "Pass",
    fail: "Fail",
    inconclusive: "Inconclusive",
    na: "Not applicable",
    excepted: "Excepted",
  },
  FrameworkVersionAuditAction: {
    promote: "Promoted",
    revert: "Reverted",
    migration_approve: "Migration approved",
    migration_reject: "Migration rejected",
  },
  FrameworkVersionMigrationMatchKind: {
    exact_code: "Same requirement code",
    added: "Added",
    removed: "Removed",
  },
  FrameworkVersionMigrationStatus: {
    pending: "Pending",
    approved: "Approved",
    rejected: "Rejected",
  },
  FrameworkVersionStatus: {
    current: "Current",
    legacy: "Legacy",
    withdrawn: "Withdrawn",
  },
  PolicyStatus: {
    draft: "Draft",
    under_review: "Under review",
    approved: "Approved",
    published: "Published",
    superseded: "Superseded",
  },
  RiskAcceptanceStatus: {
    proposed: "Proposed",
    approved: "Approved",
  },
  // #2107: the risk lifecycle.
  RiskStatus: {
    open: "Open",
    closed: "Closed",
  },
  RiskClosureReason: {
    not_applicable: "No longer applicable",
    merged: "Merged into another risk",
    realised_and_handled: "Realised and handled",
  },
  RiskLevel: {
    company: "Company",
    org: "Organisation",
    team: "Team",
  },
  RiskMethodology: {
    nist_800_30: "NIST SP 800-30",
    qualitative_5x5: "Qualitative 5×5",
    fair: "FAIR",
    cis_ram: "CIS RAM",
    iso_27005: "ISO/IEC 27005",
  },
  RiskTreatment: {
    untreated: "Untreated",
    mitigate: "Mitigate",
    transfer: "Transfer",
    accept: "Accept",
    avoid: "Avoid",
  },
  ScopeDataClassification: {
    public: "Public",
    internal: "Internal",
    confidential: "Confidential",
    restricted: "Restricted",
  },
  ScopeEnvironment: {
    prod: "Production",
    staging: "Staging",
    dev: "Development",
    sandbox: "Sandbox",
  },
  StrmRelationshipType: {
    equal: "Equal",
    subset_of: "Subset of",
    superset_of: "Superset of",
    intersects_with: "Intersects with",
    no_relationship: "No relationship",
  },
  VendorBillingCadence: {
    monthly: "Monthly",
    quarterly: "Quarterly",
    annual: "Annual",
    multi_year: "Multi-year",
    one_time: "One-time",
  },
  VendorCommercialStatus: {
    trialing: "Trialing",
    active: "Active",
    churned: "Churned",
  },
  VendorCriticality: {
    low: "Low",
    medium: "Medium",
    high: "High",
  },
  VendorReviewCadence: {
    monthly: "Monthly",
    quarterly: "Quarterly",
    biannual: "Twice a year",
    annual: "Annual",
  },
  VendorReviewOutcome: {
    pass: "Pass",
    pass_with_findings: "Pass with findings",
    fail: "Fail",
    waived: "Waived",
  },
  VendorToolCategory: {
    appsec: "Application security",
    cloud_security: "Cloud security",
    edr: "Endpoint detection and response",
    grc: "GRC",
    iam: "Identity and access management",
    monitoring: "Monitoring",
    siem: "SIEM",
    vuln_mgmt: "Vulnerability management",
    other: "Other",
  },

  // A control's rolled-up evaluation state and evidence freshness, as the
  // controls list API reports them. These are computed by the API, not
  // stored as a Postgres enum, so the coverage test does not check them.
  // #2131: the keys are the values the rollup produces (evidence_result,
  // and the freshness ranks), because the list now filters on the server
  // and refuses any other value. `insufficient_evidence` and
  // `not_applicable` were never produced.
  EvaluationState: {
    pass: "Pass",
    fail: "Fail",
    inconclusive: "Inconclusive",
    excepted: "Excepted",
    na: "Not applicable",
  },
  FreshnessState: {
    fresh: "Fresh",
    stale: "Stale",
    expired: "Expired",
    no_evidence: "No evidence",
  },
  // Statuses held in CHECK-constrained text columns, not Postgres enums.
  // enum-labels.test.ts reads their values from check-enums.golden.json,
  // which internal/db/check_enums_integration_test.go records from the
  // migrated database. AuditPeriodStatus also labels the statuses the
  // audits page offers ahead of the constraint.
  ActionPlanStatus: {
    draft: "Draft",
    in_progress: "In progress",
    blocked: "Blocked",
    completed: "Completed",
    verified: "Verified",
  },
  AuditPeriodStatus: {
    planned: "Planned",
    open: "Open",
    in_progress: "In progress",
    frozen: "Frozen",
    closed: "Closed",
  },
  // The Activity page's record kinds (AUDIT_LOG_KINDS in lib/api/audit-log.ts).
  ActivityKind: {
    decision: "Decision",
    evidence: "Evidence",
    exception: "Exception",
    sample: "Audit sample",
    audit_period: "Audit period",
    aggregation_rule: "Aggregation rule",
    feature_flag: "Feature flag",
    me: "Account settings",
    walkthrough: "Walkthrough",
    framework_scope: "Framework scope",
    taxonomy: "Taxonomy",
    mapping_overlay: "Mapping overlay",
    control_import: "Control import",
    risk: "Risk",
    posture_target: "Posture target",
    module_access: "Module access",
  },
  // Checklist citation kinds: the CitationKind constants in
  // internal/checklist/checklist.go, which the coverage test reads.
  ChecklistItemKind: {
    control: "Control",
    scf_anchor: "SCF anchor",
    policy: "Policy",
  },
  // Feature-flag categories: the `Category` strings in
  // internal/featureflag/seed.go, which the coverage test also reads.
  FeatureCategory: {
    audit: "Audit",
    board: "Board reporting",
    controls: "Controls",
    integrations: "Integrations",
    personnel: "Personnel security",
    policy: "Policies",
    questionnaire: "Questionnaires",
    risk: "Risk",
    vendor: "Vendors",
    uncategorized: "Other",
  },
} as const satisfies Record<string, Record<string, string>>;

export type EnumName = keyof typeof ENUM_LABELS;

// humanizeEnum turns an unmapped identifier into readable text:
// `semi_automated` → "Semi automated". It is the fallback, not the policy;
// a value the UI shows belongs in ENUM_LABELS.
export function humanizeEnum(value: string): string {
  const spaced = value.replace(/[_-]+/g, " ").trim();
  if (spaced === "") return value;
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

// enumLabel returns the display label for one value of one enum. Empty or
// missing values render as an em dash so a blank cell reads as "none".
export function enumLabel(
  name: EnumName,
  value: string | null | undefined,
): string {
  if (value === null || value === undefined || value === "") return "—";
  return lookupLabel(name, value) ?? humanizeEnum(value);
}

// lookupLabel returns the label for a value, or undefined when the enum
// has none. It reads own keys only, so a value such as `constructor`
// cannot resolve to an Object.prototype member.
export function lookupLabel(name: EnumName, value: string): string | undefined {
  const labels: Record<string, string> = ENUM_LABELS[name];
  return Object.hasOwn(labels, value) ? labels[value] : undefined;
}

// enumOptions lists an enum's values as select options, in the order the
// label map declares them (the order the form should offer them).
export function enumOptions(
  name: EnumName,
): { value: string; label: string }[] {
  return Object.entries(ENUM_LABELS[name]).map(([value, label]) => ({
    value,
    label,
  }));
}
