// Slice 100 — pure filter + formatter logic for the /risks list view.
//
// All filter-related calculations + per-row formatting live here as
// pure functions so they can be vitest-unit-tested without spinning up
// React. The page imports these and applies them to the fetched
// `riskWire` rows.
//
// Constitutional commitment: this module knows nothing about React,
// useSearchParams, or the BFF. It is data-in, data-out.
//
// Slice 100 shipped 3 pills (treatment + severity band + owner). Slice
// 244 extended this to 6 pills by adding category + methodology +
// org_unit — the three mockup pills slice 100 deferred (see the
// "future extension" note that lived here before this slice). The data
// for all three was already on `riskWire` (slice 019 carried category +
// methodology; slice 067 carried org_unit_id), so the cost was purely
// UI plumbing.
//
// Constitutional anti-criterion P0-A3 (slice 100) honored: only fields
// that exist on `riskWire` (slice 019 + slice 067 additions) are
// referenced — no invented columns.

import type { Risk } from "@/lib/api/risks";

/**
 * The "all values" sentinel. Used as the default filter value on every
 * pill — selecting it disables that filter. The literal string "all"
 * round-trips cleanly through the URL query string.
 */
export const ALL = "all" as const;

/**
 * Severity bands per the slice-067 5x5 severity scalar (likelihood ×
 * impact, range 0..25). Boundaries chosen to mirror the mockup's
 * rose/amber/emerald color tiers:
 *
 *   high   = severity >= 15   (rose)
 *   medium = 8..14            (amber)
 *   low    = 1..7             (emerald)
 *   none   = 0                (no numeric score on inherent_score)
 *
 * `none` is bucketed separately so a risk with a malformed score (or a
 * FAIR risk with no L×I component) is not silently lumped into "low".
 * The pill exposes 4 explicit values; the user picks one. The default
 * `ALL` returns every row regardless of severity.
 */
export type SeverityBand = "high" | "medium" | "low" | "none";

export function severityBand(severity: number): SeverityBand {
  if (severity >= 15) return "high";
  if (severity >= 8) return "medium";
  if (severity >= 1) return "low";
  return "none";
}

export type RiskFilters = {
  treatment: string;
  severity: string;
  owner: string;
  // Slice 244 additions.
  category: string;
  methodology: string;
  org_unit: string;
};

export const DEFAULT_FILTERS: RiskFilters = {
  treatment: ALL,
  severity: ALL,
  owner: ALL,
  // Slice 244 additions — all default to ALL so the pill row starts
  // unfiltered and only narrows when the user picks a specific value.
  category: ALL,
  methodology: ALL,
  org_unit: ALL,
};

/**
 * True when no filter is narrowing the result set.
 */
export function isDefault(filters: RiskFilters): boolean {
  return (
    filters.treatment === ALL &&
    filters.severity === ALL &&
    filters.owner === ALL &&
    filters.category === ALL &&
    filters.methodology === ALL &&
    filters.org_unit === ALL
  );
}

/**
 * Narrow a risk list against the active filter set. The treatment +
 * owner + category + methodology + org_unit filters compare the exact
 * string from `riskWire`; the severity filter buckets the numeric
 * `severity` scalar into the four bands above.
 *
 * Unassigned-owner rows match `owner = "unassigned"` (the sentinel the
 * mockup uses) — the wire shape carries an empty string for an unset
 * treatment_owner, so the filter normalizes both shapes.
 *
 * Slice 244 — three new branches:
 *   - category: exact match against the wire enum (`risk_category`).
 *   - methodology: exact match against the wire enum
 *     (`risk_methodology`).
 *   - org_unit: exact match against the row's `org_unit_id` (UUID).
 *     Rows with no `org_unit_id` never match a specific filter value —
 *     a row with `org_unit_id = undefined` only passes when the filter
 *     is `ALL` (i.e., disabled). This is the same shape as how the
 *     other identity-bearing pills (owner) treat unset values.
 */
export function applyFilters(rows: Risk[], filters: RiskFilters): Risk[] {
  return rows.filter((row) => {
    if (filters.treatment !== ALL && row.treatment !== filters.treatment) {
      return false;
    }
    if (filters.severity !== ALL) {
      if (severityBand(row.severity) !== filters.severity) return false;
    }
    if (filters.owner !== ALL) {
      const ownerNorm = row.treatment_owner.trim() || "unassigned";
      if (ownerNorm !== filters.owner) return false;
    }
    if (filters.category !== ALL && row.category !== filters.category) {
      return false;
    }
    if (
      filters.methodology !== ALL &&
      row.methodology !== filters.methodology
    ) {
      return false;
    }
    if (filters.org_unit !== ALL && row.org_unit_id !== filters.org_unit) {
      return false;
    }
    return true;
  });
}

