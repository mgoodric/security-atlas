// Slice 043 — investment vs coverage panel (per board-pack.html §05).
//
// Two-column layout: left = operator-entered spend; right = computed
// coverage delta + cost-per-coverage-point. The spend / baseline inputs
// are wired by the section-card (this component is pure presentational).

type InvestmentPanelProps = {
  spendUSD: number;
  coverageDelta: number;
  costPerCoveragePoint: number;
  // No baseline or no scoped framework (#2056 review): delta and cost n/a.
  notAvailable?: boolean;
};

export function InvestmentPanel({
  spendUSD,
  coverageDelta,
  costPerCoveragePoint,
  notAvailable = false,
}: InvestmentPanelProps) {
  const delta = Number(coverageDelta.toFixed(1));
  return (
    <div
      className="grid grid-cols-1 gap-5 md:grid-cols-2"
      data-testid="investment-panel"
    >
      <div className="rounded-lg border border-border p-5">
        <div className="mb-2 text-xs text-muted-foreground">
          Quarter program spend
        </div>
        <div
          className="mb-3 text-2xl font-semibold"
          data-testid="spend-display"
        >
          {formatUSD(spendUSD)}
        </div>
        <p className="text-xs text-muted-foreground">
          Operator-entered. v1 has no automated spend connector — type the
          quarter total in the section editor below and save.
        </p>
      </div>
      <div className="rounded-lg border border-border p-5">
        <div className="mb-2 text-xs text-muted-foreground">
          Coverage delta this quarter
        </div>
        <div
          className="mb-3 text-2xl font-semibold"
          data-testid="coverage-delta-display"
        >
          {notAvailable
            ? "n/a"
            : delta > 0
              ? `+${delta}`
              : delta === 0
                ? "flat"
                : delta}
          {!notAvailable && delta !== 0 ? " pts" : ""}
        </div>
        <p className="text-sm text-muted-foreground">
          Current coverage minus the baseline: coverage at the start of the
          90-day posture trend, or the operator-entered prior-quarter figure.
        </p>
        <div className="mt-3 border-t border-border pt-3 text-sm">
          <div className="mb-1 text-xs text-muted-foreground">
            Implied cost per coverage point
          </div>
          <div
            className="text-lg font-semibold text-foreground"
            data-testid="cost-per-coverage-point"
          >
            {notAvailable
              ? "n/a"
              : costPerCoveragePoint > 0
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
