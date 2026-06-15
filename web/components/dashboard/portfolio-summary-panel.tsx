"use client";

// Slice 750 — portfolio / multi-control AI evidence-summary card (dashboard).
//
// Renders the DETERMINISTIC TWO-LEVEL bounded cross-control rollup ALWAYS (cap
// controls-per-summary AND records-per-control, BOTH labeled honestly — AC-5,
// P0-750-2), plus a NON-BINDING, cited, local-default-Ollama summary of that
// rollup when the backend returns one (AC-6). The summary is a comprehension aid
// — there is NO approve/publish/export affordance anywhere in this component
// (AC-5, P0-502-3). When the summary is suppressed (generation unavailable, no
// evidence, a citation failed to verify, or a numeric claim did not match the
// rollup) the deterministic rollup still renders with a short honest note
// (graceful degradation, AC-7); the dashboard stays usable.
//
// When the tenant is on a cloud provider (slice 499 opt-in) the shared
// CloudRoutingBanner renders the "your data leaves this deployment" affordance —
// inherited for free, never re-implemented here (P0-502-6).
//
// The render decisions live in the node-testable view-model
// (portfolio-summary-view.ts); this component is a thin renderer over it.
// Cross-control sibling of slice 502's evidence-summary.tsx.

import { useQuery } from "@tanstack/react-query";

import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { CloudRoutingBanner } from "@/components/llm/cloud-routing-banner";
import {
  buildPortfolioSummaryView,
  formatPortfolioBounds,
  formatPortfolioRollupLine,
  formatPortfolioScope,
} from "@/components/dashboard/portfolio-summary-view";
import {
  fetchPortfolioEvidenceSummary,
  type PortfolioFilter,
} from "@/lib/api/portfolio-summary";

export function PortfolioSummaryPanel({
  filter,
}: {
  filter?: PortfolioFilter;
}) {
  const summaryQ = useQuery({
    queryKey: ["dashboard", "portfolio-summary", filter ?? {}],
    queryFn: () => fetchPortfolioEvidenceSummary(filter),
    // The summary is regenerated on demand (P0-502-4) and the model call is the
    // slow part — do not refetch on every window focus.
    refetchOnWindowFocus: false,
  });

  return (
    <Card data-testid="portfolio-summary-section">
      <CardHeader>
        <CardTitle>What your evidence shows across these controls</CardTitle>
        <CardDescription>
          Plain-language summary of current live evidence across a set of
          controls
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {/* P0-502-6: cloud-routing honesty affordance (renders only when the
            tenant opted into a cloud provider; nothing for the local default). */}
        <CloudRoutingBanner />
        {summaryQ.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : summaryQ.error || !summaryQ.data ? (
          <p className="text-sm text-muted-foreground">
            Portfolio evidence summary is unavailable right now.
          </p>
        ) : (
          <PortfolioSummaryBody data={summaryQ.data} />
        )}
      </CardContent>
    </Card>
  );
}

function PortfolioSummaryBody({
  data,
}: {
  data: Parameters<typeof buildPortfolioSummaryView>[0];
}) {
  const view = buildPortfolioSummaryView(data);
  return (
    <div className="space-y-3" data-testid="portfolio-summary-body">
      {/* Deterministic scope + BOTH bounds + rollup — ALWAYS rendered (AC-7);
          current live evidence only, clearly labeled (P0-502-5). */}
      <p
        className="text-sm font-medium text-foreground"
        data-testid="portfolio-summary-scope"
      >
        {formatPortfolioScope(data)}
      </p>
      <p
        className="text-sm text-muted-foreground"
        data-testid="portfolio-summary-bounds"
      >
        {formatPortfolioBounds(data)}
      </p>
      <p
        className="text-sm text-muted-foreground"
        data-testid="portfolio-summary-rollup"
      >
        {formatPortfolioRollupLine(data)}
      </p>

      {view.showSummary ? (
        <div className="space-y-2" data-testid="portfolio-summary-text-block">
          <p
            className="text-sm text-foreground"
            data-testid="portfolio-summary-text"
          >
            {view.text}
          </p>
          {view.citations.length > 0 ? (
            <div
              className="flex flex-wrap gap-1"
              data-testid="portfolio-summary-citations"
            >
              {view.citations.map((c) => (
                <Badge
                  key={`${c.kind}:${c.id}`}
                  variant="outline"
                  className="font-mono text-xs"
                >
                  {c.kind}:{c.id.slice(0, 8)}
                </Badge>
              ))}
            </div>
          ) : null}
          {/* AC-6: visible non-audit-artifact disclosure naming the model. This
              is the only metadata shown — there is deliberately NO approve /
              publish / export control here (AC-5, P0-502-3). */}
          <p
            className="text-xs italic text-muted-foreground"
            data-testid="portfolio-summary-disclosure"
          >
            {view.disclosure}
          </p>
        </div>
      ) : (
        <p
          className="text-xs text-muted-foreground"
          data-testid="portfolio-summary-degraded"
        >
          {view.degradedNote}
        </p>
      )}
    </div>
  );
}
