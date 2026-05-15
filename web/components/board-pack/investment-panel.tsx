// Slice 043 — investment vs coverage panel (per board-pack.html §05).
//
// Two-column layout: left = operator-entered spend; right = computed
// coverage delta + cost-per-coverage-point. The spend / baseline inputs
// are wired by the section-card (this component is pure presentational).

type InvestmentPanelProps = {
  spendUSD: number;
  coverageDelta: number;
  costPerCoveragePoint: number;
};

export function InvestmentPanel({
  spendUSD,
  coverageDelta,
  costPerCoveragePoint,
}: InvestmentPanelProps) {
  return (
    <div
      className="grid grid-cols-1 gap-5 md:grid-cols-2"
      data-testid="investment-panel"
    >
      <div className="rounded-lg border border-slate-200 p-5">
        <div className="mb-2 text-xs text-slate-500">Quarter program spend</div>
        <div
          className="mb-3 text-2xl font-semibold"
          data-testid="spend-display"
        >
          {formatUSD(spendUSD)}
        </div>
        <p className="text-xs text-slate-500">
          Operator-entered. v1 has no automated spend connector — type the
          quarter total in the section editor below and save.
        </p>
      </div>
      <div className="rounded-lg border border-slate-200 p-5">
        <div className="mb-2 text-xs text-slate-500">
          Coverage delta this quarter
        </div>
        <div
          className="mb-3 text-2xl font-semibold"
          data-testid="coverage-delta-display"
        >
          {coverageDelta > 0
            ? `+${coverageDelta}`
            : coverageDelta === 0
              ? "flat"
              : coverageDelta}
          {coverageDelta !== 0 ? " pts" : ""}
        </div>
        <p className="text-sm text-slate-600">
          Current coverage minus the operator-entered baseline.
        </p>
        <div className="mt-3 border-t border-slate-200 pt-3 text-sm">
          <div className="mb-1 text-xs text-slate-500">
            Implied cost per coverage point
          </div>
          <div
            className="text-lg font-semibold text-slate-900"
            data-testid="cost-per-coverage-point"
          >
            {costPerCoveragePoint > 0
              ? formatUSD(Math.round(costPerCoveragePoint))
              : "—"}
          </div>
        </div>
      </div>
    </div>
  );
}

function formatUSD(value: number): string {
  if (!Number.isFinite(value) || value === 0) return "$0";
  // Plain "$X,XXX" with comma grouping, no Intl (deterministic SSR).
  const sign = value < 0 ? "-" : "";
  const abs = Math.abs(value).toString();
  const grouped = abs.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return `${sign}$${grouped}`;
}
