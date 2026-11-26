// Slice 100 — pure formatter logic for the /risks register, plus the
// Category pill's options.
//
// #2132: the register filters, sorts and pages on the server, so the
// client-side narrowing that lived here (applyFilters, the owner and
// severity buckets, the filter state helpers) is gone. What stays is
// per-row formatting and the category options, as pure functions so they
// are vitest-unit-tested without React.

import type { Risk, RiskCategory } from "@/lib/api/risks";

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
 *   low    = above 0, under 8 (emerald)
 *   none   = 0                (no numeric score on inherent_score)
 *
 * Any value above 0 is at least low (#2185): a residual of 0.4 (strong
 * controls on a low inherent) is low, not none. Same bands as the Go
 * `risk.SeverityBand`. Never band a FAIR (dollar) value.
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
  if (severity > 0) return "low";
  return "none";
}

/**
 * #1817 — the Category pill's options: the organisation's categories by
 * name, matched by slug. Archived categories are listed only when
 * `showArchived` is on, marked "(archived)". A category the URL already
 * filters on stays listed even when hidden, so the pill never shows a
 * value it has no option for.
 */
export function categoryOptions(
  categories: RiskCategory[],
  showArchived: boolean,
  selected: string,
): { value: string; label: string }[] {
  const listed = categories
    .filter((c) => !c.archived || showArchived || c.slug === selected)
    .sort((a, b) => a.name.localeCompare(b.name));
  return [
    { value: ALL, label: "All categories" },
    ...listed.map((c) => ({
      value: c.slug,
      label: c.archived ? `${c.name} (archived)` : c.name,
    })),
  ];
}

/**
 * #1817 — how a risk's category reads: the organisation's name for it
 * (from the risk's `category_name`, else the category list, else the
 * slug), and whether that category is archived.
 */
export function categoryDisplay(
  row: Pick<Risk, "category" | "category_name">,
  categories: RiskCategory[],
): { name: string; archived: boolean } {
  const cat = categories.find((c) => c.slug === row.category);
  return {
    name: row.category_name || cat?.name || row.category,
    archived: cat?.archived ?? false,
  };
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
 * #2511 — whether a risk was logged without a score ("Score later", or an
 * MCP create_risk with no inherent_score). The platform stores its
 * inherent_score as `{}`; absent or null reads the same. Such a risk is
 * shown as Unscored, not as a severity of 0, and sits in no band.
 */
export function isUnscored(inherentScore: unknown): boolean {
  if (inherentScore == null) return true;
  return (
    typeof inherentScore === "object" &&
    !Array.isArray(inherentScore) &&
    Object.keys(inherentScore).length === 0
  );
}

export const UNSCORED_LABEL = "Unscored";

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