/**
 * Extract the unique owner set from a row list. Used to drive the
 * "Owner" pill options. Sorted alphabetically with the "unassigned"
 * sentinel pinned last so it stays visually distinct from real names.
 */
export function uniqueOwners(rows: Risk[]): string[] {
  const seen = new Set<string>();
  let hasUnassigned = false;
  for (const r of rows) {
    const norm = r.treatment_owner.trim();
    if (norm === "") {
      hasUnassigned = true;
    } else {
      seen.add(norm);
    }
  }
  const named = Array.from(seen).sort();
  return hasUnassigned ? [...named, "unassigned"] : named;
}

/**
 * Merge a partial filter update onto the existing filter set.
 */
export function setFilter(
  filters: RiskFilters,
  key: keyof RiskFilters,
  value: string,
): RiskFilters {
  return { ...filters, [key]: value };
}

/**
 * Clear all filters back to the default.
 */
export function clearFilters(): RiskFilters {
  return { ...DEFAULT_FILTERS };
}

// ----- formatters -----

/**
 * #2058 — the residual on the inherent-severity scale (1..25).
 *
 * The platform writes `residual_score` in more than one shape, and the
 * UI reads all of them so a derived residual never shows as pending:
 *
 *   - `{score, inherent_score, weighted_control_effectiveness, ...}` —
 *     the residual deriver (internal/risk/residual_store.go
 *     `residualBlob`). `score` = inherent × (1 − effectiveness), already
 *     on the 1..25 scale for a 5x5 risk.
 *   - `{severity}` — a precomputed scalar (aggregated parents).
 *   - `{likelihood, impact}` — an operator-entered 5x5 residual; the
 *     scalar is their product.
 *
 * Same precedence as the board generator's `extractSeverity`
 * (internal/board/generator.go). Returns `null` when no shape carries a
 * finite number: that is the only "pending evaluation" case.
 */
export function residualSeverity(score: unknown): number | null {
  if (score == null || typeof score !== "object") return null;
  const s = score as {
    score?: unknown;
    severity?: unknown;
    likelihood?: unknown;
    impact?: unknown;
  };
  const finite = (v: unknown): v is number =>
    typeof v === "number" && Number.isFinite(v);
  if (finite(s.score)) return s.score;
  if (finite(s.severity)) return s.severity;
  if (finite(s.likelihood) && finite(s.impact)) {
    const product = s.likelihood * s.impact;
    return Number.isFinite(product) ? product : null;
  }
  return null;
}

/**
 * Format a 1..25 severity scalar: whole numbers as-is, a fractional
 * residual (inherent × (1 − effectiveness)) to one decimal.
 */
export function formatSeverity(n: number): string {
  return Number.isInteger(n) ? String(n) : n.toFixed(1);
}

/**
 * Format a `residual_score` JSONB blob for display.
 *
 * A 5x5 residual renders on the same 1..25 scale as inherent severity
 * (#2058 — it used to be divided by 25 into 0..1, which sat beside a
 * 1..25 inherent number). A FAIR residual renders as dollars: a
 * FAIR-shaped blob via its annualized exposure, and a deriver `{score}`
 * blob on a FAIR risk (pass `methodology`) as the dollar scalar it is.
 * No usable residual renders as `"—"`.
 */
export function formatResidualScore(
  score: unknown,
  methodology?: string,
): string {
  const fair = fairScore(score);
  if (fair != null) return formatDollars(fair.annualized_loss_exposure);
  const n = residualSeverity(score);
  if (n == null) return "—";
  if (methodology === "fair") return formatDollars(n);
  return formatSeverity(n);
}

export type FairScore = {
  loss_event_frequency: number;
  loss_magnitude: number;
  annualized_loss_exposure: number;
};

export function fairScore(score: unknown): FairScore | null {
  if (score == null || typeof score !== "object") return null;
  const s = score as {
    loss_event_frequency?: unknown;
    loss_magnitude?: unknown;
    annualized_loss_exposure?: unknown;
    lef?: unknown;
    lm?: unknown;
  };
  const lossEventFrequency =
    typeof s.loss_event_frequency === "number"
      ? s.loss_event_frequency
      : typeof s.lef === "number"
        ? s.lef
        : null;
  const lossMagnitude =
    typeof s.loss_magnitude === "number"
      ? s.loss_magnitude
      : typeof s.lm === "number"
        ? s.lm
        : null;
  if (lossEventFrequency == null || lossMagnitude == null) return null;
  const derived = lossEventFrequency * lossMagnitude;
  const annualized =
    typeof s.annualized_loss_exposure === "number"
      ? s.annualized_loss_exposure
      : derived;
  if (!Number.isFinite(annualized)) return null;
  return {
    loss_event_frequency: lossEventFrequency,
    loss_magnitude: lossMagnitude,
    annualized_loss_exposure: annualized,
  };
}

export function formatDollars(n: number): string {
  if (!Number.isFinite(n)) return "—";
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: 0,
  }).format(n);
}

/**
 * Slice 680 / ATLAS-029 — residual evaluation state.
 *
 * A freshly-created risk has no residual_score yet: the create path
 * stores an empty `{}` JSONB (internal/risk/store.go `defaultResidual`)
 * and the residual deriver backfills `{score, inherent_score, ...}`
 * (slice 020 residual derivation). The old cell rendered a bare "—" for
 * that case, which reads as broken rather than "awaiting evaluation".
 *
 * This classifier distinguishes the two states the cell must render
 * differently:
 *
 *   - "pending": no residual yet — null / undefined / empty object /
 *     an object no `residualSeverity` shape reads. The common case for
 *     a brand-new risk. The cell renders a "Pending evaluation"
 *     affordance, NOT a bare dash.
 *   - "scored": any shape `residualSeverity` or `fairScore` reads — the
 *     cell renders the 1..25 severity (or FAIR dollars) via
 *     formatResidualScore. Before #2058 only `{likelihood, impact}`
 *     counted, so every deriver-written `{score}` residual read pending.
 *
 * Note: there is no separate "malformed" state. A partially-shaped
 * score (e.g. only likelihood) is indistinguishable from "not yet
 * evaluated" at the UI tier and the honest read is the same — the
 * evaluator has not produced a usable residual — so it is bucketed as
 * "pending". This keeps the affordance truthful without inventing a
 * scary error state for what is, on `main` today, only ever the
 * not-yet-evaluated case.
 */
export type ResidualState = "pending" | "scored";

export function residualState(score: unknown): ResidualState {
  if (fairScore(score) != null) return "scored";
  return residualSeverity(score) == null ? "pending" : "scored";
}

/**
 * #1867 — a rule-generated meta-risk whose pattern is no longer met.
 *
 * The aggregation engine never closes or deletes a meta-risk (canvas
 * §6.4). When the last child leaves the rule's window, loses the theme,
 * or is deleted, the next evaluation that touches the theme relinks the
 * meta-risk to zero children and scores it 0, which the grid renders as
 * a 1x1 cell on an open risk. This flags that state so the UI can say so
 * instead of showing a quiet low risk. True only for a score carrying the
 * engine's `rule_generated` flag with `child_count` exactly 0.
 */
export function patternNoLongerMet(score: unknown): boolean {
  if (score == null || typeof score !== "object") return false;
  const s = score as { rule_generated?: unknown; child_count?: unknown };
  return s.rule_generated === true && s.child_count === 0;
}

/**
 * Whether a risk has no review date. Since #2059 a risk created without
 * one is due created_at + the organisation review cadence, and existing
 * risks were backfilled, so an absent / empty `review_due_at` is a gap in
 * the data, not a state the evaluator will fill.
 */
export function reviewDuePending(reviewDueAt: string | undefined): boolean {
  return reviewDueAt == null || reviewDueAt.trim() === "";
}

/**
 * #2059 — a risk review is overdue once its due day has passed, compared
 * as UTC calendar days (the policy-review rule, #2075): a review due today
 * is not overdue yet. An absent or unparseable date is never overdue.
 */
export function reviewOverdue(
  reviewDueAt: string | undefined,
  now: Date = new Date(),
): boolean {
  if (reviewDuePending(reviewDueAt)) return false;
  const due = new Date(reviewDueAt!);
  if (Number.isNaN(due.getTime())) return false;
  return due.toISOString().slice(0, 10) < now.toISOString().slice(0, 10);
}

/**
 * Map a severity band to a Tailwind color class set for the pill in
 * the severity column. Centralised so the rose/amber/emerald palette
 * matches the mockup exactly.
 */
export function severityClasses(band: SeverityBand): string {
  switch (band) {
    case "high":
      return "bg-rose-100 text-rose-700";
    case "medium":
      return "bg-amber-100 text-amber-700";
    case "low":
      return "bg-emerald-100 text-emerald-700";
    case "none":
    default:
      return "bg-muted text-muted-foreground";
  }
}

/**
 * Display label for a severity band (#2058). Inherent and residual both
 * use it, so the two columns read on one scale.
 */
export function severityBandLabel(band: SeverityBand): string {
  switch (band) {
    case "high":
      return "High";
    case "medium":
      return "Medium";
    case "low":
      return "Low";
    case "none":
    default:
      return "None";
  }
}

/**
 * Map a formatted residual (the 1..25 scalar as a string) to the pill
 * classes of its severity band, the same palette the inherent column
 * uses. A dollar (FAIR) or "—" value is not banded and renders muted.
 */
export function residualClass(formatted: string): string {
  if (!/^-?\d+(\.\d+)?$/.test(formatted)) return "text-muted-foreground";
  return severityClasses(severityBand(parseFloat(formatted)));
}
